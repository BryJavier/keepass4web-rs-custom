# Context

## HTMX, Go, Rust, and Supabase Implementation Workflow
- source: .planning/WORKFLOW.md

DATA_a7K3mP9q_START
Build a personal multi-vault KeePass application with Go-rendered HTML, HTMX,
and Tailwind CSS. Go is the browser-facing backend and Supabase integration
layer. A private Rust service owns the existing `keepass-rs` decoding logic.

Version one supports a single account with multiple private vaults. Vault
sharing, organizations, invitations, concurrent editing, and conflict
resolution are out of scope.

Go transiently handles password/key-file request data while forwarding it to
Rust. Request bodies, tokens, decrypted entries, protected values, and
key-file bytes must not appear in logs, traces, error pages, or sessions.
Rust is internal-only: no public listener, route, or ingress.

- `vaults` stores UUID `id`, `owner_id`, display name, private object path,
  timestamps, and non-sensitive upload state.
- Each user may own multiple vaults. Object paths are
  `<owner UUID>/<vault UUID>.kdbx` in a private Storage bucket.
- Row Level Security is enabled on every application table. Policies allow a
  user to access only rows where `owner_id = auth.uid()`.
- Storage policies permit objects only when the leading object-path segment
  equals `auth.uid()`.
- Browser assets contain only public Supabase configuration. Service-role
  credentials are backend-only and never rendered or bundled.

Go calls Rust using authenticated private HTTP over loopback or a private
container network. Go supplies a short-lived service credential and
correlation ID; Rust rejects public-origin traffic and never trusts
browser-supplied ownership claims.

Rust binds every handle to its user and vault IDs. It retains the existing
encrypted expiring cache, clearing the cache/key on close, expiry, logout
notification, or authorization failure. KeePass decoding is never rewritten
in Go.

**Exit gate:** all tests pass, cross-user isolation is proven, Rust remains
private, and no secret appears in source control, browser assets, sessions,
or logs.

A signed-in user can manage multiple private `.kdbx` vaults through a
Go-rendered HTMX/Tailwind UI. Go authorizes Supabase access, Rust alone decodes
active vaults, and tests prove normal use, access denial, expiry, logout
cleanup, and secret redaction.
DATA_a7K3mP9q_END

## HTMX, Go, Rust, and Supabase Implementation Plan
- source: docs/superpowers/plans/2026-08-02-htmx-go-rust-supabase.md

DATA_Q4v8Nc2L_START
**Goal:** Deliver a server-rendered, multi-vault KeePass application where Go
uses Supabase for identity/storage and calls a private Rust service to decode
vaults.

**Architecture:** Go owns the public HTTP interface, server-side HTML,
Supabase authorization, and the private Rust-client call. Rust retains the
existing KeePass parsing, encrypted cache, and key lifecycle, but exposes them
only through a trusted internal HTTP API. HTMX progressively enhances Go
templates and Tailwind provides all styling.

**Tech Stack:** Go, `net/http`/`html/template`, HTMX, Tailwind CSS, Supabase
Auth/Postgres/Storage, Rust, Actix Web, `keepass-rs`, Playwright.

- Rust is internal-only and has no public ingress or host port.
- Supabase stores identity, vault metadata, and original encrypted `.kdbx`
  objects only; it never stores KeePass secrets or decoded values.
- Go never persists master passwords, key files, decoded entries, protected
  values, bearer tokens, or Rust service credentials.
- All Postgres application tables and Storage objects require owner-only RLS
  or Storage policies based on `auth.uid()`.
- Use plain HTML form behavior for critical flows; HTMX is enhancement only.
- Keep existing KeePass parsing/decryption in Rust; do not reimplement it in
  Go or JavaScript.

### Task 1: Establish repository layout and safe configuration

**Produces:** a Go module, documented non-secret configuration contract, and a
development topology that exposes Go only while Rust is reachable by service
name on the internal Compose network.

### Task 2: Add Supabase schema, private storage, and policy tests

**Produces:** owner-scoped `vaults` records and private object storage with
testable database and Storage policy boundaries.

### Task 3: Extract the private Rust KeePass API

**Produces:** internal `unlock`, `groups`, `entries`, `entry`, `reveal`,
`search`, and `close` endpoints using opaque vault handles bound to user and
vault IDs.

### Task 4: Implement Go authentication, Rust client, and ownership handlers

**Produces:** authenticated Go routes that list/open owned vaults, send unlock
data only to Rust, and keep only an opaque Rust handle in the encrypted Go
session.

### Task 5: Deliver the HTMX and Tailwind read-only vault interface

**Produces:** progressively enhanced server-rendered pages/fragments for
vault management, unlock, browse, search, protected reveal, close, and logout.

### Task 6: Release hardening and deployment verification

**Produces:** deployable private-service topology, operational documentation,
and automated quality gates.

- Scope coverage: the plan provides multi-vault ownership but contains no
  sharing, editing, or synchronization work.
- Consistency: all browser-to-vault requests enter Go; all decoding enters
  Rust only after Go ownership validation.
DATA_Q4v8Nc2L_END
