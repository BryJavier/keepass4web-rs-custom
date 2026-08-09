# Kubernetes Setup Guide — Phase 2 (platform-cluster)

Running log of every step performed to bootstrap the single-node `platform-cluster` per [VPS-INTEGRATION-PLAN.md](../../VPS-INTEGRATION-PLAN.md) Phase 2, on top of the hardened VPS from [VPS-HARDENING-GUIDE.md](VPS-HARDENING-GUIDE.md). Same format: real commands, real output, reproducible manually.

**Prerequisites already in place (Phase 1):** containerd (systemd cgroup driver), kubeadm/kubelet/kubectl v1.31.14 (version-pinned), UFW active, WireGuard up.

## Step 1 — `kubeadm init`

Missing dependency caught by preflight checks first:
```
error execution phase preflight: [preflight] Some fatal errors occurred:
	[ERROR FileExisting-conntrack]: conntrack not found in system path
```
Fixed:
```bash
sudo apt-get install -y conntrack
```

Then the actual init, using Calico's default pod CIDR so the two don't need reconciling later, and advertising on the VPS's only real interface (`eth0`, `169.58.149.18` — this is a single-NIC VPS, no private network add-on in use):
```bash
sudo kubeadm init \
  --pod-network-cidr=192.168.0.0/16 \
  --apiserver-advertise-address=169.58.149.18 \
  --control-plane-endpoint=169.58.149.18 \
  --node-name=vmi3496773 \
  --kubernetes-version=v1.31.14
```
Succeeded — control plane initialized, CoreDNS and kube-proxy addons applied automatically. Saved output includes the `kubeadm join` command with a bootstrap token (not reproduced here since it's time-limited and single-use; regenerate with `kubeadm token create --print-join-command` if a second node is ever added).

```bash
mkdir -p $HOME/.kube
sudo cp -i /etc/kubernetes/admin.conf $HOME/.kube/config
sudo chown $(id -u):$(id -g) $HOME/.kube/config
```

## Step 2 — Calico CNI via tigera-operator

Used the operator-based install (not the older manifest-only install) since it's what upstream Calico currently recommends and handles upgrades better:
```bash
kubectl create -f https://raw.githubusercontent.com/projectcalico/calico/v3.29.1/manifests/tigera-operator.yaml
curl -fsSL -o /tmp/calico-custom-resources.yaml https://raw.githubusercontent.com/projectcalico/calico/v3.29.1/manifests/custom-resources.yaml
```
Checked the downloaded manifest before applying — its default `cidr: 192.168.0.0/16` already matched what was passed to `kubeadm init`, so applied as-is with no edits:
```bash
kubectl apply -f /tmp/calico-custom-resources.yaml
```

Rollout took a few minutes (image pulls). Final state:
```
$ kubectl get tigerastatus
NAME        AVAILABLE   PROGRESSING   DEGRADED   SINCE
apiserver   True        False         False      36s
calico      True        False         False      41s
ippools     True        False         False      3m41s
```
All Calico pods `Running` in `calico-system` and `calico-apiserver` namespaces.

## Step 3 — Remove the control-plane taint (single-node requirement)

By default `kubeadm init` taints the control-plane node `NoSchedule`, which makes sense for multi-node clusters (keep workloads off the control plane) but blocks everything on a single-node cluster like this one:
```bash
kubectl taint nodes vmi3496773 node-role.kubernetes.io/control-plane:NoSchedule-
```

## Step 4 — Verify node readiness, DNS, and networking

```
$ kubectl get nodes
NAME         STATUS   ROLES           AGE   VERSION
vmi3496773   Ready    control-plane   ...   v1.31.14
```

Ran a disposable `busybox` pod to check DNS and connectivity:
```bash
kubectl run netcheck --image=busybox:1.36 --restart=Never --command -- sleep 600
kubectl wait --for=condition=Ready pod/netcheck --timeout=60s
kubectl exec netcheck -- nslookup kubernetes.default.svc.cluster.local
```
```
Server:		10.96.0.10
Name:	kubernetes.default.svc.cluster.local
Address: 10.96.0.1
```
FQDN resolution confirms CoreDNS + service networking are both correct. (A short-name lookup, `nslookup kubernetes.default`, returned `NXDOMAIN` — that's a known BusyBox `nslookup` limitation, it doesn't expand `ndots`/search-domain suffixes like most resolvers do; not a cluster issue, confirmed by the FQDN success.)

### Gotcha: pod-to-internet egress blocked by UFW's default forward policy

Symptom: DNS worked, pod-to-pod worked, but `wget` from inside a pod to the public internet timed out.

Root cause: `/etc/default/ufw` ships with `DEFAULT_FORWARD_POLICY="DROP"`. Calico manages its own detailed traffic policy inside the `cali-FORWARD` iptables chain (with its own ACCEPT rules for allowed traffic, marked via `0x10000`), but that chain doesn't short-circuit the rest of the top-level `FORWARD` chain — packets still fall through to UFW's `ufw-reject-forward`/default-DROP further down before ever reaching Calico's own ACCEPT rule. Adding a plain `ufw route allow from 192.168.0.0/16` didn't fix it either, for the same reason: it's still evaluated in that same chain, in the same position, relative to the DROP.

Fix — this is the standard, documented approach for running a CNI (Calico, Flannel, Cilium, etc.) alongside UFW: let UFW's `INPUT` chain keep protecting the host itself, but let the CNI own `FORWARD`-chain policy for pod traffic instead of fighting it:
```bash
sudo sed -i 's/DEFAULT_FORWARD_POLICY="DROP"/DEFAULT_FORWARD_POLICY="ACCEPT"/' /etc/default/ufw
sudo ufw reload
```
Verified:
```
$ kubectl exec netcheck3 -- wget -q -O- --timeout=5 http://neverssl.com
<html><head><title>NeverSSL - Connecting ... </title>...
```

**Security note:** this does not reopen anything to the public internet — UFW's `INPUT` chain (which governs what can reach the host's own listening ports: SSH, HTTP/S, WireGuard) is untouched and still default-deny. `FORWARD` policy only governs traffic being routed *through* the host (pod egress, WireGuard peer traffic), which Calico and kube-proxy now police themselves via their own iptables rules — the same trust model every standard kubeadm+UFW guide recommends.

## Step 5 — Persistent volume support (local-path-provisioner)

Chose Rancher's `local-path-provisioner` over a full CSI/Longhorn-style setup because this is a single-node host with local SSD — no need for network-replicated storage, and it's the standard lightweight choice for exactly this case:
```bash
kubectl apply -f https://raw.githubusercontent.com/rancher/local-path-provisioner/v0.0.30/deploy/local-path-storage.yaml
kubectl patch storageclass local-path -p '{"metadata": {"annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}'
```
Result:
```
NAME                   PROVISIONER             RECLAIMPOLICY   VOLUMEBINDINGMODE      ALLOWVOLUMEEXPANSION   AGE
local-path (default)   rancher.io/local-path   Delete          WaitForFirstConsumer   false                  17s
```

Validated with a real PVC + pod (not just checking the StorageClass exists) — created a 100Mi PVC, wrote a file from one pod, deleted the pod, confirmed the PV persisted:
```bash
kubectl apply -f - <<'YAML'
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: pvc-test }
spec:
  accessModes: ["ReadWriteOnce"]
  resources: { requests: { storage: 100Mi } }
---
apiVersion: v1
kind: Pod
metadata: { name: pvc-test-pod }
spec:
  containers:
  - name: writer
    image: busybox:1.36
    command: ["sh", "-c", "echo hello-from-pvc > /data/test.txt && sleep 30"]
    volumeMounts: [{ name: data, mountPath: /data }]
  volumes: [{ name: data, persistentVolumeClaim: { claimName: pvc-test } }]
YAML
```
PVC bound, pod wrote the file, then both were deleted (reclaim policy `Delete` means the underlying PV cleans itself up automatically — confirmed via `kubectl get pv` showing it move to `Released` then disappear).

## Step 6 — Baseline resource limits

Applied a default `LimitRange` to the `default` namespace so pods without explicit resource requests/limits don't run unbounded on this 6 vCPU / 12GB host:
```bash
kubectl apply -f - <<'YAML'
apiVersion: v1
kind: LimitRange
metadata:
  name: default-limits
  namespace: default
spec:
  limits:
  - type: Container
    default: { cpu: "500m", memory: "512Mi" }
    defaultRequest: { cpu: "100m", memory: "128Mi" }
    max: { cpu: "4", memory: "8Gi" }
    min: { cpu: "10m", memory: "16Mi" }
YAML
```

**Follow-up, not yet done:** this only applies to the `default` namespace, which nothing will actually run in — Phase 5 creates dedicated namespaces for the Go/Rust services, and this same `LimitRange` (or a `ResourceQuota` for tighter per-namespace caps) needs to be applied there too once those namespaces exist. Kept as a reminder rather than guessing namespace names now.

## Step 7 — Kubernetes entry point (ingress-nginx) behind Caddy

Installed the baremetal/NodePort variant (not the cloud-LoadBalancer one — there's no cloud load balancer here, Caddy plays that role at the host level):
```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.11.3/deploy/static/provider/baremetal/deploy.yaml
```
Pinned the auto-assigned random NodePorts to fixed values so Caddy's config doesn't break on a controller redeploy:
```bash
kubectl patch svc ingress-nginx-controller -n ingress-nginx --type='json' -p '[
  {"op": "replace", "path": "/spec/ports/0/nodePort", "value": 30080},
  {"op": "replace", "path": "/spec/ports/1/nodePort", "value": 30443}
]'
```

Wired Caddy (`/etc/caddy/Caddyfile`) to proxy the public app hostname to it:
```caddyfile
app.bryan-javier-llm-local.cc {
	reverse_proxy localhost:30080
}

# Placeholder until Supabase is deployed (Phase 4)
supabase.bryan-javier-llm-local.cc {
	respond "Supabase not yet deployed (Phase 4)" 503
}
```
```bash
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
```

**Confirmed the full public path works end-to-end**, including real automatic HTTPS:
```
$ curl -sv https://app.bryan-javier-llm-local.cc
*  subject: CN=app.bryan-javier-llm-local.cc
*  issuer: C=US; O=Let's Encrypt; CN=YE2
*  SSL certificate verify ok.
> GET / HTTP/2
< HTTP/2 404
```
The `404` is correct and expected — no `Ingress` resource routes to a backend yet, since KeePass4Web isn't deployed until Phase 5. This confirms DNS → Caddy (real Let's Encrypt cert) → ingress-nginx is fully wired and just waiting for a backend.

## Step 8 — Keep the Kubernetes API private

Same pattern as SSH in [VPS-HARDENING-GUIDE.md](VPS-HARDENING-GUIDE.md) Step 5 — restrict `6443/tcp` to the WireGuard subnet plus the same temporary recovery IP (6443 was never publicly open before this, since UFW default-denies incoming; this is a proactive, defense-in-depth scoping so it can never accidentally become public later):
```bash
sudo ufw allow from 10.44.13.0/24 to any port 6443 proto tcp comment 'Kubernetes API - WireGuard only'
sudo ufw allow from 112.203.225.173 to any port 6443 proto tcp comment 'Kubernetes API - temporary recovery IP'
```
Verified `kubectl` still works locally on the VPS afterward (UFW allows all loopback-interface traffic unconditionally, and a local process connecting to the box's own IP is delivered via `lo`, not `eth0`, so this doesn't break same-host `kubectl` usage — only remote access is scoped).

To run `kubectl` from your own machine against this cluster, you'll need the WireGuard tunnel active and to copy `~/.kube/config` off the VPS (contains the cluster's admin credentials — handle it like the WireGuard peer configs, never paste it into chat or commit it).

---

## Phase 2 status: complete

All Phase 2 exit criteria met: node is `Ready`, DNS/networking confirmed via a real test pod (FQDN resolution + internet egress), and only the intended ports (`80`, `443` via Caddy; `6443` restricted to VPN) are reachable. Calico, local-path storage, ingress-nginx, and a real Let's Encrypt cert for `app.<domain>` are all live.

**Known follow-ups carried forward:**
- Apply the `LimitRange` (Step 6) to actual app namespaces once Phase 5 creates them.
- `supabase.<domain>` is a placeholder 503 in Caddy until Phase 4 deploys Supabase.
- The `app.<domain>` path returns a correct 404 until Phase 5 deploys KeePass4Web with a real `Ingress` resource.
- Same kernel-upgrade-pending note from Phase 1 still applies — reboot at a deliberate maintenance window, not mid-Phase-3/4.

## Configurable files reference

Where to look if you want to hand-tune anything from this phase later, and what each one controls.

| File (on VPS) | Controls | Notes |
|---|---|---|
| `/etc/kubernetes/admin.conf`, `~/.kube/config` | Cluster credentials `kubectl` uses | The full-admin kubeconfig. Treat like a password — don't copy off-box except over WireGuard, never commit it. |
| `/etc/kubernetes/kubeadm-config.yaml` (generated, not hand-written) | Cluster-wide kubeadm settings (pod CIDR, API server args, etc.) | Stored as the `kubeadm-config` ConfigMap in `kube-system`; edit via `kubectl edit cm kubeadm-config -n kube-system`, not the file directly, since kubeadm reads it from the API, not disk, after init. |
| `/etc/kubernetes/kubelet-resource-reservations.yaml` | Staged kubelet CPU/memory/eviction reservations (see [VPS-HARDENING-GUIDE.md](VPS-HARDENING-GUIDE.md) Step 8) | Not active yet — needs to be wired into kubelet's actual config (`/var/lib/kubelet/config.yaml`) or passed via `kubeadm init --config` on a future rebuild to take effect. |
| `/var/lib/kubelet/config.yaml` | Live kubelet configuration | Auto-generated by kubeadm; edit directly + `sudo systemctl restart kubelet` if adjusting live (e.g. eviction thresholds), but changes here are lost if the node is ever reset/rejoined — prefer feeding changes through `kubeadm init --config` for anything permanent. |
| `/etc/containerd/config.toml` | Container runtime config (cgroup driver, registry mirrors, etc.) | `sudo systemctl restart containerd` after edits. Changing `SystemdCgroup` back to `false` will break kubelet — don't. |
| Calico `Installation` custom resource | Pod network CIDR, encapsulation mode, IP pools | `kubectl edit installation default` — changing the CIDR after the fact is disruptive (existing pods keep old IPs); if you need a different range, better to `kubeadm reset` and redo Steps 1–2 with the new CIDR from the start. |
| `local-path` StorageClass + its ConfigMap (`local-path-config` in `local-path-storage` namespace) | Where dynamically-provisioned PV data actually lives on disk | Default path is `/opt/local-path-provisioner`; `kubectl edit cm local-path-config -n local-path-storage` to change it or add multiple storage paths across disks. |
| `LimitRange` (`default-limits`, currently only in the `default` namespace) | Default/min/max CPU+memory per container | `kubectl edit limitrange default-limits -n default`, or apply a copy to new namespaces as they're created (Phase 5). |
| `/etc/caddy/Caddyfile` | All host-level HTTP/HTTPS routing (public hostnames → backends) | `sudo caddy validate --config /etc/caddy/Caddyfile` before every reload; `sudo systemctl reload caddy` applies changes with zero downtime. This is where Phase 4 (Supabase/Kong) and Phase 5 (real KeePass4Web Ingress) routing gets added. |
| `ingress-nginx-controller` Service (`ingress-nginx` namespace) | Which local ports (currently `30080`/`30443`) Caddy proxies into | `kubectl edit svc ingress-nginx-controller -n ingress-nginx` — if you change these ports, update the Caddyfile to match or `app.<domain>` breaks. |
| `Ingress` resources (none yet — created in Phase 5) | Per-app hostname/path routing rules inside the cluster | This is where you'll point `app.<domain>` at the actual KeePass4Web Go Service once it's deployed. |
| `/etc/ufw/*.rules`, `sudo ufw status verbose` | All firewall rules, including the `6443` and `22` VPN-only restrictions from this phase | Prefer `sudo ufw allow/delete` commands over hand-editing the rules files directly — easier to keep the numbered/comment format consistent (see the cheat sheets in both guides). |

## Quick command cheat sheet

## Quick command cheat sheet

Kept up to date as each Phase 2 step progresses. Run from the operator's machine over SSH unless noted otherwise; `kubectl` runs as `bryjavier` on the VPS (kubeconfig at `~/.kube/config`).

### Cluster status
```bash
kubectl get nodes -o wide
kubectl get pods -A
kubectl get tigerastatus            # Calico component health
```

### Networking troubleshooting
```bash
kubectl run netcheck --image=busybox:1.36 --restart=Never --command -- sleep 600
kubectl wait --for=condition=Ready pod/netcheck --timeout=60s
kubectl exec netcheck -- nslookup kubernetes.default.svc.cluster.local   # use FQDN, not short name (busybox nslookup quirk)
kubectl exec netcheck -- wget -q -O- --timeout=5 http://neverssl.com
kubectl delete pod netcheck --wait=false
```

### UFW + Kubernetes forwarding
```bash
grep DEFAULT_FORWARD_POLICY /etc/default/ufw   # must be ACCEPT for pod egress to work
sudo iptables -L FORWARD -n -v --line-numbers  # inspect chain ordering if debugging
```

### Cluster bootstrap reference (already done, don't re-run)
```bash
sudo kubeadm init --pod-network-cidr=192.168.0.0/16 --apiserver-advertise-address=169.58.149.18 --control-plane-endpoint=169.58.149.18 --node-name=vmi3496773 --kubernetes-version=v1.31.14
kubectl taint nodes vmi3496773 node-role.kubernetes.io/control-plane:NoSchedule-
```

### Storage (local-path-provisioner)
```bash
kubectl get storageclass
kubectl get pv,pvc -A
kubectl edit cm local-path-config -n local-path-storage   # change on-disk storage path(s)
```

### Resource limits
```bash
kubectl get limitrange -A
kubectl describe limitrange default-limits -n default
```

### Ingress / Caddy
```bash
kubectl get pods,svc -n ingress-nginx
kubectl get ingress -A                                     # empty until Phase 5
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy
curl -s -o /dev/null -w "%{http_code}\n" -H "Host: app.bryan-javier-llm-local.cc" http://localhost:80
```

### Kubernetes API access control
```bash
sudo ufw status verbose | grep 6443
# From your own machine (needs WireGuard active):
scp -i ~/.ssh/contabo_keepass4web_vps bryjavier@10.44.13.1:~/.kube/config ~/.kube/keepass4web-vps-config
KUBECONFIG=~/.kube/keepass4web-vps-config kubectl get nodes
```
