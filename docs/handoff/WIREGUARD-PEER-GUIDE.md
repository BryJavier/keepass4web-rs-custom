# WireGuard Setup and Adding Peers Over Time

Interface: `wg0`
Subnet: `10.44.13.0/24`
Port: `54871/udp`
Initial peers: 5 devices

## 1. One-time server setup

On the VPS (`169.58.149.18`), as `bryjavier` with sudo:

```bash
sudo apt update && sudo apt install -y wireguard
umask 077
wg genkey | tee /etc/wireguard/server_private.key | wg pubkey > /etc/wireguard/server_public.key
```

Create `/etc/wireguard/wg0.conf`:

```ini
[Interface]
Address = 10.44.13.1/24
ListenPort = 54871
PrivateKey = <contents of server_private.key>
SaveConfig = false

# Peers are appended below by add-peer.sh — do not hand-edit past this line
```

Enable IP forwarding and NAT/firewall rules so tunnel traffic reaches the reverse proxy:

```bash
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-wireguard.conf
sudo sysctl --system
```

Add to `[Interface]` in `wg0.conf` (adjust `eth0` to your actual public interface name):

```ini
PostUp = ufw route allow in on wg0 out on eth0
PostDown = ufw route delete allow in on wg0 out on eth0
```

Open the firewall port and bring the interface up:

```bash
sudo ufw allow 54871/udp
sudo systemctl enable --now wg-quick@wg0
```

## 2. Peer numbering scheme

To avoid collisions as you add people, reserve IP ranges by role from the start:

| Range | Purpose |
|---|---|
| `10.44.13.1` | Server (wg0 itself) |
| `10.44.13.10`–`10.44.13.49` | Admin/personal devices (you) |
| `10.44.13.50`–`10.44.13.99` | Other trusted human admins, if any join later |
| `10.44.13.100`–`10.44.13.199` | Automation/CI runners that need VPN access, if ever needed |
| `10.44.13.200`–`10.44.13.254` | Spare/rotation |

Your initial 5 devices go in `10.44.13.10`–`10.44.13.14`.

## 3. Script to add a peer (reusable for future users too)

Save as `/usr/local/sbin/add-wg-peer.sh` on the VPS:

```bash
#!/usr/bin/env bash
set -euo pipefail
# Usage: add-wg-peer.sh <name> <tunnel-ip-last-octet>
# Example: add-wg-peer.sh bryan-laptop 10

NAME="$1"
OCTET="$2"
CLIENT_IP="10.44.13.${OCTET}/32"
SERVER_ENDPOINT="169.58.149.18:54871"
SERVER_PUBKEY=$(cat /etc/wireguard/server_public.key)
DNS_FOR_CLIENT="1.1.1.1"   # or your split-DNS resolver IP if you add one later

CLIENT_DIR="/etc/wireguard/peers/${NAME}"
mkdir -p "$CLIENT_DIR"
umask 077
wg genkey | tee "${CLIENT_DIR}/private.key" | wg pubkey > "${CLIENT_DIR}/public.key"
CLIENT_PRIV=$(cat "${CLIENT_DIR}/private.key")
CLIENT_PUB=$(cat "${CLIENT_DIR}/public.key")

# Append the peer to the running interface + persisted config
sudo wg set wg0 peer "$CLIENT_PUB" allowed-ips "$CLIENT_IP"
{
  echo ""
  echo "[Peer]"
  echo "# $NAME"
  echo "PublicKey = $CLIENT_PUB"
  echo "AllowedIPs = $CLIENT_IP"
} | sudo tee -a /etc/wireguard/wg0.conf > /dev/null

# Write the client config the human will import
cat > "${CLIENT_DIR}/${NAME}.conf" <<EOF
[Interface]
PrivateKey = ${CLIENT_PRIV}
Address = ${CLIENT_IP}
DNS = ${DNS_FOR_CLIENT}

[Peer]
PublicKey = ${SERVER_PUBKEY}
Endpoint = ${SERVER_ENDPOINT}
AllowedIPs = 10.44.13.0/24
PersistentKeepalive = 25
EOF

echo "Peer '$NAME' added. Client config at: ${CLIENT_DIR}/${NAME}.conf"
echo "Transfer that file to the user's device out-of-band (never over email/chat in plaintext)."
```

```bash
sudo chmod +x /usr/local/sbin/add-wg-peer.sh
```

## 4. Creating the initial 5 peers

```bash
sudo /usr/local/sbin/add-wg-peer.sh bryan-laptop   10
sudo /usr/local/sbin/add-wg-peer.sh bryan-phone     11
sudo /usr/local/sbin/add-wg-peer.sh bryan-desktop   12
sudo /usr/local/sbin/add-wg-peer.sh device-4        13
sudo /usr/local/sbin/add-wg-peer.sh device-5        14
```

Rename `device-4`/`device-5` to whatever those devices actually are — the name is just a label used in comments and the peer directory, it doesn't need to match anything else.

Each generated `.conf` file under `/etc/wireguard/peers/<name>/` is a complete client config — import it directly into the WireGuard app (desktop) or scan it as a QR code (mobile) via:

```bash
sudo apt install -y qrencode
qrencode -t ansiutf8 < /etc/wireguard/peers/bryan-phone/bryan-phone.conf
```

## 5. Adding more peers later (the future-users case)

Same script, next free octet:

```bash
sudo /usr/local/sbin/add-wg-peer.sh new-teammate-laptop 15
```

No service restart is required — `wg set` applies the new peer to the live interface immediately, and appending to `wg0.conf` persists it across reboots.

## 6. Revoking a peer

```bash
sudo wg set wg0 peer <public-key-of-that-peer> remove
```

Then delete the corresponding `[Peer]` block from `/etc/wireguard/wg0.conf` and remove `/etc/wireguard/peers/<name>/` so the octet can be reused.

## 7. Sanity check from a client

After importing a config and activating the tunnel:

```bash
ping 10.44.13.1
```

A reply confirms the tunnel is up. Hostnames like `studio.vpn.bryan-javier-llm-local.cc` won't resolve to anything *reachable* until the DNS records from [DNS-SETUP-GUIDE.md](DNS-SETUP-GUIDE.md) are in place and the reverse proxy on the VPS is bound and routing on `wg0`.
