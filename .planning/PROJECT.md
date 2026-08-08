# KeePass4Web

## What This Is

KeePass4Web is a public Go web application for one person's private KeePass vaults. It uses Go-rendered HTML, HTMX, and Tailwind for the browser experience; Supabase for identity, owner-scoped metadata, and encrypted `.kdbx` storage; and a private Rust service that retains the existing `keepass-rs` decoding logic.

Version one lets a signed-in owner upload, select, and open multiple private vaults without exposing master passwords, key files, decoded vault data, or protected values outside their permitted boundary.

## Core Value

A signed-in user can upload, select, and securely open two private KeePass vaults through the HTMX interface, while cross-user access is denied.

## Requirements

### Validated

(None yet — ship to validate)

### Active

- [ ] Establish a safe public-Go/private-Rust operating baseline.
- [ ] Give each authenticated owner multiple private `.kdbx` vaults in Supabase.
- [ ] Make the existing Rust KeePass decoder available only through a trusted private contract.
- [ ] Authorize every browser-to-vault request in Go and keep browser sessions opaque.
- [ ] Deliver the progressive HTML and HTMX vault workflow.
- [ ] Prove access isolation, cleanup, redaction, and release readiness.

### Out of Scope

- Vault sharing — v1 is personal and owner-only.
- Organizations and invitations — no multi-user collaboration model is being introduced.
- Concurrent editing and conflict resolution — v1 is a read-only vault experience.
- Vault synchronization — original encrypted vault storage is in scope; sync semantics are not.
- Reimplementing KeePass decoding in Go or JavaScript — the existing Rust `keepass-rs` path remains authoritative.

## Context

This is a brownfield Rust KeePass codebase being extended with a public Go application and Supabase. The existing decoder and encrypted expiring cache stay in Rust. All browser-to-vault requests enter Go, which validates the Supabase session and ownership before retrieving a private object or calling Rust.

## Constraints

- **Runtime**: Public Go web application, internal Rust service, and Supabase — the approved target topology.
- **Interface**: Go `html/template`, HTMX, and Tailwind CSS — critical flows retain ordinary HTML form fallbacks.
- **Security boundary**: Rust has no public listener, route, host port, or ingress — only Go may call it over authenticated private HTTP.
- **Data**: Supabase stores identity, non-sensitive vault metadata, and original encrypted `.kdbx` objects only — no KeePass secrets or decoded values.
- **Authorization**: Every application table has RLS and every Storage object is owner-scoped by `auth.uid()` — cross-user metadata and object access must fail.
- **Secret handling**: Passwords, key-file bytes, request bodies, tokens, decoded entries, and protected values must not enter logs, traces, error pages, sessions, or browser storage — transient forwarding to Rust is the only permitted handling.
- **Session handling**: Browser sessions retain only an opaque active-vault handle — Rust binds it to the authenticated user and vault and clears cached state on close, expiry, logout notification, or authorization failure.

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Go owns the public HTTP interface and server rendering | Centralizes browser authentication, authorization, templates, and the private Rust call | — Pending |
| Rust retains `keepass-rs` decoding and encrypted expiring cache | Avoids duplicating sensitive KeePass logic in Go or JavaScript | — Pending |
| Supabase provides Auth, Postgres metadata, and private Storage | Gives managed identity and owner-enforced data boundaries | — Pending |
| Vault objects use `<owner UUID>/<vault UUID>.kdbx` paths | Makes Storage ownership policies mechanically enforceable | — Pending |
| Rust handles are opaque and bound to user and vault IDs | Prevents cross-user handle replay and limits browser session exposure | — Pending |
| HTMX progressively enhances normal HTML forms | Critical vault actions remain usable without JavaScript | — Pending |

## Evolution

After each phase, review active requirements, move verified work to Validated, record durable decisions, and keep the security boundary unchanged unless explicitly re-approved.

---
*Last updated: 2026-08-02 after approved workflow ingest*
