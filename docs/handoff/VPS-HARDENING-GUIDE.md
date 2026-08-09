# VPS Hardening Guide — Phase 1 (Contabo VPS)

This is a running log of every step performed to harden the Contabo VPS per [VPS-INTEGRATION-PLAN.md](../../VPS-INTEGRATION-PLAN.md) Phase 1. Each step shows the real command run and its real output, so you can reproduce it manually later. Screenshots for browser/panel steps are described in text (the automation tooling used to perform them can't export images to files) but were shown live during the session.

**Target server:** Contabo `vmi3496773`, Ubuntu 24.04.4 LTS, public IP `169.58.149.18`, 6 vCPU / 12GB RAM / 200GB SSD.

## Step 0 — Establish key-based SSH access

**Goal:** stop relying on password SSH before we harden anything.

1. Generated a dedicated ed25519 keypair locally, not reused from anywhere else:
   ```bash
   ssh-keygen -t ed25519 -f ~/.ssh/contabo_keepass4web_vps -N "" -C "bryjavier@keepass4web-vps-hardening"
   ```
   Public key installed on the server:
   ```
   ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJC9HcaHWlGtEhYFO/YEvnYmIYTqBeR6JgN7dd7Y2rvV bryjavier@keepass4web-vps-hardening
   ```

2. Checked Contabo's panel (`hpanel`-style Customer Panel → Account → Secret Management → SSH) for a way to push the key to an already-running instance. **Finding: not possible** — Contabo only applies stored SSH keys during install/reinstall of an instance, never to a live one. Screenshot shown in-session: "No SSH-Keys" page with the note *"You can use SSH-Keys authentication to use them when installing / reinstalling your instances."*

3. Since the VPS already had the `bryjavier` user and we didn't want to reinstall (would wipe it), the key was added manually via the user's own `bryjavier@169.58.149.18` SSH session (password auth, since it was still enabled at this point):
   ```bash
   mkdir -p ~/.ssh && echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJC9HcaHWlGtEhYFO/YEvnYmIYTqBeR6JgN7dd7Y2rvV bryjavier@keepass4web-vps-hardening" >> ~/.ssh/authorized_keys && chmod 700 ~/.ssh && chmod 600 ~/.ssh/authorized_keys
   ```

4. Verified from the automation side:
   ```bash
   $ ssh -o BatchMode=yes -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18 "echo CONNECTED && whoami && hostname && cat /etc/os-release | grep PRETTY"
   CONNECTED
   bryjavier
   vmi3496773
   PRETTY_NAME="Ubuntu 24.04.4 LTS"
   ```

## Step 0.5 — Passwordless sudo for the automation account

**Why:** privileged (`sudo`) commands need a password prompt by default, and that password must never be typed by anyone other than the account owner — including automation. `bryjavier` was already in the `sudo` group; we added a scoped NOPASSWD rule so remote command execution could proceed without ever handling that password.

Run by the user directly (typing their own sudo password interactively, never shared):
```bash
echo "bryjavier ALL=(ALL) NOPASSWD:ALL" | sudo tee /etc/sudoers.d/bryjavier-nopasswd && sudo chmod 440 /etc/sudoers.d/bryjavier-nopasswd
```
Output:
```
bryjavier ALL=(ALL) NOPASSWD:ALL
SUDOERS_OK
```

Verified remotely:
```bash
$ ssh -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18 "sudo -n true && echo NOPASSWD_CONFIRMED"
NOPASSWD_CONFIRMED
```

**Security note:** this grants broad NOPASSWD sudo to one already-trusted admin account, not a service account or public-facing process. Revisit if this VPS ever needs multiple human admins with different trust levels (see "future apps" prerequisite from the integration plan).

## Step 0.75 — Baseline system check

```bash
$ uname -a
Linux vmi3496773 6.8.0-136-generic #136-Ubuntu SMP PREEMPT_DYNAMIC Wed Jul 1 21:53:05 UTC 2026 x86_64 GNU/Linux

$ nproc && free -h && df -h /
6
               total        used        free      shared  buff/cache   available
Mem:            11Gi       484Mi        11Gi       1.0Mi       236Mi        11Gi
Swap:             0B          0B          0B
Filesystem      Size  Used Avail Use% Mounted on
/dev/sda1       193G  2.4G  191G   2% /

$ sudo ufw status verbose
Status: inactive

$ sudo grep -E 'PermitRootLogin|PasswordAuthentication|Port' /etc/ssh/sshd_config
#Port 22
PermitRootLogin yes
#PasswordAuthentication yes

$ dpkg -l unattended-upgrades | tail -1
ii  unattended-upgrades 2.9.1+nmu4ubuntu1 all  automatic installation of security upgrades
```

Findings: firewall not yet active, root login and password auth are still at their (insecure) defaults, `unattended-upgrades` is pre-installed by Contabo's image but not yet confirmed configured. All three addressed in the next steps.

## Step 1 — Disable root login and password SSH auth

**Gotcha hit:** Contabo's image ships `/etc/ssh/sshd_config.d/50-cloud-init.conf` which sets `PasswordAuthentication yes`. OpenSSH's `sshd_config` uses **first-occurrence-wins**, and `Include /etc/ssh/sshd_config.d/*.conf` sits near the top of the main config, processing files in the directory in glob (alphabetical) order. A drop-in named `99-hardening.conf` loads *after* `50-cloud-init.conf` and loses. The fix: name your override so it sorts before `50` — we used `00-hardening.conf`.

```bash
sudo tee /etc/ssh/sshd_config.d/00-hardening.conf > /dev/null <<'CONF'
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
X11Forwarding no
MaxAuthTries 3
CONF
sudo sshd -t && sudo systemctl restart ssh
```

Verified effective config after restart:
```bash
$ sudo sshd -T | grep -E '^permitrootlogin|^passwordauthentication|^kbdinteractiveauthentication'
permitrootlogin no
passwordauthentication no
kbdinteractiveauthentication no
```

Sanity-checked key login still works immediately after the restart (before touching anything else):
```bash
$ ssh -o BatchMode=yes -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18 "echo KEY_LOGIN_STILL_WORKS"
KEY_LOGIN_STILL_WORKS
```

**If you're doing this manually:** always verify key-based login works in a *second, separate* terminal session before closing the session you used to make the change — if `sshd -t` passed but something is still wrong, you don't want to be locked out with no active session left to fix it.

## Step 2 — Confirm automatic security updates

Contabo's image already had the `unattended-upgrades` package installed but not fully enabled. Configured and enabled it:

```bash
sudo tee /etc/apt/apt.conf.d/20auto-upgrades > /dev/null <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Download-Upgradeable-Packages "1";
APT::Periodic::AutocleanInterval "7";
APT::Periodic::Unattended-Upgrade "1";
CONF
sudo systemctl enable --now unattended-upgrades
```

`/etc/apt/apt.conf.d/50unattended-upgrades` (Ubuntu default) already includes the `${distro_id}:${distro_codename}-security` origin, so security patches are covered out of the box — no edit needed there.

Verified with a dry run:
```bash
$ sudo unattended-upgrade --dry-run --debug 2>&1 | tail -5
pkgs that look like they should be upgraded:
Fetched 0 B in 0s (0 B/s)
fetch.run() result: 0
No packages found that can be upgraded unattended and no pending auto-removals
```
(Zero pending upgrades is expected — this is a freshly-provisioned image.)

## Step 3 — WireGuard server and initial peers

Matches [WIREGUARD-PEER-GUIDE.md](WIREGUARD-PEER-GUIDE.md): subnet `10.44.13.0/24`, port `54871/udp`, public interface `eth0` (confirmed via `ip -o -4 route show to default`).

```bash
sudo apt-get install -y wireguard qrencode
sudo mkdir -p /etc/wireguard
sudo bash -c 'umask 077; wg genkey | tee /etc/wireguard/server_private.key | wg pubkey > /etc/wireguard/server_public.key'
```
Server public key: `ZPS7HCKEC+IVwMYGmASm/c+0DQEdoLa4HxnLvV6R5kM=`

`/etc/wireguard/wg0.conf`:
```ini
[Interface]
Address = 10.44.13.1/24
ListenPort = 54871
PrivateKey = <server_private.key contents>
SaveConfig = false
PostUp = ufw route allow in on wg0 out on eth0
PostDown = ufw route delete allow in on wg0 out on eth0
```

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee /etc/sysctl.d/99-wireguard.conf
sudo sysctl --system
sudo systemctl enable --now wg-quick@wg0
```

Verified:
```bash
$ sudo wg show
interface: wg0
  public key: ZPS7HCKEC+IVwMYGmASm/c+0DQEdoLa4HxnLvV6R5kM=
  private key: (hidden)
  listening port: 54871
```

Deployed `/usr/local/sbin/add-wg-peer.sh` (same script as in WIREGUARD-PEER-GUIDE.md) and generated the 5 initial peers:
```bash
sudo /usr/local/sbin/add-wg-peer.sh bryan-laptop 10
sudo /usr/local/sbin/add-wg-peer.sh bryan-phone 11
sudo /usr/local/sbin/add-wg-peer.sh bryan-desktop 12
sudo /usr/local/sbin/add-wg-peer.sh device-4 13
sudo /usr/local/sbin/add-wg-peer.sh device-5 14
```

All 5 client `.conf` files (each with its own private key) were pulled off the server and delivered directly to the user — never pasted into chat or committed anywhere. Rename `device-4`/`device-5` locally once you know which physical devices they're for; the label only lives in `/etc/wireguard/peers/<name>/` on the server and in the file name, it isn't otherwise significant.

**Note:** the WireGuard tunnel is up, but nothing can reach it yet — the firewall (next step) hasn't opened `54871/udp` publicly.

## Step 4 — Firewall policy (UFW)

**Ordering matters here:** allow the SSH rule *before* flipping the default-incoming policy to deny and enabling UFW, or you lock yourself out immediately with no recovery path (short of Contabo's VNC console).

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow 22/tcp comment 'SSH - temporary, will restrict to WireGuard after VPN validation'
sudo ufw allow 80/tcp comment 'HTTP - reverse proxy'
sudo ufw allow 443/tcp comment 'HTTPS - reverse proxy'
sudo ufw allow 54871/udp comment 'WireGuard'
sudo ufw --force enable
```

Result:
```
Status: active
Default: deny (incoming), allow (outgoing), deny (routed)

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
80/tcp                     ALLOW IN    Anywhere
443/tcp                    ALLOW IN    Anywhere
54871/udp                  ALLOW IN    Anywhere
Anywhere on eth0           ALLOW FWD   Anywhere on wg0
```

Verified SSH survived the transition:
```bash
$ ssh -o BatchMode=yes -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18 "echo SSH_STILL_REACHABLE_WITH_FIREWALL_ON"
SSH_STILL_REACHABLE_WITH_FIREWALL_ON
```

**Deliberately left open, pending VPN validation:** `22/tcp` is still reachable from anywhere. The plan calls for restricting SSH to the WireGuard subnet only, but doing that *before* confirming at least one real device can actually connect over WireGuard would risk locking out all access — the automation account's own SSH session doesn't route through the tunnel. This step is paused until a peer config is imported and connectivity is confirmed from the user's side; then SSH gets narrowed to `10.44.13.0/24` only.

**Never opened, per plan:** `5432/tcp` (Postgres), `6543/tcp` (Supavisor), `6443/tcp` (Kubernetes API) — none of these services exist yet on this VPS, and UFW's default-deny-incoming means they won't be reachable even once installed unless explicitly allowed later, which the plan says never to do publicly.

## Step 5 — Restrict SSH to WireGuard (with a temporary recovery IP)

Before narrowing SSH, the tunnel was validated end-to-end by importing `bryan-desktop.conf` on the user's machine and pinging the server's tunnel address:
```
$ ping 10.44.13.1
64 bytes from 10.44.13.1: icmp_seq=0 ttl=64 time=2461.075 ms
...
--- 10.44.13.1 ping statistics ---
21 packets transmitted, 13 packets received, 38.1% packet loss
round-trip min/avg/max/stddev = 260.900/489.872/2461.075/574.027 ms
```

**Reading this correctly:** 38% ICMP loss and jittery latency looks alarming, but confirmed server-side via `sudo wg show wg0 transfer` and `latest-handshakes` that a real encrypted handshake completed and traffic flowed (`2608 rx / 2320 tx` bytes on the `bryan-desktop` peer). Correct `ttl=64` on every reply confirms packets arrived directly, not through some unexpected extra hop. The loss/jitter is almost certainly the client's local network, not a WireGuard misconfiguration — and TCP traffic (SSH, HTTPS) tolerates that kind of loss far better than bare ICMP, since it retransmits.

Given the plan explicitly allows "an optional temporary trusted-IP recovery rule," SSH was restricted to the WireGuard subnet *plus* the user's current public IP as a safety net, rather than committing blindly to VPN-only on a connection that hadn't been stress-tested:

```bash
sudo ufw delete allow 22/tcp
sudo ufw allow from 10.44.13.0/24 to any port 22 proto tcp comment 'SSH - WireGuard only'
sudo ufw allow from 112.203.225.173 to any port 22 proto tcp comment 'SSH - temporary recovery IP, remove once VPN confirmed reliable'
```

Verified reachability didn't break:
```bash
$ ssh -o BatchMode=yes -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18 "echo STILL_REACHABLE"
STILL_REACHABLE
```

**Follow-up task (not yet done):** once WireGuard has been used reliably for a while (e.g. a week of normal use, or after diagnosing the client-side loss), remove the `112.203.225.173` recovery rule with:
```bash
sudo ufw delete allow from 112.203.225.173 to any port 22 proto tcp
```
Also note: `112.203.225.173` is a **dynamic residential/mobile IP** in most ISP setups — if it changes, this rule silently stops being useful as a recovery path. Don't treat it as permanent.

### Troubleshooting: "connection timed out" over WireGuard

Hit this a few times after Step 5, worth documenting since it's an easy mistake to repeat:

**Symptom:** connected to the WireGuard VPN, but `ssh -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18` (the **public IP**) times out.

**Cause:** the client configs generated by `add-wg-peer.sh` set `AllowedIPs = 10.44.13.0/24` (see Step 3) — this is a routing scope, not just an access-control setting. Only traffic destined for that subnet actually goes through the `wg0` tunnel interface; a connection to the public IP `169.58.149.18` is not in that range, so it goes out over the client's normal internet connection instead. Once SSH was restricted to the WireGuard subnet + one recovery IP (Step 5), that ordinary-internet-route connection arrives at the VPS from whatever IP the client's actual network assigns — which UFW then blocks unless it happens to match the whitelisted recovery IP.

**Fix:** when connected via WireGuard, SSH to the **tunnel IP**, not the public IP:
```bash
ssh -i ~/.ssh/contabo_keepass4web_vps bryjavier@10.44.13.1
```

**Separately diagnosed while debugging this:** mobile data tethering showed real packet loss on the WireGuard tunnel itself (measured 64.7% loss via `ping 10.44.13.1` on one attempt, 0% on a later attempt with a better signal) — that's a mobile network quality issue independent of the SSH-target mistake above, and can independently cause `ssh` to hang/timeout even when using the correct `10.44.13.1` address, since TCP tolerates packet loss far worse than raw ICMP. If mobile SSH access needs to be reliably usable (not just occasional), consider installing `mosh` (mobile shell) instead of relying on plain SSH over a lossy link — not done yet, only diagnosed.

## Step 6 — Docker Engine, Compose, and Caddy reverse proxy

Installed from Docker's official apt repo (not Ubuntu's, which lags):
```bash
sudo install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list
sudo apt-get update && sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo usermod -aG docker bryjavier
sudo systemctl enable --now docker
```
Result: `Docker version 29.7.2`, `Docker Compose version v5.4.0`, service active.

Reverse proxy — chose **Caddy** over nginx for this plan because it has first-class automatic HTTPS (Let's Encrypt HTTP-01 and DNS-01) built in with no separate cert-manager-style tooling needed at the host level, which matters since Phase 4/5 need both public certs (`app`, `supabase`) and DNS-01 certs for the VPN-only hostnames.
```bash
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt-get update && sudo apt-get install -y caddy
```
Result: `v2.11.4`, enabled and active.

**Not yet configured:** the actual `Caddyfile` routing (`app.`, `supabase.`, `studio.vpn.`, `argocd.vpn.` vhosts) — there's nothing to route to yet since Kubernetes (Phase 2) and Supabase (Phase 4) don't exist on this host yet. Caddy is running with its default config for now.

## Step 7 — Kubernetes runtime prerequisites

This gets the host ready for Phase 2's `kubeadm init`, without actually bootstrapping the cluster yet (that's a separate, bigger step with Calico networking).

```bash
sudo swapoff -a
sudo sed -i '/ swap /s/^/#/' /etc/fstab   # swap was already 0B on this image, but future-proofed anyway

echo -e "overlay\nbr_netfilter" | sudo tee /etc/modules-load.d/k8s.conf
sudo modprobe overlay && sudo modprobe br_netfilter

cat <<SYSCTL | sudo tee /etc/sysctl.d/k8s.conf
net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1
SYSCTL
sudo sysctl --system

# containerd was already installed as a Docker Engine dependency — just switching its cgroup driver to match kubelet's expectation
sudo mkdir -p /etc/containerd
containerd config default | sudo tee /etc/containerd/config.toml
sudo sed -i 's/SystemdCgroup = false/SystemdCgroup = true/' /etc/containerd/config.toml
sudo systemctl restart containerd

sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.k8s.io/core:/stable:/v1.31/deb/Release.key | sudo gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v1.31/deb/ /' | sudo tee /etc/apt/sources.list.d/kubernetes.list
sudo apt-get update && sudo apt-get install -y kubelet kubeadm kubectl
sudo apt-mark hold kubelet kubeadm kubectl   # prevent unattended-upgrades from silently bumping the cluster version
```
Result: `kubeadm v1.31.14`, `kubelet v1.31.14`, `kubectl v1.31.14` — all held from auto-upgrade so `unattended-upgrades` (Step 2) won't touch cluster version without a deliberate decision.

**Why v1.31 specifically:** it's the most recent branch with a full year of support runway left as of this setup, and matches what's widely documented for kubeadm single-node installs. Revisit before Phase 2 if a newer stable branch makes more sense by then.

## Step 8 — Resource and log limits

**Docker log rotation** — without this, container logs can grow unbounded and fill the disk:
```bash
sudo mkdir -p /etc/docker
cat <<JSON | sudo tee /etc/docker/daemon.json
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "default-ulimits": { "nofile": { "Name": "nofile", "Hard": 64000, "Soft": 64000 } }
}
JSON
sudo systemctl restart docker
```

**journald cap** — same reasoning, for system logs generally:
```bash
sudo mkdir -p /etc/systemd/journald.conf.d
cat <<CONF | sudo tee /etc/systemd/journald.conf.d/size-limit.conf
[Journal]
SystemMaxUse=1G
SystemMaxFileSize=100M
CONF
sudo systemctl restart systemd-journald
```

**Kubelet resource reservations** — staged now, applied at Phase 2's `kubeadm init --config` (kubelet isn't running yet, so this can't take effect until the cluster is bootstrapped):
```bash
sudo mkdir -p /etc/kubernetes
cat <<YAML | sudo tee /etc/kubernetes/kubelet-resource-reservations.yaml
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
systemReserved:
  cpu: "500m"
  memory: "512Mi"
  ephemeral-storage: "2Gi"
kubeReserved:
  cpu: "500m"
  memory: "512Mi"
  ephemeral-storage: "1Gi"
evictionHard:
  memory.available: "300Mi"
  nodefs.available: "10%"
YAML
```
On a 6 vCPU / 12GB / 200GB host, this reserves ~1 vCPU and ~1GB RAM combined for system + kubelet overhead before Kubernetes workloads or Docker Compose (Supabase) can consume the rest — matches the plan's requirement that "neither can starve the host."

---

## Phase 1 status: complete

All Phase 1 exit criteria met: VPS is patched (unattended-upgrades active), reachable by VPN (WireGuard validated end-to-end), and administrative services are not public (SSH restricted to WireGuard + one temporary recovery IP; UFW default-deny everywhere else). Docker, Caddy, and the Kubernetes runtime packages are staged for Phase 2/4/5.

**Known follow-ups carried forward:**
- Remove the temporary SSH recovery IP (`112.203.225.173`) once WireGuard reliability is confirmed over normal use (see Step 5).
- A kernel upgrade is pending (`6.8.0-136` running, `6.8.0-137` available) — reboot at a convenient maintenance window, not mid-Phase-2.
- Caddy's actual routing config, and the Kubernetes cluster bootstrap itself (`kubeadm init` + Calico), are Phase 2 work, not yet done.

## Quick command cheat sheet

Kept up to date as each phase progresses. Run from the operator's machine unless noted "on VPS."

### SSH access
```bash
# Via the whitelisted recovery IP (only works from that one IP, see Step 5)
ssh -i ~/.ssh/contabo_keepass4web_vps bryjavier@169.58.149.18

# Via WireGuard (use this when on any other network) — must target the TUNNEL IP, not the public IP
ssh -i ~/.ssh/contabo_keepass4web_vps bryjavier@10.44.13.1
```

### WireGuard (on VPS)
```bash
sudo wg show                              # peer status, handshakes, transfer
sudo systemctl status wg-quick@wg0        # interface service status
sudo /usr/local/sbin/add-wg-peer.sh <name> <octet>   # add a new peer (10.44.13.<octet>)
sudo wg set wg0 peer <pubkey> remove      # revoke a peer live
```

### Firewall (on VPS)
```bash
sudo ufw status verbose                   # current rules
sudo ufw allow <port>/<tcp|udp> comment '<why>'
sudo ufw delete allow <port>/<tcp|udp>
```

### SSH hardening (on VPS)
```bash
sudo sshd -T | grep -E '^permitrootlogin|^passwordauthentication'   # effective config
sudo sshd -t && sudo systemctl restart ssh                          # validate + reload after edits
```
Remember: name overrides in `/etc/ssh/sshd_config.d/` so they sort **before** `50-cloud-init.conf` (e.g. `00-hardening.conf`) — Contabo's cloud-init drop-in sets `PasswordAuthentication yes` and first-match-wins in sshd_config.

### Automatic updates (on VPS)
```bash
sudo unattended-upgrade --dry-run --debug   # preview what would be patched
sudo systemctl status unattended-upgrades
```

### System baseline (on VPS)
```bash
uname -a && nproc && free -h && df -h /
```

### Docker / Compose (on VPS)
```bash
sudo docker ps -a                         # containers
sudo docker compose -f <file> up -d       # bring up a Compose stack (Supabase, Phase 4)
sudo docker system df                     # disk usage by images/containers/volumes
sudo systemctl status docker
cat /etc/docker/daemon.json               # log rotation + ulimits config
```

### Caddy reverse proxy (on VPS)
```bash
sudo systemctl status caddy
sudo caddy validate --config /etc/caddy/Caddyfile
sudo systemctl reload caddy               # zero-downtime reload after Caddyfile edits
```

### Kubernetes runtime (on VPS, pre-cluster-bootstrap)
```bash
kubeadm version && kubelet --version && kubectl version --client
sudo systemctl status containerd
apt-mark showhold                         # confirm kubelet/kubeadm/kubectl are version-pinned
cat /etc/kubernetes/kubelet-resource-reservations.yaml   # staged reservations, applied at kubeadm init (Phase 2)
```

### Logs and disk usage (on VPS)
```bash
journalctl --disk-usage
sudo docker system df
df -h /
```
