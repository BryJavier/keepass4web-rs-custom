# GitOps and Secrets Guide — Phase 3

Running log of every step performed for Phase 3 of [VPS-INTEGRATION-PLAN.md](../../VPS-INTEGRATION-PLAN.md) — the four platform repos, Argo CD, and SOPS+age encrypted secrets — on top of the Kubernetes cluster from [KUBERNETES-SETUP-GUIDE.md](KUBERNETES-SETUP-GUIDE.md). Same format as the other guides: real commands, real output, reproducible manually.

## Step 1 — Create the four platform repositories

```bash
gh repo create BryJavier/platform-deployments --private --description "Kubernetes manifests and immutable image references for KeePass4Web"
gh repo create BryJavier/platform-bootstrap --private --description "Argo CD root/apps and generic platform components"
gh repo create BryJavier/platform-secrets --private --description "SOPS-encrypted secrets only"
gh repo create BryJavier/platform-supabase --private --description "Pinned Supabase Compose definition and host deployment automation"
```

Each was cloned locally, scaffolded with a `README.md` explaining its purpose (and a placeholder `keepass4web/namespace.yaml` in the deployments repo, and an `apps/` directory in `platform-bootstrap`), committed, and pushed to `main`:
```bash
git add -A && git commit -m "Scaffold repo: ..." && git branch -M main && git push -u origin main
```

**Renamed shortly after creation:** `platform-deployments` → `keepass4web-deployments`, `platform-supabase` → `keepass4web-supabase` (the other two, `platform-bootstrap` and `platform-secrets`, stayed as-is — they're genuinely generic/platform-wide, not KeePass4Web-specific, unlike the deployments and Supabase repos which only ever hold this one app's manifests and compose stack).
```bash
gh repo rename keepass4web-deployments -R BryJavier/platform-deployments --yes
gh repo rename keepass4web-supabase -R BryJavier/platform-supabase --yes
```
GitHub automatically redirects the old repo URLs, but local clones' remotes needed a manual fix (redirects work for `git clone`/`fetch` over HTTPS, but existing `origin` remotes stay pointed at the pre-rename URL until updated):
```bash
git remote set-url origin https://github.com/BryJavier/keepass4web-deployments.git
git remote set-url origin https://github.com/BryJavier/keepass4web-supabase.git
```
Final repo names used throughout the rest of this guide and `platform-bootstrap`'s `Application` manifests: **`keepass4web-deployments`**, **`platform-bootstrap`**, **`platform-secrets`**, **`keepass4web-supabase`**.

### Gotcha: branch protection requires GitHub Pro for private repos

Attempted the standard branch-protection API call:
```bash
gh api repos/BryJavier/platform-bootstrap/branches/main/protection -X PUT ...
```
Result on all 4 repos:
```json
{"message":"Upgrade to GitHub Pro or make this repository public to enable this feature.","status":"403"}
```
GitHub gates branch protection (required PRs, required reviews, force-push/delete prevention) behind Pro/Team for **private** repos on a free personal account — it's available for free on public repos, but the plan explicitly calls for private repos here.

**Decision: skipped branch protection for now.** For solo work this is an acceptable gap — reviewed care with direct pushes substitutes for enforced PR review. Revisit if:
- A second contributor joins (branch protection becomes much more valuable with more than one committer), or
- The account is upgraded to GitHub Pro, or
- You decide the risk of an accidental direct push to `main` reconciling straight into the cluster (once Argo CD is watching) is worth avoiding — Argo CD applies whatever is on `main` regardless of protection, so protection here is about preventing *mistakes before they reach Git*, not a security boundary Argo CD itself depends on.

## Step 2 — Install Argo CD (pinned release)

```bash
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/v2.13.2/manifests/install.yaml
kubectl wait --for=condition=available --timeout=180s deployment/argocd-server -n argocd
```
All 7 Argo CD pods (`application-controller`, `applicationset-controller`, `dex-server`, `notifications-controller`, `redis`, `repo-server`, `server`) came up `Running`.

### Exposing the UI at `argocd.vpn.<domain>` (WireGuard-only)

Per the plan, Argo CD's UI must only be reachable through WireGuard even though its hostname resolves publicly. Three parts:

**1. Serve plain HTTP internally**, since Caddy terminates TLS in front of it (the standard pattern for Argo CD behind an external reverse proxy):
```bash
kubectl patch deployment argocd-server -n argocd --type='json' \
  -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--insecure"}]'
```

**2. Expose via NodePort**, same pattern as `ingress-nginx` in Phase 2:
```bash
kubectl patch svc argocd-server -n argocd -p '{"spec":{"type":"NodePort","ports":[{"name":"http","port":80,"targetPort":8080,"nodePort":30943},{"name":"https","port":443,"targetPort":8080}]}}'
```

**3. Bind Caddy's site block to the WireGuard interface IP only**, appended to `/etc/caddy/Caddyfile`:
```caddyfile
https://argocd.vpn.bryan-javier-llm-local.cc {
	bind 10.44.13.1
	reverse_proxy localhost:30943
}
```

### Gotcha #1: `tls internal` fights the automatic ACME cert — don't add it

First instinct was to force Caddy's local/self-signed CA (`tls internal`) reasoning that Let's Encrypt's HTTP-01 validator, running from the public internet, couldn't possibly reach a listener bound only to a private WireGuard IP. **That reasoning was wrong.** Caddy's ACME HTTP-01 challenge solver runs through a *shared* listener across the whole instance (the public `app.`/`supabase.` sites are listening on `0.0.0.0:80`), and it will answer the `/.well-known/acme-challenge/` path for *any* domain Caddy has been asked to manage a cert for — regardless of which specific site block's actual traffic is bound to a private interface. So a real Let's Encrypt cert for `argocd.vpn.bryan-javier-llm-local.cc` was issued successfully on the very first attempt, without needing `tls internal` at all.

Adding `tls internal` afterward created **two conflicting TLS policies for the same domain** (one ACME-issued, one local-CA), which made Caddy's Go TLS layer fail SNI certificate selection outright — every handshake attempt returned `TLS alert, internal error (592)`, even via plain `openssl s_client`. Fix: removed the `tls internal` line, and deleted the stray local-CA cert Caddy had already written to `/var/lib/caddy/.local/share/caddy/certificates/local/` (a stale on-disk cert for the same domain, cached independently of the live config, kept re-triggering the conflict on every reload/restart until removed):
```bash
sudo sed -i '/tls internal/d' /etc/caddy/Caddyfile
sudo rm -rf /var/lib/caddy/.local/share/caddy/certificates/local/argocd.vpn.bryan-javier-llm-local.cc
sudo systemctl restart caddy
```

**Takeaway:** the cert issuer (Let's Encrypt vs. local CA) has nothing to do with whether a VPN-only site is actually reachable — that's entirely controlled by `bind`. Don't add `tls internal` defensively; let automatic HTTPS do its thing, since the shared ACME challenge listener will get it a real cert regardless of where the site's actual traffic is bound.

### Gotcha #2: testing HTTPS against a bare IP sends no SNI — false negative

While debugging Gotcha #1, `curl -k https://10.44.13.1` kept failing even after the real fix was in place, which looked like the fix hadn't worked. It had — the test was wrong: curl derives the TLS SNI from the URL's hostname, and **an IP-literal URL sends no SNI at all**. Caddy's listener needs SNI to pick the right certificate/route for a `bind`-restricted site, so a no-SNI probe against it isn't a meaningful test either way. Confirmed this by cross-checking with `openssl s_client -connect 10.44.13.1:443 -servername argocd.vpn.bryan-javier-llm-local.cc`, which supplies SNI explicitly and succeeded immediately, proving the server side was fine all along.

**Correct way to test a `bind`-restricted vhost from the command line** — force curl to resolve the real hostname to whichever IP you're testing, so SNI is sent correctly:
```bash
curl -sk --resolve argocd.vpn.bryan-javier-llm-local.cc:443:10.44.13.1 https://argocd.vpn.bryan-javier-llm-local.cc/api/version
```

### Confirming the WireGuard-only restriction actually holds

Compared a real Argo CD API path via both IPs:
```
$ curl -sk --resolve argocd.vpn.bryan-javier-llm-local.cc:443:169.58.149.18 https://argocd.vpn.bryan-javier-llm-local.cc/api/version -i   # public IP
HTTP/2 200
content-length: 0                                    # empty — never reached the backend

$ curl -sk --resolve argocd.vpn.bryan-javier-llm-local.cc:443:10.44.13.1 https://argocd.vpn.bryan-javier-llm-local.cc/api/version -i        # WireGuard IP
HTTP/2 200
content-type: application/json
via: 1.1 Caddy
{"Version":"v2.13.2+dc43124"}                          # real backend response
```
The public interface's shared listener still completes a valid TLS handshake for this domain (since the cert is cached instance-wide) and returns an empty `200` — a harmless artifact of Caddy's fallback behavior, not a proper `404` — but it never actually routes to the Argo CD backend. Confirmed via the `content-type`/`via` headers and real JSON body only appearing through the WireGuard-bound path. **This is a cosmetic imperfection worth knowing about (an empty 200 instead of 404 on public probes), not a security gap** — zero application functionality or data crosses to the public interface.

### Retrieving the initial admin password

Deliberately not fetched/printed here — retrieve it yourself over the VPN so it never passes through this session:
```bash
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d
```
Log in at `https://argocd.vpn.bryan-javier-llm-local.cc` (requires WireGuard connected) as `admin`, then rotate this password and delete the `argocd-initial-admin-secret` Secret per Argo CD's own recommendation.

## Step 3 — SOPS + age for `platform-secrets`

```bash
sudo apt-get install -y age
curl -fsSL -o /tmp/sops.deb "https://github.com/getsops/sops/releases/download/v3.9.4/sops_3.9.4_amd64.deb"
sudo dpkg -i /tmp/sops.deb
```

Generated the keypair and stored the **private** key as a restricted in-cluster Secret (readable only by cluster admins — this is a single-admin cluster, so no extra RBAC needed beyond that):
```bash
mkdir -p ~/.sops-keys && umask 077
age-keygen -o ~/.sops-keys/keepass4web-platform.agekey
kubectl create secret generic sops-age-key -n argocd --from-file=keys.txt=$HOME/.sops-keys/keepass4web-platform.agekey
```
Public key (safe to share, used only for encryption): `age1v9aduwg523a3q5486w9ktr2hkkryc7v2gtyrrkpmh7s5gd6ekusqkzckyx`

**The private key was also delivered directly to the user as a file** (never pasted into chat/logs — the automation had a safety block that refused to print it to a terminal, which is correct behavior) for offline encrypted backup, per the plan's "keep the private age key... in encrypted offline recovery storage" requirement. Local temp copies were deleted immediately after delivery.

`platform-secrets/.sops.yaml`:
```yaml
creation_rules:
  - path_regex: secrets/.*\.yaml$
    encrypted_regex: ^(data|stringData)$
    age: age1v9aduwg523a3q5486w9ktr2hkkryc7v2gtyrrkpmh7s5gd6ekusqkzckyx
```

Proved the encrypt/decrypt round-trip with a placeholder secret before wiring anything into Argo CD:
```bash
sops --config /dev/null -e --age <public-key> --encrypted-regex '^(data|stringData)$' example-secret.yaml > example-secret.enc.yaml
sops -d example-secret.enc.yaml   # confirmed the real plaintext value came back
```
Committed only the **encrypted** file to `platform-secrets/secrets/example-secret.yaml` — the plaintext version never touched Git.

## Step 4 — Wire automatic in-cluster decryption (Argo CD + ksops)

This is what actually makes "secrets decrypted only at runtime" true, rather than just an intention — without this, Argo CD would apply the raw `ENC[...]` ciphertext directly as the Secret's value, which is exactly what happened for a few minutes during setup (see gotchas below) before this was working.

**Pattern:** a sidecar container running [ksops](https://github.com/viaduct-ai/kustomize-sops) (a kustomize plugin for SOPS) alongside `argocd-repo-server`, registered as an Argo CD Config Management Plugin (CMP). The age private key is mounted into that sidecar from the `sops-age-key` Secret created in Step 3. When Argo CD syncs the `platform-secrets` Application, it invokes this plugin instead of rendering plain YAML, and the plugin decrypts in-memory before the manifest is ever applied.

```bash
kubectl apply -f - <<'YAML'
apiVersion: v1
kind: ConfigMap
metadata:
  name: ksops-cmp-plugin
  namespace: argocd
data:
  plugin.yaml: |
    apiVersion: argoproj.io/v1alpha1
    kind: ConfigManagementPlugin
    metadata:
      name: ksops
    spec:
      generate:
        command: ["kustomize"]
        args: ["build", "--enable-alpha-plugins", "--enable-exec"]
      discover:
        find:
          glob: "./kustomization.yaml"
YAML
```

Patched `argocd-repo-server` to add: an `initContainer` that copies the `ksops` binary into the exact directory structure `kustomize`'s plugin loader expects, and a sidecar container running the CMP server itself.

```bash
kubectl patch deployment argocd-repo-server -n argocd --type='json' -p='[
  {"op":"add","path":"/spec/template/spec/volumes/-","value":{"name":"kustomize-plugin-home","emptyDir":{}}},
  {"op":"add","path":"/spec/template/spec/initContainers/-","value":{
    "name":"ksops-plugin-init",
    "image":"viaductoss/ksops:v4.3.2",
    "command":["sh","-c","mkdir -p /plugin-home/kustomize/plugin/viaduct.ai/v1/ksops && cp /usr/local/bin/ksops /plugin-home/kustomize/plugin/viaduct.ai/v1/ksops/ksops && chmod +x /plugin-home/kustomize/plugin/viaduct.ai/v1/ksops/ksops"],
    "volumeMounts":[{"mountPath":"/plugin-home","name":"kustomize-plugin-home"}]
  }}
]'
kubectl patch deployment argocd-repo-server -n argocd --type='json' -p='[
  {"op":"add","path":"/spec/template/spec/containers/-","value":{
    "name":"ksops",
    "command":["/var/run/argocd/argocd-cmp-server"],
    "image":"viaductoss/ksops:v4.3.2",
    "securityContext":{"runAsNonRoot":true,"runAsUser":999},
    "volumeMounts":[
      {"mountPath":"/var/run/argocd","name":"var-files"},
      {"mountPath":"/home/argocd/cmp-server/plugins","name":"plugins"},
      {"mountPath":"/tmp","name":"ksops-tmp"},
      {"mountPath":"/home/argocd/cmp-server/config/plugin.yaml","subPath":"plugin.yaml","name":"ksops-cmp-plugin"},
      {"mountPath":"/.config/sops/age/keys.txt","subPath":"keys.txt","name":"sops-age"},
      {"mountPath":"/plugin-home","name":"kustomize-plugin-home"}
    ],
    "env":[
      {"name":"XDG_CONFIG_HOME","value":"/plugin-home"},
      {"name":"SOPS_AGE_KEY_FILE","value":"/.config/sops/age/keys.txt"}
    ]
  }}
]'
```
(plus `ksops-cmp-plugin` and `sops-age` volumes referencing the ConfigMap and `sops-age-key` Secret from Step 3 — see the full deployment YAML on the cluster with `kubectl get deployment argocd-repo-server -n argocd -o yaml` as the authoritative current state.)

`platform-secrets/secrets/kustomization.yaml`:
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
generators:
  - secret-generator.yaml
```
`platform-secrets/secrets/secret-generator.yaml`:
```yaml
apiVersion: viaduct.ai/v1
kind: ksops
metadata:
  name: secret-generator
files:
  - example-secret.yaml
```
`platform-bootstrap/apps/platform-secrets.yaml` — the Application source needs to explicitly reference the plugin:
```yaml
spec:
  source:
    repoURL: git@github.com:BryJavier/platform-secrets.git
    targetRevision: main
    path: secrets
    plugin:
      name: ksops
```

### Gotchas (four separate bugs, found and fixed by testing end-to-end, not assumed)

1. **Wrong image, twice.** First tried `viaduct/ksops` (doesn't exist on Docker Hub), then `ghcr.io/viaduct-ai/ksops` (403 — not published there). The actual published image is **`viaductoss/ksops`** on Docker Hub (note: different org name than the GitHub repo, `viaduct-ai/kustomize-sops`).
2. **Wrong `discover.find.glob`.** Initially `./*/kustomization.yaml` (one directory level too deep) — since the Application's `path: secrets` already sets the plugin's working directory to that folder, the correct glob is `./kustomization.yaml`.
3. **Socket name mismatch from an unnecessary `version: v1` field** in `plugin.yaml`. That field makes the CMP server listen on `ksops-v1.sock`, but Argo CD's main repo-server container looks for a socket named after the **sidecar container's name** (`ksops.sock`) — removing the `version` field fixed the mismatch (confirmed via `Unable to connect to config management plugin service` in the repo-server's own logs, not just the plugin sidecar's).
4. **`XDG_CONFIG_HOME` fix for kustomize (gotcha #2's real fix) silently broke SOPS's own default key-lookup path**, since SOPS also honors `XDG_CONFIG_HOME` for finding `sops/age/keys.txt` by default. Fixed by setting `SOPS_AGE_KEY_FILE` explicitly to the exact mounted path (`/.config/sops/age/keys.txt`), which takes precedence over the `XDG_CONFIG_HOME`-derived default.

**Verified the actual decrypted value ends up in the cluster** — not just "sync status: Synced," which alone wouldn't prove decryption happened correctly:
```
$ kubectl get secret example-secret -n argocd -o jsonpath='{.data.example-key}' | base64 -d
this-is-a-placeholder-value-to-prove-the-sops-roundtrip-works
```
This matches the original plaintext from Step 3's round-trip test exactly — confirming Git only ever stored ciphertext, and decryption happened in-cluster at sync time via the mounted age key, exactly as the plan requires.

---

## Quick command cheat sheet

Kept up to date as each Phase 3 step progresses.

### GitHub repos
```bash
gh repo list BryJavier --limit 10
gh repo view BryJavier/platform-bootstrap --json defaultBranchRef,visibility,description
gh repo clone BryJavier/<repo>
gh repo rename <new-name> -R BryJavier/<old-name> --yes
```

### Argo CD
```bash
kubectl get pods -n argocd
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' | base64 -d   # initial admin password
argocd login argocd.vpn.bryan-javier-llm-local.cc --username admin   # CLI login, needs WireGuard active
kubectl get applications -n argocd                                    # once Applications exist (next steps)
```

### Generate a new Argo CD admin password
```bash
# Via the CLI (needs WireGuard active + already logged in as admin):
argocd account update-password --account admin

# Or without the CLI, generate a bcrypt hash yourself and patch the Secret directly
# (needs `htpasswd`, from apache2-utils: sudo apt-get install -y apache2-utils):
NEWPASS=$(openssl rand -base64 24)
HASH=$(htpasswd -nbBC 10 "" "$NEWPASS" | tr -d ':\n' | sed 's/^\$2y\$/\$2a\$/')
kubectl -n argocd patch secret argocd-secret \
  -p "{\"stringData\": {\"admin.password\": \"$HASH\", \"admin.passwordMtime\": \"$(date +%FT%T%Z)\"}}"
echo "$NEWPASS"   # save this somewhere safe, it's shown only once
# Then delete the old initial-admin secret, no longer needed:
kubectl -n argocd delete secret argocd-initial-admin-secret --ignore-not-found
```

### Testing a WireGuard-bound (`bind`-restricted) Caddy vhost correctly
```bash
# Always use --resolve so the real hostname (and correct SNI) is sent, never a bare IP:
curl -sk --resolve <hostname>:443:10.44.13.1 https://<hostname>/some/path      # via VPN interface — should reach the backend
curl -sk --resolve <hostname>:443:169.58.149.18 https://<hostname>/some/path  # via public interface — should NOT reach the backend
```

### SOPS + age
```bash
sops -e --age <public-key> --encrypted-regex '^(data|stringData)$' plain.yaml > encrypted.yaml   # encrypt (needs only the public key)
sops -d encrypted.yaml                                                                             # decrypt (needs SOPS_AGE_KEY_FILE / the private key)
kubectl get secret sops-age-key -n argocd                                                           # confirm the in-cluster private key still exists
```

### Encrypting and adding a new secret (the actual day-to-day workflow)
```bash
cd platform-secrets/secrets
cat > my-new-secret.yaml <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: my-new-secret
  namespace: argocd   # change to the target namespace
type: Opaque
stringData:
  some-key: some-real-value
EOF
sops -e --age age1v9aduwg523a3q5486w9ktr2hkkryc7v2gtyrrkpmh7s5gd6ekusqkzckyx --encrypted-regex '^(data|stringData)$' -i my-new-secret.yaml   # encrypts in place
# then add "my-new-secret.yaml" to the `files:` list in secret-generator.yaml, commit, push
```

### Split DNS (`.vpn` hostnames)
```bash
sudo systemctl status dnsmasq
dig @10.44.13.1 argocd.vpn.bryan-javier-llm-local.cc +short   # should return 10.44.13.1
dig @10.44.13.1 studio.vpn.bryan-javier-llm-local.cc +short   # should return 10.44.13.1
sudo cat /etc/dnsmasq.conf
# On a client: check which DNS server the active WireGuard tunnel is actually using
scutil --dns | grep -A3 "resolver" | grep -B1 "10.44.13.1"   # macOS
```

### Debugging the ksops plugin pipeline
```bash
kubectl get pods -n argocd -l app.kubernetes.io/name=argocd-repo-server   # should show 2/2 (main + ksops sidecar)
kubectl logs -n argocd deployment/argocd-repo-server -c ksops --tail=30           # plugin-side errors
kubectl logs -n argocd deployment/argocd-repo-server -c argocd-repo-server --tail=30   # socket-connection errors surface here, not the plugin log
kubectl exec -n argocd deployment/argocd-repo-server -c ksops -- ls /home/argocd/cmp-server/plugins/    # should show exactly "ksops.sock"
kubectl patch application platform-secrets -n argocd --type merge -p '{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}'
kubectl get application platform-secrets -n argocd -o jsonpath='{.status.conditions}'   # full error text when stuck
```

## Step 5 — Split DNS for the `.vpn` hostnames (closing a gap from DNS-SETUP-GUIDE.md)

**Symptom reported by the user:** `https://argocd.vpn.bryan-javier-llm-local.cc` wouldn't load in the browser even while connected to WireGuard.

**Root cause:** [DNS-SETUP-GUIDE.md](DNS-SETUP-GUIDE.md) originally chose "Option A" (rely on the public A record + Caddy's `bind` restriction) and left real split DNS ("Option B") as a documented-but-not-implemented future option. Being connected to the VPN makes `10.44.13.1` *reachable*, but it doesn't change what a normal DNS lookup for `argocd.vpn.bryan-javier-llm-local.cc` returns — that's still the public A record, `169.58.149.18`. So the browser was connecting to the public IP the whole time, landing on Caddy's harmless-but-useless empty `200` response (see Phase 3 Step 2's Gotcha section) instead of ever reaching `10.44.13.1`.

**Fix: implemented Option B.** Installed `dnsmasq` bound only to the WireGuard interface IP, resolving the `.vpn` hostnames to the tunnel address and forwarding everything else to a real upstream resolver (so normal browsing still works while connected):

```bash
sudo apt-get install -y dnsmasq
sudo tee /etc/dnsmasq.conf > /dev/null <<'CONF'
listen-address=10.44.13.1
bind-interfaces
no-resolv
no-hosts
address=/argocd.vpn.bryan-javier-llm-local.cc/10.44.13.1
address=/studio.vpn.bryan-javier-llm-local.cc/10.44.13.1
server=1.1.1.1
server=1.0.0.1
cache-size=1000
CONF
sudo systemctl enable --now dnsmasq
```

Firewalled to the WireGuard subnet only, same pattern as SSH/6443:
```bash
sudo ufw allow from 10.44.13.0/24 to any port 53 proto udp comment 'split DNS - WireGuard only'
sudo ufw allow from 10.44.13.0/24 to any port 53 proto tcp comment 'split DNS - WireGuard only'
```

**The other half of the fix — client configs.** A resolver on the server does nothing until WireGuard clients are actually told to use it. Each client `.conf` file has its own `DNS =` line (previously `1.1.1.1`, a generic public resolver with no idea about the `.vpn` hostnames). Updated all 5 existing peer configs and the `add-wg-peer.sh` script's default for future peers:
```bash
sudo sed -i 's/DNS_FOR_CLIENT="1.1.1.1"/DNS_FOR_CLIENT="10.44.13.1"/' /usr/local/sbin/add-wg-peer.sh
sudo sed -i 's/DNS = 1.1.1.1/DNS = 10.44.13.1/' /etc/wireguard/peers/<name>/<name>.conf   # for each existing peer
```
Re-delivered all 5 updated `.conf` files to the user — re-importing is required on any device that already has the old config active, since WireGuard doesn't hot-reload a running tunnel's DNS setting from a file that changed on disk elsewhere.

Verified before declaring it fixed:
```bash
$ dig @10.44.13.1 argocd.vpn.bryan-javier-llm-local.cc +short
10.44.13.1
$ dig @10.44.13.1 example.com +short
172.66.147.243
104.20.23.154
```
VPN hostname resolves to the tunnel IP; normal domains still forward correctly through the real upstream.

---

## Phase 3 status: complete

All Phase 3 exit criteria met and **empirically verified, not just configured**: a non-secret git commit (the `keepass4web` namespace manifest) reconciled into the cluster automatically via Argo CD, and a secret is genuinely decrypted only at runtime — confirmed by reading back the real plaintext value from the live cluster Secret, not just checking sync status.

**What exists now:**
- 4 GitHub repos: `platform-bootstrap`, `platform-secrets`, `keepass4web-deployments`, `keepass4web-supabase` (the last two renamed from their original `platform-*` names)
- Argo CD v2.13.2, UI reachable only via WireGuard at `argocd.vpn.bryan-javier-llm-local.cc`
- App-of-apps pattern: one root `Application` watching `platform-bootstrap/apps/`, which currently contains 2 child Applications (`keepass4web`, `platform-secrets`)
- SOPS + age fully wired for automatic in-cluster decryption via a ksops sidecar on `argocd-repo-server`

**Known follow-ups carried forward:**
- No branch protection on any of the 4 repos (GitHub Pro required for private repos on this account) — acceptable for solo work, revisit if a collaborator joins.
- The `keepass4web` Application currently only has a placeholder `Namespace` manifest — real Deployments/Services come in Phase 5.
- Argo CD's initial admin password hasn't been rotated yet — do this before Phase 4/5 (`kubectl -n argocd get secret argocd-initial-admin-secret ...`, then delete that Secret per Argo CD's own recommendation).
- The example encrypted secret (`platform-secrets/secrets/example-secret.yaml`) is a placeholder proving the pipeline works — remove it once real secrets (Supabase credentials, Phase 4) replace it, or leave it as a template to copy from.
