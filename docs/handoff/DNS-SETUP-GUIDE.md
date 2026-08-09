# DNS Setup — Pointing bryan-javier-llm-local.cc to the VPS

Target VPS: `169.58.149.18` (Contabo)
Domain: `bryan-javier-llm-local.cc` (registered/managed at Hostinger)

## 1. Records to create in Hostinger's DNS zone editor

Log in to Hostinger → **Domains** → `bryan-javier-llm-local.cc` → **DNS / Nameservers** → **Manage DNS records**.

Create these A records (all pointing at the same VPS IP, since the host reverse proxy on the VPS routes by hostname):

| Type | Name (host) | Value | TTL |
|---|---|---|---|
| A | `app` | `169.58.149.18` | 300 (5 min, lower while testing) |
| A | `supabase` | `169.58.149.18` | 300 |
| A | `studio.vpn` | `169.58.149.18` | 300 |
| A | `argocd.vpn` | `169.58.149.18` | 300 |

Notes:
- Hostinger's editor may require `studio.vpn` and `argocd.vpn` entered exactly as two-label subdomains in the "Name" field (not `studio` + a separate `vpn` record) — enter the full subdomain as shown.
- Leave the root domain (`@`) and `www` alone unless you're also serving something at the bare domain; nothing in this plan needs them.
- Once things are stable, you can raise the TTL to 3600+ to reduce DNS query load.

## 2. Split DNS caveat for the VPN-only hostnames

`studio.vpn.bryan-javier-llm-local.cc` and `argocd.vpn.bryan-javier-llm-local.cc` resolving publicly to `169.58.149.18` is fine — the plan's firewall policy means nothing public actually answers on those hostnames (Studio/Argo CD listeners bind to the WireGuard interface only, not the public one). Public DNS resolution alone does not expose the service.

However, for these two hostnames to actually be *reachable* from a connected WireGuard client, you have two options (this was left open in the prerequisite checklist — "split DNS supplied by DNS provider, router, or a small VPN DNS resolver"):

- **Option A (simplest): use the same public A records above.** Since a WireGuard client still has normal internet DNS resolution, `studio.vpn.bryan-javier-llm-local.cc` will resolve to `169.58.149.18` for everyone, VPN-connected or not. The reverse-proxy on the VPS only needs to bind those two vhosts to the WireGuard interface's IP so non-VPN traffic can't reach them even though DNS resolves. This is what the A records above already set up — no extra DNS server needed.
- **Option B: real split DNS via a resolver on the WireGuard subnet** (e.g. `dnsmasq` or `unbound` running on the VPS, handed out to peers via WireGuard's `DNS =` client config) that resolves the `.vpn` hostnames to the VPS's WireGuard tunnel IP (e.g. `10.44.13.1`) instead of the public IP, while non-VPN resolution never sees those records at all. More correct/private, but adds a moving part.

Recommendation: start with Option A (already covered by the records above) since the plan's firewall/reverse-proxy binding is the actual security boundary, not DNS. Revisit Option B later if you want the `.vpn` hostnames to not resolve publicly at all.

## 3. Verify propagation before requesting certificates

```bash
dig +short app.bryan-javier-llm-local.cc
dig +short supabase.bryan-javier-llm-local.cc
dig +short studio.vpn.bryan-javier-llm-local.cc
dig +short argocd.vpn.bryan-javier-llm-local.cc
```

Each should return `169.58.149.18`. This can take a few minutes up to a few hours depending on the previous TTL of the zone (new zones are usually fast).

## 4. DNS-01 API token (for cert-manager / acme.sh later) — CONFIRMED AVAILABLE

Certificate issuance for the VPN-only hostnames needs DNS-01 validation (they don't answer HTTP challenges since they're not meant to be publicly served). Verified in hPanel → **Dev tools → API** (`hpanel.hostinger.com/api`): Hostinger exposes a documented API, explicitly including "Manage domains and DNS like code," with a one-click **Generate API token** button on that page. No Cloudflare fallback needed.

1. Token generation is deferred until `platform-secrets` exists (Phase 3) — generating it now with nowhere encrypted to put it just means it sits around in plaintext somewhere in the meantime.
2. When Phase 3 is underway: go to `hpanel.hostinger.com/api` → **Generate API token**, scope it to DNS management only if Hostinger's token UI allows scoping, then immediately encrypt it into `platform-secrets` via SOPS. Don't paste the token into chat or commit it in plaintext anywhere.
3. Also present on that page: a **Hostinger Connector** VS Code/Cursor/Claude Code MCP extension. Not needed for this plan — skip it, it's for IDE-integrated Hostinger management, unrelated to cert-manager's DNS-01 flow.

**Status:** Phase 0 DNS prerequisites are now fully closed — all four hostnames resolve correctly (verified via DNS propagation check) and the API token path is confirmed available for later.
