# Supabase Deployment Guide — Phase 4

Running log of every step performed for Phase 4 of [VPS-INTEGRATION-PLAN.md](../../VPS-INTEGRATION-PLAN.md) — deploying a stripped-down, hardened self-hosted Supabase as host-level Docker Compose (deliberately outside Kubernetes, per the plan's architecture). Same format as the other guides: real commands, real output, reproducible manually.

## Step 1 — Build a stripped-down, hardened Compose stack

Full repo: [`keepass4web-supabase`](https://github.com/BryJavier/keepass4web-supabase) on GitHub.

**Kept:** `db` (Postgres), `supavisor` (pooler), `auth` (GoTrue), `rest` (PostgREST — also serves GraphQL via `pg_graphql`, no separate container), `storage` (Storage API), `meta` (postgres-meta, needed by Studio), `kong` (gateway), `studio` (admin UI). This matches the earlier prerequisite decisions: Storage enabled, Realtime/Functions/Analytics never enabled.

**Dropped entirely:** `realtime`, `functions` (edge-runtime), `analytics`/`logflare`, `vector`, `imgproxy` (no image-transform need for vault attachments).

**Pinned against `supabase/supabase` tag `v1.26.08`.** To avoid any risk of corrupting the SQL bootstrap scripts by hand-copying them through a fetch tool, vendored the boilerplate files (`volumes/db/*.sql`, `volumes/pooler/pooler.exs`, `volumes/api/kong-entrypoint.sh`) byte-for-byte via a pinned sparse checkout instead of retyping them:
```bash
git clone --depth 1 --branch v1.26.08 --filter=blob:none --sparse https://github.com/supabase/supabase.git /tmp/supabase-vendor
cd /tmp/supabase-vendor
git sparse-checkout set docker/volumes/db docker/volumes/api docker/volumes/pooler
```

**Two deliberate hardening changes on top of upstream, both explained in the repo's `docker-compose.yml` header comment:**

1. **Every port binding is explicit `127.0.0.1`-only.** Upstream's default `ports:` mappings bind `0.0.0.0` (all interfaces) unless told otherwise. On a VPS with a public IP, that would put Postgres (`5432`), Supavisor (`6543`), and Kong (`8000`/`8443`) directly reachable from the internet — exactly what the plan's firewall policy forbids. UFW's default-deny would likely have blocked external access anyway, but binding to localhost is a second, independent layer that doesn't rely on the firewall never being misconfigured.
2. **Studio bypasses Kong entirely.** Upstream's Kong config has a catch-all `/` route proxying to Studio with basic-auth in front. Since Kong sits behind Caddy at the *public* `supabase.<domain>` hostname, keeping that route would mean the Studio login page is reachable from the public internet — directly contradicting the plan's "public reverse proxy rejects Studio/dashboard routes." Removed the `dashboard` (and `mcp`/`mcp-blocker`) routes from `kong.yml` entirely; Studio is reached only via `studio.vpn.<domain>`, bound to the WireGuard interface, going straight to the Studio container with Caddy applying the same basic-auth Kong used to provide.

Everything else in `kong.yml` — including the `request-transformer` / `$LUA_AUTH_EXPR` machinery that translates the `apikey` header into `Authorization: Bearer <jwt>` for backends that expect it — is unchanged from upstream. That logic is load-bearing for normal `supabase-js` client requests (which send only `apikey`); don't strip it when trimming routes.

## Step 2 — Dedicated service account

Per the plan's "dedicated service account" requirement — Supabase doesn't run as the admin (`bryjavier`) account:
```bash
sudo useradd --system --create-home --home-dir /opt/keepass4web-supabase --shell /usr/sbin/nologin svc-supabase
sudo usermod -aG docker svc-supabase
```
A dedicated read-only GitHub deploy key was generated for `keepass4web-supabase` (same pattern as the Argo CD repo keys in Phase 3) and placed at `/opt/keepass4web-supabase/.ssh/id_ed25519`, owned by `svc-supabase`. The repo is cloned to `/opt/keepass4web-supabase/app`.

**Working with a locked-down home directory from an admin shell:** `svc-supabase`'s home is `700`, so `bryjavier` can't `cd` into it directly. Two things that matter when scripting around this: `sudo -u svc-supabase bash -c 'cd ... && ...'` works (the `cd` runs *as* that user, inside the sudo'd shell) where a plain `cd` beforehand fails; and reads from that directory as `bryjavier` need `sudo cat`, not a bare redirect.

## Step 3 — Generate and encrypt secrets (two different patterns)

**Per your request mid-setup:** all Supabase-related secrets, plus the KeePass4Web app's own secrets, are consolidated in `platform-secrets` — none of them live in the `keepass4web-deployments` ConfigMap. But they use **two different storage patterns**, because Supabase and KeePass4Web run in two different places:

### Pattern A — Supabase's own secrets (host-level, plain dotenv)
Supabase runs as Docker Compose directly on the VPS, never touching Kubernetes. Its secrets are a SOPS-encrypted **dotenv file**, decrypted straight into `.env` at deploy time — no Kubernetes Secret involved:
```bash
sops --config /dev/null --input-type dotenv --output-type dotenv \
  -e --age age1v9aduwg523a3q5486w9ktr2hkkryc7v2gtyrrkpmh7s5gd6ekusqkzckyx \
  generated-secrets.env > supabase-secrets.enc.env
```
Committed to `platform-secrets/secrets/supabase.env` — **not** referenced in the ksops `secret-generator.yaml` `files:` list, since it's not a Kubernetes manifest and would break `kustomize build` if ksops tried to parse it as one.

### Pattern B — KeePass4Web's app secrets (in-cluster Kubernetes Secret)
The Go/Rust services run in Kubernetes, so their secrets go through the same ksops pipeline from Phase 3 — a real `kind: Secret` manifest, SOPS-encrypted, decrypted automatically by Argo CD at sync time:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: keepass4web-secrets
  namespace: keepass4web
type: Opaque
stringData:
  APP_SESSION_SECRET: "..."
  SUPABASE_ANON_KEY: "..."          # same value as Supabase's own ANON_KEY below
  SUPABASE_SERVICE_ROLE_KEY: "..."  # same value as Supabase's own SERVICE_ROLE_KEY
  RUST_SERVICE_TOKEN: "..."
```
Committed (encrypted) to `platform-secrets/secrets/keepass4web-app-secrets.yaml`, and added to `secrets/secret-generator.yaml`'s `files:` list so ksops actually picks it up.

**`SUPABASE_ANON_KEY` and `SUPABASE_SERVICE_ROLE_KEY` are generated once and reused in both places** — they must be the exact same values the Go service sends to Supabase and the values Supabase itself validates, or authentication breaks. `keepass4web-deployments/keepass4web/configmap.yaml` was updated to remove the `SUPABASE_ANON_KEY` placeholder it previously held (see [Phase 3's guide](GITOPS-SECRETS-GUIDE.md) — it was a placeholder from before this repo existed) and documents the exact `envFrom` stanza Phase 5's Deployments need:
```yaml
envFrom:
  - configMapRef:
      name: keepass4web-config
  - secretRef:
      name: keepass4web-secrets
```

### Generating the secrets themselves

Random values via `openssl rand`; `ANON_KEY`/`SERVICE_ROLE_KEY` are hand-signed HS256 JWTs (no external JWT library needed — base64url-encode header + payload, HMAC-SHA256 sign, base64url-encode the signature):
```bash
b64url() { openssl base64 -A | tr '+/' '-_' | tr -d '='; }
sign_jwt() {
  local role=$1
  local header='{"alg":"HS256","typ":"JWT"}'
  local iat=$(date +%s)
  local exp=$((iat + 315360000))  # +10 years
  local payload="{\"role\":\"${role}\",\"iss\":\"supabase\",\"iat\":${iat},\"exp\":${exp}}"
  local h=$(printf '%s' "$header" | b64url)
  local p=$(printf '%s' "$payload" | b64url)
  local sig=$(printf '%s' "${h}.${p}" | openssl dgst -sha256 -hmac "$JWT_SECRET" -binary | b64url)
  printf '%s.%s.%s' "$h" "$p" "$sig"
}
ANON_KEY=$(sign_jwt anon)
SERVICE_ROLE_KEY=$(sign_jwt service_role)
```
Verified before trusting them — decoded the payload back out and confirmed `role: anon` / `role: service_role` with sane `iat`/`exp`, rather than assuming the signing script was correct.

Ten secrets total: `POSTGRES_PASSWORD`, `JWT_SECRET`, `ANON_KEY`, `SERVICE_ROLE_KEY`, `DASHBOARD_PASSWORD`, `PG_META_CRYPTO_KEY`, `SECRET_KEY_BASE`, `VAULT_ENC_KEY`, `APP_SESSION_SECRET`, `RUST_SERVICE_TOKEN`. Plaintext copies were deleted from the VPS filesystem immediately after encrypting (`~/.supabase-secrets/` now holds only the two `.enc.*` ciphertext files, `chmod 700`/`600`).

## Step 4 — Assemble `.env` and deploy

```bash
# Non-secret base + decrypted secrets, concatenated
sudo cat /opt/keepass4web-supabase/app/.env.example > /tmp/supabase.env.tmp
sops -d ~/.supabase-secrets/supabase-secrets.enc.env >> /tmp/supabase.env.tmp
sudo mv /tmp/supabase.env.tmp /opt/keepass4web-supabase/app/.env
sudo chown svc-supabase:svc-supabase /opt/keepass4web-supabase/app/.env
sudo chmod 600 /opt/keepass4web-supabase/app/.env

sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose pull'
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose up -d'
```

### Gotcha: Supavisor crash-looped, and the real error was hidden

Symptom: `supabase-pooler` cycled `Created → Starting → Restarting (1)` indefinitely. `docker compose logs supavisor` showed only:
```
Setting RLIMIT_NOFILE to 100000
/app/limits.sh: line 6: ulimit: open files: cannot modify limit: Operation not permitted
```
repeated, then nothing — exit code 1, no application-level error at all. Easy to misdiagnose as a Postgres connectivity problem (wrong password, `_supabase` database missing) since there's no obvious clue otherwise.

**Root cause:** Supavisor's own entrypoint tries to raise its container's `nofile` ulimit to 100000. **This VPS's Docker daemon already has `default-ulimits: nofile hard=64000`** — set back in [VPS-HARDENING-GUIDE.md](VPS-HARDENING-GUIDE.md) Phase 1, Step 8, for completely unrelated reasons (general resource-limit hygiene). A container can never raise its own ulimit *above* the daemon's hard cap, so `ulimit -n 100000` fails, and Supavisor's entrypoint script apparently treats that failure as fatal and aborts immediately — before Elixir/BEAM even starts, hence zero application-level output.

**Fix:** override the ulimit for just this one service in `docker-compose.yml`, rather than raising the Docker daemon's global default (which was set deliberately and applies for good reasons to every other container):
```yaml
supavisor:
  ulimits:
    nofile:
      soft: 100000
      hard: 100000
```
Verified fixed — `docker compose logs supavisor` after the fix shows real Supavisor startup output (`Connected to Postgres database`, `Running SupavisorWeb.Endpoint...`) instead of the truncated ulimit warning, and the container settled into `Up ... (healthy)` instead of restart-looping.

**General lesson:** a container failing with *no error output at all* right after a permission-denied message is worth checking against the host's own Docker daemon config — `default-ulimits` (or similar global settings) can silently break a specific container's own internal limit-raising logic without that container ever getting far enough to log a real diagnostic.

Final state — all 8 containers `Up ... (healthy)`:
```
supabase-auth      healthy
supabase-db        healthy   (no host port — reachable only via supavisor)
supabase-kong      healthy   127.0.0.1:8000, 127.0.0.1:8443
supabase-meta      healthy   (no host port — reachable only from Studio over the compose network)
supabase-pooler    healthy   127.0.0.1:6543
supabase-rest      healthy   (no host port)
supabase-storage   healthy   (no host port)
supabase-studio    healthy   127.0.0.1:3001
```

## Step 5 — Wire Caddy routing

`supabase.<domain>` → Kong (public, real Let's Encrypt cert, same as `app.<domain>` in Phase 2):
```caddyfile
supabase.bryan-javier-llm-local.cc {
	reverse_proxy localhost:8000
}
```

`studio.vpn.<domain>` → Studio directly, WireGuard-only (same `bind` pattern as `argocd.vpn` in Phase 3), with Caddy's `basic_auth` replacing the basic-auth Kong used to provide before its dashboard route was removed:
```bash
DASHBOARD_PASSWORD=$(sops -d ~/.supabase-secrets/supabase-secrets.enc.env | grep ^DASHBOARD_PASSWORD= | cut -d= -f2-)
HASH=$(sudo caddy hash-password --plaintext "$DASHBOARD_PASSWORD")
```
```caddyfile
https://studio.vpn.bryan-javier-llm-local.cc {
	bind 10.44.13.1
	basic_auth {
		admin <bcrypt hash>
	}
	reverse_proxy localhost:3001
}
```
(Caddy's `basicauth` directive name is deprecated in favor of `basic_auth` — used the current name directly rather than the alias.)

## Step 6 — Verify end-to-end

```
$ curl -H "apikey: <real ANON_KEY>" https://supabase.bryan-javier-llm-local.cc/auth/v1/health
{"version":"v2.189.0","name":"GoTrue",...}                          # real backend response

$ curl --resolve studio.vpn...:443:10.44.13.1 https://studio.vpn... (no credentials)
401                                                                   # basic_auth correctly enforced

$ curl --resolve studio.vpn...:443:169.58.149.18 https://studio.vpn... -i   (public IP)
HTTP/2 200
content-length: 0                                                    # same harmless artifact as argocd.vpn — never reaches the backend

$ sudo ss -tlnp | grep -E ':5432|:6543|:8000|:8443|:3001'
127.0.0.1:8443 ... 127.0.0.1:3001 ... 127.0.0.1:8000 ... 127.0.0.1:6543
                                                                       # note: no 5432 line at all — Postgres has no host port, period
```
All Phase 4 exit criteria met: Supabase is healthy, API access is HTTPS-only with a real cert, Studio is VPN-only (confirmed via the same public-IP-returns-empty-response pattern verified for Argo CD in Phase 3), Postgres/Supavisor were never bound beyond localhost.

**Known follow-ups carried forward:**
- No SMTP provider configured yet — `ENABLE_EMAIL_AUTOCONFIRM=true` is a pragmatic placeholder until real email confirmation is wired up; this is a genuinely weaker security posture (anyone can sign up with any email) and should be revisited before onboarding real users.
- Local (`file`) storage backend, not external object storage — was left as an open/deferred decision in the original prerequisites.
- Backup destination for Postgres/Storage still deferred (from the original prerequisite checklist) — the plan's Phase 4 exit criteria calls for "a restore test succeeds," which can't happen until that's decided.
- `SUPABASE_CONTAINER_URL` in the `keepass4web-deployments` ConfigMap is still a placeholder — needs the actual reachable address from inside a Kubernetes pod to this host-level Compose stack, worth finalizing now that Supabase is actually running (see Quick command cheat sheet below for how to find the right address).

## Quick command cheat sheet

### Compose stack (run as `svc-supabase`, from `/opt/keepass4web-supabase/app`)
```bash
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose ps'
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose logs <service> --tail=50'
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose restart <service>'
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose up -d'   # after pulling new commits
```

### Pulling new commits to the deploy checkout
```bash
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && git -c core.sshCommand="ssh -i /opt/keepass4web-supabase/.ssh/id_ed25519" pull'
```

### Secrets
```bash
export SOPS_AGE_KEY_FILE=$HOME/.sops-keys/keepass4web-platform.agekey
sops -d ~/.supabase-secrets/supabase-secrets.enc.env                     # view all Supabase host secrets
sops -d ~/.supabase-secrets/supabase-secrets.enc.env | grep ^ANON_KEY=   # one specific value

# Rebuilding .env after a secret changes:
sudo cat /opt/keepass4web-supabase/app/.env.example > /tmp/supabase.env.tmp
sops -d ~/.supabase-secrets/supabase-secrets.enc.env >> /tmp/supabase.env.tmp
sudo mv /tmp/supabase.env.tmp /opt/keepass4web-supabase/app/.env
sudo chown svc-supabase:svc-supabase /opt/keepass4web-supabase/app/.env
sudo chmod 600 /opt/keepass4web-supabase/app/.env
sudo -u svc-supabase bash -c 'cd /opt/keepass4web-supabase/app && docker compose up -d'  # re-apply
```

### Studio dashboard password

**Get the current password:**
```bash
export SOPS_AGE_KEY_FILE=$HOME/.sops-keys/keepass4web-platform.agekey
sops -d ~/.supabase-secrets/supabase-secrets.enc.env | grep ^DASHBOARD_PASSWORD=
```

**Generate a new one and bind it to Supabase.** Note what "binding" actually means in this stripped-down setup: `DASHBOARD_PASSWORD` isn't read by any Docker container anymore (Kong's dashboard route — the thing that used to consume it — was deliberately removed in Step 1). The only place this password is actually enforced is **Caddy's `basic_auth` block** for `studio.vpn.<domain>`, as a bcrypt hash. So rotating it means: generate → re-encrypt into `platform-secrets` (so the source of truth stays in Git) → re-hash → update the Caddyfile → reload. Tested end-to-end while writing this (old password confirmed rejected with `401`, new one confirmed accepted):

```bash
export SOPS_AGE_KEY_FILE=$HOME/.sops-keys/keepass4web-platform.agekey

# 1. Generate a new password
NEWPASS=$(openssl rand -base64 24 | tr -d '\n=+/' | head -c 24)

# 2. Update it in the SOPS-encrypted secret (decrypt, edit, re-encrypt)
sops -d ~/.supabase-secrets/supabase-secrets.enc.env > /tmp/supabase-secrets-plain.env
sed -i "s/^DASHBOARD_PASSWORD=.*/DASHBOARD_PASSWORD=${NEWPASS}/" /tmp/supabase-secrets-plain.env
sops --config /dev/null --input-type dotenv --output-type dotenv \
  -e --age age1v9aduwg523a3q5486w9ktr2hkkryc7v2gtyrrkpmh7s5gd6ekusqkzckyx \
  /tmp/supabase-secrets-plain.env > /tmp/supabase-secrets-new.enc.env
shred -u /tmp/supabase-secrets-plain.env 2>/dev/null || rm -f /tmp/supabase-secrets-plain.env
mv /tmp/supabase-secrets-new.enc.env ~/.supabase-secrets/supabase-secrets.enc.env

# 3. Re-hash and bind it into Caddy
NEWHASH=$(sudo caddy hash-password --plaintext "$NEWPASS")
sudo sed -i "s|admin \$2a\\\$[^ ]*|admin ${NEWHASH}|" /etc/caddy/Caddyfile
sudo caddy validate --config /etc/caddy/Caddyfile && sudo systemctl reload caddy

# 4. Push the updated encrypted secret back to platform-secrets (git stays source of truth)
#    — from your local clone: pull, overwrite secrets/supabase.env with the new
#    encrypted content from ~/.supabase-secrets/supabase-secrets.enc.env, commit, push.

echo "New password: $NEWPASS"   # save it now — this is the only time it's shown in plaintext
```

If `POSTGRES_PASSWORD` or another container-consumed secret ever needs rotating instead, the same steps 2 and 4 apply, but step 3 is different — that value *is* read by containers via `.env`, so rebuilding `.env` (see the "Secrets" section above) and `docker compose up -d` is the binding step, not a Caddy hash.

### Verifying access controls
```bash
# Public API (should work with a real apikey):
curl -H "apikey: <ANON_KEY>" https://supabase.bryan-javier-llm-local.cc/auth/v1/health

# Studio VPN-only check (see GITOPS-SECRETS-GUIDE.md's --resolve pattern):
curl -sk --resolve studio.vpn.bryan-javier-llm-local.cc:443:10.44.13.1 https://studio.vpn.bryan-javier-llm-local.cc      # should prompt for auth (401 without creds)
curl -sk --resolve studio.vpn.bryan-javier-llm-local.cc:443:169.58.149.18 https://studio.vpn.bryan-javier-llm-local.cc -i # public IP — should be empty/harmless

# Confirm no service is bound beyond localhost:
sudo ss -tlnp | grep -E ':5432|:6543|:8000|:8443|:3001'
```

### Finding the right SUPABASE_CONTAINER_URL for Phase 5
```bash
# The VPS's address on the Docker bridge network Compose created, reachable from
# a Kubernetes pod only if that pod can route to the host — verify this actually
# works before hardcoding it into the ConfigMap:
sudo docker network inspect keepass4web-supabase_default | grep Gateway
```
