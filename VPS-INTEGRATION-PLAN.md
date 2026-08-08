# VPS Integration Plan

> **Status:** Planning only. This document does not authorize or perform any VPS, DNS, GitHub, Kubernetes, Supabase, or secret configuration.

## Goal

Deploy the KeePass4Web application to a single Ubuntu VPS using a generic Kubernetes cluster named `platform-cluster` and Argo CD GitOps. Run self-hosted Supabase on the *same VPS* as a separate Docker Compose stack, not as Kubernetes workloads. Secure administrative access to Supabase Studio and Argo CD through WireGuard only.

## Scope and operating model

The target VPS has 6 vCPU and 12 GB RAM. This is appropriate for a low-traffic, production-style single-node installation, but it is not highly available. A VPS outage affects Kubernetes, the application, and Supabase at once. Off-VPS backups and restore testing are mandatory.

The chosen boundaries are:

| Component | Management plane | Runtime |
|---|---|---|
| KeePass4Web public Go service | Argo CD | Kubernetes |
| KeePass4Web private Rust service | Argo CD | Kubernetes |
| Kubernetes platform services | Argo CD after bootstrap | Kubernetes |
| Supabase | Host-level GitHub-driven deployment | Docker Compose on the VPS |
| Supabase Studio | WireGuard + reverse proxy | Docker Compose via Kong |
| Application database migrations | Argo CD pre-sync gate | Kubernetes Job against Supabase |

## Target architecture

```text
                                Public Internet
                                      |
                       +--------------+--------------+
                       |                             |
            app.example.com                 supabase.example.com
                       |                             |
                       +-------- Host reverse proxy -+
                                      |
                +---------------------+----------------------+
                |                                            |
      Kubernetes entry point                         Supabase Kong :8000
                |                                            |
      +---------v----------+                    Docker Compose on same VPS
      | platform-cluster   |                    - Postgres and Supavisor
      |                   |                    - Auth, REST, Storage, Realtime
      |  Argo CD           |                    - Studio (not publicly routed)
      |  KeePass4Web Go ---+-------------------> Supabase API
      |  KeePass4Web Rust  |                    - persistent volumes
      +--------------------+

                              WireGuard VPN only
                                      |
                         studio.vpn.example.com
                         argocd.vpn.example.com
```

Only the KeePass4Web Go service is public. The Rust service has no public route. PostgreSQL, Supavisor, and Supabase internal services are never public.

## Repository layout and responsibilities

```text
BryJavier/keepass4web-rs-custom   Application source, SQL migrations, tests, image builds
BryJavier/platform-deployments    Kubernetes manifests and immutable image references
BryJavier/platform-bootstrap      Argo CD root/apps and generic platform components
BryJavier/platform-secrets        SOPS-encrypted secrets only
BryJavier/platform-supabase       Pinned Supabase Compose definition and host deployment automation
```

The source repository builds artifacts but does not receive Kubernetes credentials. `platform-deployments` is the change-controlled source of application desired state. `platform-bootstrap` lets the cluster grow to host future applications. `platform-secrets` must contain encrypted values only; raw secrets are never committed.

## Networking and access controls

### Public routes

- `https://app.<domain>` routes to the Kubernetes KeePass4Web Go service.
- `https://supabase.<domain>` routes only approved Supabase API routes through Kong: Auth, REST, Storage, Realtime, and Functions if enabled.
- The public reverse proxy rejects Studio/dashboard routes.

### VPN-only routes

- `https://studio.vpn.<domain>` is available only through WireGuard and reaches Supabase Studio through Kong.
- `https://argocd.vpn.<domain>` is available only through WireGuard and reaches Argo CD.
- Studio requires both a valid WireGuard peer and the Supabase dashboard username/password.

### Firewall policy

- Allow `80/tcp` and `443/tcp` publicly only for the reverse proxy.
- Allow WireGuard on `51820/udp` (or a chosen alternate port).
- Restrict SSH to the WireGuard subnet after VPN validation, with an optional temporary trusted-IP recovery rule.
- Never expose `5432/tcp`, `6543/tcp`, Kubernetes API `6443/tcp`, or Supabase internal service ports publicly.

### WireGuard policy

- Use a dedicated `wg0` interface and an isolated subnet, proposed as `10.77.0.0/24`.
- Create an individual key pair and fixed tunnel IP for every device; never share a peer configuration.
- Bind the Studio and Argo CD reverse-proxy listeners to the WireGuard interface only.
- Use split DNS so `studio.vpn.<domain>` and `argocd.vpn.<domain>` resolve to the WireGuard server IP for VPN clients.
- Use DNS-01 certificate validation for VPN-only HTTPS hostnames, or a private CA if public DNS control is unavailable.

## Phased integration plan

### Phase 0 — Finalize architecture and recovery requirements

1. Confirm Ubuntu LTS release, VPS provider, static IP, disk capacity, and backup capabilities.
2. Confirm generic cluster name `platform-cluster`.
3. Confirm public and VPN-only hostnames.
4. Choose external S3-compatible storage for Supabase Storage and backups, or explicitly accept local-storage limitations.
5. Define RPO, RTO, maintenance windows, and migration approval policy.

**Exit criteria:** All values in the prerequisite checklist are approved and no production secret has been shared in plaintext.

### Phase 1 — Harden the VPS

1. Create a non-root administrative user with SSH keys.
2. Disable root/password SSH access and enable OS security updates.
3. Apply the firewall policy and establish WireGuard before exposing administrative interfaces.
4. Install host-level reverse proxy, Docker Engine/Compose for Supabase, and the Kubernetes runtime according to the selected compatibility design.
5. Reserve CPU, memory, disk, and log limits for Docker Compose and Kubernetes so neither can starve the host.

**Exit criteria:** The VPS is patched, reachable by VPN, and administrative services are not public.

### Phase 2 — Install Kubernetes platform components

1. Install kubeadm, kubelet, and the chosen compatible container runtime integration.
2. Initialize the single-node `platform-cluster` and install Calico networking.
3. Install persistent volume support for single-node workloads and enforce resource requests/limits.
4. Configure a Kubernetes entry point behind the host reverse proxy.
5. Keep the Kubernetes API private; use the VPN for administration.

**Exit criteria:** Cluster node is `Ready`, DNS/networking work, and only intended high ports are reachable from the host reverse proxy.

### Phase 3 — Establish GitOps and encrypted secrets

1. Create the four platform repositories in GitHub with protected default branches and reviewed pull requests.
2. Install a pinned Argo CD release manually once.
3. Apply one root Application that watches `platform-bootstrap`; after this, manage all Kubernetes configuration through Git.
4. Configure SOPS + age for `platform-secrets`; keep the private age key as a restricted cluster secret and in encrypted offline recovery storage.
5. Create Argo CD Applications for platform components, KeePass4Web, encrypted secrets, and future applications.

**Exit criteria:** A non-secret Git commit can reconcile into the cluster, and secrets are decrypted only at runtime.

### Phase 4 — Deploy and secure host-level Supabase

1. Maintain the pinned Docker Compose configuration in `platform-supabase`.
2. Deploy Supabase as a system-managed Compose stack on the VPS, with a dedicated service account and persistent host volumes.
3. Generate Supabase keys/passwords outside Git; store encrypted source material in `platform-secrets` and deploy host values with restrictive filesystem permissions.
4. Route public Supabase API traffic through the host reverse proxy to Kong, but make Studio VPN-only.
5. Keep Postgres and Supavisor private; enable only the narrowly required internal migration path.
6. Configure Supabase Storage to use external object storage where possible.
7. Schedule encrypted Postgres and storage backups off-VPS; test restore before onboarding real data.

**Exit criteria:** Supabase is healthy, API access is HTTPS-only, Studio is VPN-only, and a restore test succeeds.

### Phase 5 — Deploy KeePass4Web through Argo CD

1. Publish separate immutable Go and Rust images to GHCR:
   - `ghcr.io/bryjavier/keepass4web-web:<commit-sha>`
   - `ghcr.io/bryjavier/keepass4web-rust:<commit-sha>`
2. Define separate Kubernetes Deployments and ClusterIP Services for Go and Rust.
3. Route only the Go Service from `app.<domain>`.
4. Configure Go with browser-facing `https://supabase.<domain>` and an approved server-side Supabase endpoint.
5. Configure Go-to-Rust communication with the internal `keepass4web-rust` Service and a shared secret token.
6. Add NetworkPolicies allowing only necessary traffic: ingress to Go, Go to Rust, and Go to Supabase.
7. Decide and document the production KeePass database storage model; never deploy the repository test KDBX file as production data.

**Exit criteria:** The public application is healthy; Rust, Postgres, and Studio have no public route.

### Phase 6 — CI/CD and database migrations

1. Source repository pull requests run application tests, start ephemeral Supabase, replay all migrations, and run database/RLS tests.
2. The source workflow builds Go, Rust, and a dedicated migration-runner image using the same source commit SHA.
3. On source merge, GitHub Actions pushes images to GHCR and opens a pull request in `platform-deployments` that updates all three immutable tags together.
4. A reviewed deployment-repository merge triggers Argo CD through a GitHub webhook; polling is a fallback.
5. Argo CD executes the following ordered gates:
   - Supabase health confirmation
   - Postgres backup job
   - KeePass4Web migration pre-sync Job
   - KeePass4Web Go/Rust rollout
   - post-deployment smoke test
6. The migration Job uses a restricted migration database credential, reads the versioned `supabase/migrations` set, and applies only pending migrations.
7. A failed migration prevents application rollout. Application rollback does not automatically reverse schema changes.

**Exit criteria:** A reviewed source change produces a reviewed deployment PR; merging it safely migrates and deploys the application without direct Kubernetes access from source CI.

### Phase 7 — Operations and recovery

1. Monitor VPS CPU/RAM/disk, Kubernetes Pod health, Docker Compose services, TLS expiry, Argo CD sync state, and Postgres capacity.
2. Define alert routing and an on-call contact path.
3. Practice restoration of Postgres, Supabase Storage, Kubernetes etcd, and any KeePass persistent data.
4. Maintain written runbooks for a failed deployment, failed migration, expired certificate, lost WireGuard client, and total VPS rebuild.
5. Patch Kubernetes, host packages, and Supabase only through scheduled, tested upgrades with backups.

**Exit criteria:** Restore and rollback drills are documented and successfully tested.

## Database migration rules

- Migrations are committed as new, forward-only files; never edit a migration that has run in production.
- CI replays the full migration history against an ephemeral Supabase instance.
- Production never runs `supabase db reset` or seed data.
- Use expand/contract changes: add compatible schema first, deploy compatible code, then remove obsolete fields in a later release.
- Destructive migrations require explicit approval, a maintenance window, and a verified backup.
- The migration credential is separate from application credentials and is not available to browser code.

## Prerequisite checklist

Provide non-sensitive values directly. For secrets, provide the storage/ownership decision rather than plaintext values.

### VPS and access

- [ ] VPS provider, region, Ubuntu version, public IPv4, disk size/type, and snapshot capability.
- [ ] SSH username, public key, SSH port, and emergency trusted administration IP/CIDR.
- [ ] Backup destination, encryption policy, retention, RPO, and RTO.
- [ ] Expected user count, vault/storage size, and future applications planned for this VPS.

### Domains, DNS, and TLS

- [ ] Primary domain and DNS provider.
- [ ] Public hostnames for `app` and `supabase`.
- [ ] VPN-only hostnames for `studio.vpn` and `argocd.vpn`.
- [ ] DNS API capability/token storage plan for DNS-01 certificate validation.
- [ ] Let’s Encrypt notification email.

### WireGuard and administrative access

- [ ] Confirm WireGuard subnet or accept `10.77.0.0/24`.
- [ ] Confirm WireGuard UDP port or accept `51820`.
- [ ] Initial administrator devices/users requiring individual VPN peers.
- [ ] Confirm SSH, Argo CD, and Supabase Studio will be VPN-only after validation.
- [ ] Decide whether split DNS is supplied by the DNS provider, router, or a small VPN DNS resolver.

### GitHub and CI/CD

- [ ] Confirm GitHub owner `BryJavier` and final repository names/visibility.
- [ ] Confirm source branch `master` and desired default branches for new repositories.
- [ ] Choose a GitHub App (preferred) or a narrowly scoped fine-grained token for source-to-deployment PRs.
- [ ] Choose GHCR image visibility; private images require Kubernetes image-pull credentials.
- [ ] Identify required reviewers and branch-protection rules.

### Supabase

- [ ] Confirm Docker Compose on the same VPS, outside Kubernetes.
- [ ] Decide whether optional Supabase services (Realtime, Storage, Functions, analytics) are initially enabled.
- [ ] Select object storage provider/bucket or accept local Supabase Storage with its backup implications.
- [ ] Required auth methods: email/password, magic links, OAuth providers, MFA, and SMTP provider.
- [ ] Supabase public URL, site URL, and auth redirect URLs.
- [ ] Studio username; password is supplied later through encrypted secret handling.
- [ ] Decide whether the migration Job reaches Supabase through a private host endpoint or a carefully scoped host-network path.

### KeePass4Web

- [ ] Production KeePass data storage choice: PVC-backed filesystem, HTTP backend, or another supported backend.
- [ ] Retention and backup requirements for vault data and attachments.
- [ ] Application environment names and desired resource limits.
- [ ] Confirm Go is public and Rust remains private-only.

### Database migration governance

- [ ] Confirm Argo CD PreSync migration Job model.
- [ ] Confirm migration approval/maintenance-window policy for destructive schema changes.
- [ ] Confirm mandatory local migration replay and RLS tests in source CI.
- [ ] Confirm production seed data is prohibited.

## Explicit non-goals

- This plan does not provide high availability; one VPS remains one failure domain.
- This plan does not expose Supabase Studio, Postgres, Rust, Argo CD, or Kubernetes API publicly.
- This plan does not store plain secrets in Git.
- This plan does not configure any service until prerequisite values are approved.
