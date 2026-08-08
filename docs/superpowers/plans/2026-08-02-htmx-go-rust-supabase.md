# HTMX, Go, Rust, and Supabase Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` (recommended) or `superpowers:executing-plans` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

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

## Global Constraints

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

---

### Task 1: Establish repository layout and safe configuration

**Files:**
- Create: `go.mod`, `cmd/web/main.go`, `internal/config/config.go`
- Create: `.env.example`, `rust-service/.env.example`
- Modify: `.gitignore`, `docker-compose.yml`, `.github/workflows/test.yml`

**Consumes:** the existing Cargo application and Docker Compose deployment.

**Produces:** a Go module, documented non-secret configuration contract, and a
development topology that exposes Go only while Rust is reachable by service
name on the internal Compose network.

- [ ] **Step 1: Add failing Go configuration tests**

Create `internal/config/config_test.go` with table tests that require
`APP_SESSION_SECRET`, `SUPABASE_URL`, `SUPABASE_ANON_KEY`,
`SUPABASE_JWT_ISSUER`, `RUST_SERVICE_URL`, and `RUST_SERVICE_TOKEN`; assert a
missing field returns its field name and no value is included in the error.

- [ ] **Step 2: Run the test to prove the contract is absent**

Run: `go test ./internal/config`

Expected: FAIL because the Go module/config package does not exist.

- [ ] **Step 3: Implement configuration loading**

Define `type Config struct` in `internal/config/config.go`; load settings from
the environment, validate all required fields, and return errors naming only
the missing key. `cmd/web/main.go` must exit before binding a public port when
validation fails. Add example values only to `.env.example`; ignore `.env`.

- [ ] **Step 4: Add internal service topology and CI checks**

Change `docker-compose.yml` to define `web`, `rust-service`, and optional
local Supabase dependencies. Map a host port only for `web`; use `expose` for
Rust. Update GitHub Actions to run `go test ./...` in addition to existing
Rust and browser tests.

- [ ] **Step 5: Verify and commit**

Run: `go test ./... && cargo test --verbose`

Commit: `git add go.mod cmd internal .env.example rust-service/.env.example .gitignore docker-compose.yml .github/workflows/test.yml && git commit -m "chore: add Go service foundation"`

### Task 2: Add Supabase schema, private storage, and policy tests

**Files:**
- Create: `supabase/config.toml`
- Create: `supabase/migrations/0001_vaults.sql`
- Create: `supabase/tests/vault_access.sql`
- Create: `internal/vault/repository.go`, `internal/vault/repository_test.go`

**Consumes:** validated Go configuration from Task 1.

**Produces:** owner-scoped `vaults` records and private object storage with
testable database and Storage policy boundaries.

- [ ] **Step 1: Write failing repository tests**

Define `Vault { ID, OwnerID, Name, ObjectPath, CreatedAt, UpdatedAt }` and a
`Repository` interface with `List(ctx, ownerID)`, `Create(ctx, ownerID, name)`,
`Delete(ctx, ownerID, vaultID)`, and `Open(ctx, ownerID, vaultID)`. Use a fake
transport to assert every request is scoped to the authenticated owner and
that mismatched owner IDs return `ErrNotFound`.

- [ ] **Step 2: Run the failing Go test**

Run: `go test ./internal/vault`

Expected: FAIL because the package and repository do not exist.

- [ ] **Step 3: Implement migration and policies**

In `0001_vaults.sql`, create `public.vaults` with UUID primary key, `owner_id`
referencing `auth.users`, display name, unique object path, timestamps, and
upload state. Enable RLS and add owner-only select/insert/update/delete
policies. Create private bucket `vaults`; add object policies requiring the
first path segment to equal `auth.uid()`.

- [ ] **Step 4: Implement repository and policy verification**

Use the Supabase REST/Storage endpoints through the authenticated user's
token. Generate object paths as `<owner UUID>/<vault UUID>.kdbx`. In
`vault_access.sql`, use two fixture identities to prove cross-owner select,
update, delete, list, download, and upload attempts fail.

- [ ] **Step 5: Verify and commit**

Run: `go test ./internal/vault`

Run against local Supabase: `supabase test db`

Commit: `git add supabase internal/vault && git commit -m "feat: add private multi-vault storage"`

### Task 3: Extract the private Rust KeePass API

**Files:**
- Create: `src/service/mod.rs`, `src/service/auth.rs`, `src/service/routes.rs`
- Create: `tests/service_contract.rs`
- Modify: `src/main.rs`, `src/server.rs`, `src/server/route/auth.rs`
- Modify: `src/server/route/keepass.rs`, `src/keepass/db_cache.rs`, `Cargo.toml`

**Consumes:** existing `KeePass::from_backend`, `DbCache`, and KeePass route
operations; a private service credential from Task 1.

**Produces:** internal `unlock`, `groups`, `entries`, `entry`, `reveal`,
`search`, and `close` endpoints using opaque vault handles bound to user and
vault IDs.

- [ ] **Step 1: Write Rust contract tests**

In `tests/service_contract.rs`, test: missing/invalid service credential is
401; a handle issued for `(user_a, vault_a)` cannot serve `(user_b, vault_a)`;
close invalidates the handle; expired handles fail; and error bodies do not
echo password/key-file input.

- [ ] **Step 2: Run the contract test**

Run: `cargo test --test service_contract`

Expected: FAIL because the private service API is absent.

- [ ] **Step 3: Implement service authentication and handles**

Add a constant-time service-token extractor in `src/service/auth.rs`. Define a
handle record containing random handle ID, authenticated user ID, vault ID,
encrypted cache reference, and expiry. Change `DbCache` indexing from only
user ID to `(user_id, vault_id)` and revoke the associated key/cache entry on
close or expiry.

- [ ] **Step 4: Implement routes by adapting existing operations**

Move the unlock path currently in `src/server/route/auth.rs` behind
`POST /internal/v1/vaults/{vault_id}/unlock`. Adapt the JSON handlers in
`src/server/route/keepass.rs` into private handlers that accept a handle and
explicit IDs; use existing `KeePass` methods rather than reparsing vault data.
Bind Rust only to its Compose private address.

- [ ] **Step 5: Verify and commit**

Run: `cargo test --test service_contract && cargo test --verbose`

Commit: `git add src tests/service_contract.rs Cargo.toml && git commit -m "feat: expose private Rust KeePass service"`

### Task 4: Implement Go authentication, Rust client, and ownership handlers

**Files:**
- Create: `internal/auth/session.go`, `internal/auth/session_test.go`
- Create: `internal/rustclient/client.go`, `internal/rustclient/client_test.go`
- Create: `internal/http/handlers.go`, `internal/http/handlers_test.go`
- Create: `templates/layout.html`, `templates/vaults.html`, `templates/unlock.html`
- Modify: `cmd/web/main.go`

**Consumes:** Task 2 repository and Task 3 private service contract.

**Produces:** authenticated Go routes that list/open owned vaults, send unlock
data only to Rust, and keep only an opaque Rust handle in the encrypted Go
session.

- [ ] **Step 1: Write handler and client tests**

Test that unauthenticated requests redirect to sign-in; `POST /vaults/{id}/open`
returns 404 for a vault not owned by the session user; and the Rust client sends
the configured service token plus user/vault IDs, but its errors do not include
the password or key-file bytes.

- [ ] **Step 2: Run failing Go tests**

Run: `go test ./internal/auth ./internal/rustclient ./internal/http`

Expected: FAIL because these packages do not exist.

- [ ] **Step 3: Implement session validation and the vault flow**

Verify Supabase JWT issuer/audience/signature before every protected handler.
Use `Repository.Open` with the validated `sub` claim, download the private
object, then call Rust `unlock`. Store `{user_id, vault_id, handle, expiry}`
only in a secure HttpOnly SameSite session cookie. Never include password or
key-file bytes in a redirect, template model, log, or error response.

- [ ] **Step 4: Implement safe error and logout cleanup**

Render a generic 404 for absent/unowned vaults. On close, timeout, logout, and
session invalidation, call Rust `close` best-effort and clear the Go session.

- [ ] **Step 5: Verify and commit**

Run: `go test ./...`

Commit: `git add cmd internal templates && git commit -m "feat: add Go vault gateway"`

### Task 5: Deliver the HTMX and Tailwind read-only vault interface

**Files:**
- Create: `templates/fragments/vault_list.html`, `templates/fragments/groups.html`
- Create: `templates/fragments/entries.html`, `templates/fragments/entry.html`
- Create: `templates/fragments/error.html`, `web/tailwind.css`, `web/app.css`
- Create: `package.json`, `tailwind.config.js`, `playwright.config.js`
- Modify: `internal/http/handlers.go`, `tests/e2e/main-flow.spec.js`

**Consumes:** Task 4 authenticated Go handlers and Task 3 read contract.

**Produces:** progressively enhanced server-rendered pages/fragments for
vault management, unlock, browse, search, protected reveal, close, and logout.

- [ ] **Step 1: Write browser tests before templates**

Replace the legacy React navigation assertions with tests for HTML fallback and
HTMX: sign-in, vault list, selecting one of two vaults, unlock failure,
successful group/entry browse, search, protected reveal, close, session expiry,
and a second account denied access to the first account's vault URL.

- [ ] **Step 2: Run the failing browser suite**

Run: `npm ci && npx playwright test --reporter=list`

Expected: FAIL because the Go web flow and templates do not exist.

- [ ] **Step 3: Implement templates and HTMX fragments**

Use full-page handlers for GET navigation and fragments for HTMX requests.
Each state-changing form includes Go CSRF protection. Add the HTMX script with
subresource integrity or a locally vendored pinned copy; compile Tailwind to a
static CSS file and serve it from Go. Do not restore the React/Browserify
bundle or place vault content in browser storage.

- [ ] **Step 4: Verify fallback and redaction behavior**

Run browser tests once with HTMX enabled and once against form submissions
without the HTMX script. Inspect rendered HTML and test artifacts to confirm
master passwords, key-file data, service tokens, and protected values are not
present except the intentional protected-field response in the active page.

- [ ] **Step 5: Verify and commit**

Run: `go test ./... && cargo test --verbose && npx playwright test --reporter=list`

Commit: `git add templates web package.json tailwind.config.js playwright.config.js internal/http tests/e2e && git commit -m "feat: add HTMX vault interface"`

### Task 6: Release hardening and deployment verification

**Files:**
- Create: `docs/deployment.md`, `docs/security-checklist.md`
- Modify: `docker-compose.yml`, `.github/workflows/test.yml`, `README.md`

**Consumes:** Tasks 1–5.

**Produces:** deployable private-service topology, operational documentation,
and automated quality gates.

- [ ] **Step 1: Add failing deployment assertions**

Add a CI shell check that fails if the Compose file maps a Rust host port, if
an `.env` file is tracked, or if built browser assets contain the configured
service-role key test sentinel.

- [ ] **Step 2: Implement release documents and CI gates**

Document environment variables, Supabase migration/policy deployment,
private-network requirements, account recovery, backup/restore of source
`.kdbx` files, rollback, and the two-account isolation checklist. Extend CI
to run Rust, Go, migration-policy, browser, and deployment-assertion checks.

- [ ] **Step 3: Run the full verification suite**

Run: `cargo test --verbose && go test ./... && supabase test db && npx playwright test --reporter=list`

Expected: PASS in a non-production Supabase environment with two test users.

- [ ] **Step 4: Commit**

Commit: `git add docs docker-compose.yml .github/workflows/test.yml README.md && git commit -m "docs: harden deployment workflow"`

## Self-review

- Security boundary coverage: Tasks 1, 2, 3, 4, and 6 enforce configuration,
  RLS, private networking, secret handling, and verification.
- Architecture coverage: Tasks 3–5 map directly to the Rust service, Go BFF,
  and HTMX/Tailwind frontend design.
- Scope coverage: the plan provides multi-vault ownership but contains no
  sharing, editing, or synchronization work.
- Consistency: all browser-to-vault requests enter Go; all decoding enters
  Rust only after Go ownership validation.
