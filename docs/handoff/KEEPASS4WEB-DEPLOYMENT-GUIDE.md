# KeePass4Web Deployment Guide — Phase 5

Running log of every step performed for Phase 5 of [VPS-INTEGRATION-PLAN.md](../../VPS-INTEGRATION-PLAN.md) — building and deploying the Go and Rust services through Argo CD. Same format as the other guides: real commands, real output, reproducible manually.

This phase surfaced **seven distinct real bugs**, all found and fixed by actually running the thing, not by inspection alone. Each is documented in full below because the failure symptoms were often generic/misleading (silent exits, identical-looking crash loops) and the fixes are non-obvious.

## Step 1 — Write the Rust Dockerfile

No Dockerfile existed for `backend-rust` (Go already had one at `backend-go/cmd/web/Dockerfile`). Written matching the Go Dockerfile's conventions: multi-stage build, non-root user `65532:65532`, from `rust:1.97.1-slim-bookworm` (matches the pinned `rust-toolchain.toml`) to a `debian:bookworm-slim` runtime with `ca-certificates` (needed for outbound TLS — LDAP/OIDC/reqwest all use `rustls`).

### Gotcha #1 — missing test fixture (`ENOENT`)

First attempt ran `cargo test --release --locked` as part of the build (matching Go's pattern). 6 tests failed with `Os { code: 2, kind: NotFound }`. Root cause: `keepass::keepass::tests::database_roundtrip` does `fs::read("tests/test.kdbx").await.unwrap()` — that fixture lives at the **repo root** (`tests/test.kdbx`), not under `backend-rust/`, and the Dockerfile only copied `Cargo.toml`, `Cargo.lock`, and `backend-rust/`. Fixed by adding `COPY tests ./tests` to the build stage only — never copied into the runtime image (this is literally the file the plan warns not to deploy as production data).

### Gotcha #2 — kernel session keyring isn't available in a `docker build` sandbox

After fixing the fixture, one test still failed: `keepass::key::tests::key_roundtrip`, with `Unknown(1)` (EPERM). This test calls `SecretKey::store()`/`retrieve()`, which use the real Linux kernel **session keyring** (`KeyRingIdentifier::Session`, `keyctl`/`add_key`/`request_key` syscalls) — the exact reason `backend-rust/seccomp/keyring.json` exists (Docker's default seccomp profile blocks those syscalls).

Tried skipping just that one test (`cargo test -- --skip key_roundtrip`) — a **different** test failed on the next build (`database_roundtrip`, same `Unknown(1)` signature), because `to_enc()`/`from_enc()` in `keepass.rs` and `with_vault`/`with_vault_mut` in `private_service.rs` all transitively touch the same kernel keyring. This isn't a short list of isolated tests — it's core to how the service works. Skip-listing individual test names doesn't scale and would gut most of the meaningful coverage anyway.

**Decision: `cargo test` does not run as part of this image build at all**, unlike Go's Dockerfile. An ephemeral `docker build` RUN step doesn't reliably provide a real session keyring context regardless of thread count or skip lists — that needs a CI environment with the right session/capability setup (Phase 6 territory). Run `cargo test` locally/in a real dev environment before releasing; the Dockerfile's own comment block documents this reasoning in full so it isn't rediscovered from scratch later.

## Step 2 — Build and push images to GHCR

```bash
gh auth token | docker login ghcr.io -u BryJavier --password-stdin
docker build -f backend-go/cmd/web/Dockerfile -t ghcr.io/bryjavier/keepass4web-web:<short-sha> .
docker build -f backend-rust/Dockerfile -t ghcr.io/bryjavier/keepass4web-rust:<short-sha> .
docker push ghcr.io/bryjavier/keepass4web-web:<short-sha>
docker push ghcr.io/bryjavier/keepass4web-rust:<short-sha>
```
Built natively on the VPS (linux/amd64), not cross-compiled from the local Mac (arm64) — avoids cross-arch build complexity entirely. Tagged with the **short git commit SHA** (first 12 chars) the source was built from, per the plan's "immutable image references" requirement — never `latest`.

**Known gap, flagged not hidden:** image pushes authenticate with the operator's own broad-scope personal `gh` token (via `gh auth token`), not a dedicated scoped token. The original prerequisite decision was a fine-grained PAT for CI, which doesn't exist yet since Phase 6 (GitHub Actions) hasn't been built. Revisit when wiring up CI.

## Step 3 — GHCR pull secret and seccomp profile

Pull secret generated with `kubectl create secret docker-registry ... --dry-run=client -o yaml`, then **encrypted with SOPS and committed to `platform-secrets`** rather than left as an imperative `kubectl create` — otherwise a cluster rebuild would silently lose it, breaking the whole GitOps philosophy. Added to `secrets/secret-generator.yaml`'s `files:` list alongside the Phase 3/4 secrets.

Seccomp profile (`backend-rust/seccomp/keyring.json`) copied to the node's kubelet seccomp root:
```bash
sudo mkdir -p /var/lib/kubelet/seccomp/profiles
sudo cp keyring.json /var/lib/kubelet/seccomp/profiles/keepass4web-rust-keyring.json
```
Single-node cluster, so this manual placement is sufficient — a multi-node cluster would need this on every node (a DaemonSet, typically).

## Step 4 — Deployments, Services, Ingress, NetworkPolicies

Written to `keepass4web-deployments/keepass4web/`:
- `go-deployment.yaml` — public, `httpGet /healthz` probes, `readOnlyRootFilesystem: true` + an `emptyDir` at `/tmp` (standard pairing — the Go runtime may need scratch space even though the app itself doesn't obviously write to disk).
- `rust-deployment.yaml` — private, `tcpSocket` probes (**no HTTP health endpoint exists** — checked `private_service.rs` and `server/route.rs` directly rather than assuming one existed; only `/internal/v1/*` API routes are registered), custom `seccompProfile.type: Localhost` referencing the profile from Step 3.
- `ingress.yaml` — routes `app.<domain>` to the Go Service only. **No Ingress resource exists for Rust at all.**
- `networkpolicies.yaml` — implements the plan's Phase 5 step 6 literally: ingress-nginx → Go, Go → Rust, Go → Supabase (egress `ipBlock` to the VPS's real IP on port 8000), plus mandatory DNS egress (any egress-restricted pod needs this explicitly or name resolution breaks). Rust's policy only restricts `Ingress` (from Go's pods only) — belt-and-suspenders even though no Ingress resource routes to it anyway.
- `limitrange.yaml` — the same defaults from Phase 2's `default` namespace, applied here since this is the first namespace with real workloads (a Phase 2 follow-up, closed out now).

## Step 5 — Deploy and debug (the real work)

Triggered sync, then spent most of this phase diagnosing why pods wouldn't come up. Documenting every failure precisely because the symptoms were misleading — mostly **silent exits with no useful log output**, which is bad ergonomics worth knowing about if this happens again.

### Gotcha #3 — Kong bound to `127.0.0.1`, unreachable from pods

`SUPABASE_CONTAINER_URL` was originally set to `http://169.58.149.18:8000` (the VPS's IP), but Kong's HTTP port was bound to `127.0.0.1` only (Phase 4 hardening). **A Kubernetes pod's `127.0.0.1` is the pod's own loopback, not the host's** — this was already true before Phase 5, it just had no consumer to expose the problem until now.

Verified empirically before deciding on a fix (see "pod-to-host reachability" test): a test pod could reach the VPS's real IP on port 80 (Caddy, real `308` redirect) but got `connection refused` (not timeout) on port 8000 (Kong bound elsewhere, not blocked) — confirming pods route to the host's real IP fine, just not `127.0.0.1`.

**Fix:** rebound Kong's HTTP port (only — HTTPS stays `127.0.0.1`-only, no pod consumer) to the VPS's real IP in `keepass4web-supabase/docker-compose.yml`, and added a UFW rule scoping it to the pod CIDR only (`192.168.0.0/16`), same trusted-CIDR pattern as SSH/6443/DNS:
```bash
sudo ufw allow from 192.168.0.0/16 to any port 8000 proto tcp comment 'Kong - pod network only, KeePass4Web Go service'
```

### Gotcha #4 — Go's own `validPublicURL()` security check rejects a raw IP

With Kong reachable, the Go pod still exited silently (exit code 1, **zero log output** — not even a startup_failed event). Ran the container directly with real env vars (`docker run --env-file ...`) to bypass any Kubernetes log-capture nuance — still nothing. Traced the code path directly: `supabase.NewVaultClient()`/`NewAuthClient()` call `validPublicURL()`, which **deliberately** only allows plain HTTP for `localhost`, `host.docker.internal`, or loopback IPs (a real security control against sending Supabase credentials over unencrypted HTTP to an arbitrary host) — and the error path that returns from these constructors **isn't logged** in `runWithLogger`, explaining the total silence.

**Fix, respecting the existing check rather than bypassing it:** the code already explicitly whitelists `host.docker.internal` for exactly this "Supabase reachable via the container/pod bridge" scenario. Added `hostAliases` to the Go Deployment mapping that name to the VPS's real IP, and changed `SUPABASE_CONTAINER_URL` to `http://host.docker.internal:8000`:
```yaml
hostAliases:
  - ip: "169.58.149.18"
    hostnames:
      - "host.docker.internal"
```

### Gotcha #5 — Rust's own container-bind safety check

Rust exited with code **0** ("Completed", not a crash) and a `startup_failure` log — a deliberate refusal, not an error. `private_service.rs`: `if !bind.ip().is_loopback() && !allow_container_bind { refuse }`. `RUST_PRIVATE_LISTEN` binds `0.0.0.0` (required — a Kubernetes Service can't route to a pod's loopback-only listener), so `PRIVATE_SERVICE_ALLOW_CONTAINER_BIND` must be `"true"`. It had been left as an unexamined `"false"` placeholder from Phase 3's initial ConfigMap. One-line fix, but easy to misdiagnose as a real crash since it isn't one.

### Gotcha #6 — `config.yml` missing from the runtime image entirely

Fixing Gotcha #5 didn't fix the crash loop. `main.rs` calls `Config::from_file("config.yml")` (default path, relative to the working directory) — **never copied into the Docker runtime stage**, only relevant if it happened to exist at build time (it didn't get copied there either, only used implicitly by nothing). The failure was, again, completely silent — `observability::emit()` is a deliberately minimal "safe" logger (`println!`+`eprintln!` of a generic `startup_failure` event, by design, to avoid leaking sensitive detail) with no way to get more detail even via `RUST_LOG=debug` (it doesn't route through `log`/`env_logger` at all).

Diagnosed by reading `main.rs`'s two possible failure branches (`Config::from_file` vs `Server::new`) and tracing what each could fail on, since log output gave no clues. **Fix:**
```dockerfile
COPY backend-rust/config.yml /app/config.yml
```
Also confirmed (before assuming it was safe to leave the file's defaults as-is) that `db_backend`/`auth_backend` settings in `config.yml` are **not referenced anywhere in `private_service.rs`** — they only matter for a separate legacy standalone Rust UI on port 8081 that this deployment doesn't expose or use. `db_backend: 'Filesystem'` validation only checks the configured path string is non-empty, not that a file actually exists, so it passes trivially either way.

### Gotcha #7 — malformed `RUST_PRIVATE_LISTEN` value

Still crash-looping after Gotcha #6. Traced `config.rs`'s `apply_private_service_environment()`: `listen.parse::<SocketAddr>()` — and `":9090"` (no host) **is not a valid `SocketAddr`** in Rust; it needs `"0.0.0.0:9090"`. This parse failure returns an `Err` that propagates straight out of `Config::from_file`, producing the exact same generic `startup_failure` output as every other failure mode in this phase — impossible to distinguish from logs alone.

```yaml
RUST_PRIVATE_LISTEN: "0.0.0.0:9090"   # was ":9090" — not a valid SocketAddr
```

After this fix, both pods came up `1/1 Running` and stayed there.

## Step 6 — Verify end-to-end (not just "pods are Running")

`Running` and `Ready` on their own don't prove the actual architecture works — verified each specific claim:

```
$ curl -s https://app.bryan-javier-llm-local.cc/healthz
ok                                                              # real public HTTPS path works

$ kubectl get ingress -n keepass4web
keepass4web-go   nginx   app.bryan-javier-llm-local.cc   ...    # only Go has one — Rust has none at all

$ kubectl exec deploy/keepass4web-go -n keepass4web -- wget -O- http://keepass4web-rust...:9090/
HTTP/1.1 404 Not Found                                          # real response — Go reaches Rust over TCP+HTTP

$ kubectl run netcheck-blocked ...; wget http://keepass4web-rust...:9090/
wget: download timed out                                        # an UNRELATED pod is genuinely blocked by NetworkPolicy

$ sudo ss -tlnp | grep -E ':5432|:3001|:8443'
127.0.0.1:8443 ...   127.0.0.1:3001 ...                         # Postgres has no line at all; Studio still localhost-only
```

All Phase 5 exit criteria met and **functionally**, not just structurally, verified: the public application is healthy, and Rust/Postgres/Studio have no public route — including confirming NetworkPolicy actually blocks traffic it's supposed to, not just that the YAML exists.

**Known follow-ups carried forward:**
- GHCR push authentication uses a broad personal token, not a scoped one — revisit in Phase 6.
- The `readOnlyRootFilesystem`/`emptyDir /tmp` pairing on both Deployments is a defensive default, not verified against an actual observed disk-write need — fine as-is, just noting it wasn't empirically required.
- No liveness proof yet that the *application logic* (vault unlock, KeePass operations) works end-to-end through Supabase Storage — this phase verified infrastructure connectivity (Go ↔ Rust ↔ Supabase reachability), not a real user login/vault-open flow. Worth a manual UAT pass before considering this production-ready.

## Quick command cheat sheet

### Building and pushing images
```bash
gh auth token | ssh <vps> "docker login ghcr.io -u BryJavier --password-stdin"
ssh <vps> "cd /tmp/keepass4web-build && git pull && \
  docker build -f backend-go/cmd/web/Dockerfile -t ghcr.io/bryjavier/keepass4web-web:<sha> . && \
  docker push ghcr.io/bryjavier/keepass4web-web:<sha> && \
  docker build -f backend-rust/Dockerfile -t ghcr.io/bryjavier/keepass4web-rust:<sha> . && \
  docker push ghcr.io/bryjavier/keepass4web-rust:<sha>"
```

### Bumping the deployed image tag
```bash
sed -i '' 's/<old-sha>/<new-sha>/' keepass4web-deployments/keepass4web/go-deployment.yaml keepass4web-deployments/keepass4web/rust-deployment.yaml
# commit, push, then:
kubectl patch application keepass4web -n argocd --type merge -p '{"metadata":{"annotations":{"argocd.argoproj.io/refresh":"hard"}}}'
```

### When only a referenced ConfigMap/Secret changes (not the Deployment spec itself)
Argo CD syncs the changed object, but **won't automatically roll the pods** that reference it — the Deployment's pod template hash doesn't change just because a ConfigMap it points to did:
```bash
kubectl rollout restart deployment/keepass4web-go -n keepass4web
kubectl rollout restart deployment/keepass4web-rust -n keepass4web
```

### Diagnosing a silently-exiting pod (this phase's most common problem)
```bash
kubectl get pods -n keepass4web -o wide
kubectl logs <pod> -n keepass4web                                    # often unhelpful by design (safe/minimal logging)
kubectl logs <pod> -n keepass4web --previous                         # last crashed instance
kubectl get pod <pod> -n keepass4web -o jsonpath='{.status.containerStatuses[0].lastState.terminated}'  # real exit code/reason

# Run the exact image with the exact real env vars directly — bypasses Kubernetes'
# log capture entirely and is the most reliable way to see what's actually happening:
kubectl get configmap keepass4web-config -n keepass4web -o go-template='{{range $k,$v := .data}}{{$k}}={{$v}}{{"\n"}}{{end}}' > /tmp/all.env
kubectl get secret keepass4web-secrets -n keepass4web -o json | python3 -c "
import json,sys,base64
d = json.load(sys.stdin)
with open('/tmp/all.env','a') as f:
    for k,v in d['data'].items():
        f.write(f'{k}={base64.b64decode(v).decode()}\n')
"
docker run --rm --env-file /tmp/all.env ghcr.io/bryjavier/keepass4web-web:<sha>
rm -f /tmp/all.env
```

### Testing pod-to-host and pod-to-pod connectivity directly
```bash
kubectl run nettest --image=busybox:1.36 --restart=Never --command -- sleep 300
kubectl wait --for=condition=Ready pod/nettest --timeout=30s
kubectl exec nettest -- wget -q -O- --timeout=5 http://<host-ip>:<port> -S    # pod -> host
kubectl exec nettest -n keepass4web -- wget -q -O- --timeout=3 http://keepass4web-rust.keepass4web.svc.cluster.local:9090/  # pod -> pod, respects NetworkPolicy
kubectl delete pod nettest --wait=false
```

### Seccomp profile (Rust only)
```bash
ls /var/lib/kubelet/seccomp/profiles/                                # confirm placed
kubectl get pod -n keepass4web -l app=keepass4web-rust -o jsonpath='{.items[0].spec.securityContext.seccompProfile}'
```

### Application health and status
```bash
kubectl get application keepass4web -n argocd
kubectl get pods,svc,ingress,networkpolicy -n keepass4web
curl -s https://app.bryan-javier-llm-local.cc/healthz
```
