# Custom KeePass Web v1 Implementation Plan

**Status:** Proposed

**Source of truth:** `ARCHITECTURE.md`

**Execution skills:** `$implement-go-rust-backend`, `$implement-htmx-frontend`

## Goal

Replace the current Actix/React KeePass viewer with a production-ready, single-user, multi-vault application consisting of:

- A server-rendered Go application using HTMX and Tailwind CSS
- Supabase Auth, PostgreSQL, and private Storage
- An isolated Rust worker that retains only KDBX decoding behavior
- Application-level encryption for decoded records and attachments
- Editable Supabase-backed entries while original KDBX files remain immutable snapshots

The replacement must be delivered without deleting the current implementation until decoder parity and end-to-end acceptance tests pass.

## Global Acceptance Criteria

Version 1 is complete only when all of the following are demonstrated:

1. The allow-listed user can sign in with Supabase email/password and receives only an opaque, secure application session cookie.
2. The user can import multiple password- or password/keyfile-protected KDBX vaults and distinguish them by encrypted display name.
3. Every original KDBX file is stored unchanged in a private Storage bucket.
4. Decoded titles, usernames, URLs, passwords, notes, tags, custom fields, group names, attachment names, and attachment bytes are never persisted as plaintext.
5. The browser communicates only with Go; no Supabase service credential, database credential, refresh token, or application key reaches client code.
6. Groups and entries can be browsed; entries can be created, edited, moved between existing groups, and soft-deleted.
7. Concurrent entry edits use revisions and return `409 Conflict` rather than silently overwriting newer data.
8. Search decrypts only the authorized selected vault and filters bounded results in Go. There are no plaintext search columns, blind indexes, or database-side decryption.
9. Imported attachments can be downloaded through an authorized Go handler and decrypted as a bounded stream.
10. Import failure, worker timeout/crash, duplicate submission, database failure, Storage failure, and process restart leave no active partial vault and converge through compensation/recovery.
11. Login, import, browsing, search, reveal/copy, CRUD, attachment download, conflicts, and destructive confirmation pass Playwright tests at mobile, tablet, and desktop viewports.
12. Go, Rust, SQL migrations, frontend assets, Docker image, and CI pass their full verification gates.
13. Security inspection finds no secrets in logs, errors, URLs, HTML outside explicit reveal responses, database plaintext, Storage metadata, or abandoned temporary files.
14. The old Actix/React/keyring implementation is removed only after all parity and replacement gates pass.
15. Application-key rotation completes through a resumable re-encryption job, supports mixed key versions during migration, and refuses to retire an old key while any live record or encrypted attachment still uses it.

## Scope

### Included

- One administratively provisioned, allow-listed account
- Multiple independent vaults
- Immutable KDBX snapshots
- Password and optional keyfile import
- Groups, entries, custom fields, protected values, tags, icons, and attachments
- Entry CRUD, movement, soft deletion, revisions, and change events
- Go-side decrypted search
- Mobile-first liquid-glass UI
- Import and cleanup jobs, recovery, observability, Docker, and CI

### Deferred

- Writing/exporting the edited state back to KDBX
- Re-import/merge into an already edited vault
- Attachment creation, replacement, or deletion
- Group creation, reorganization, or deletion
- Multiple users, sharing, invitations, offline clients, browser extensions
- Blind-index, cache-based, or plaintext database search

## Architectural Invariants

- Go owns HTTP, templates, authentication, authorization, sessions, CSRF, encryption, database/Storage access, jobs, and worker orchestration.
- Rust owns only KDBX validation, decoding, normalization, and restricted attachment staging.
- The Rust worker has no HTTP server, Supabase client, authorization logic, or database access.
- Passwords/keyfiles travel to the worker through stdin or restricted inherited descriptors, never arguments or environment variables.
- Supabase service-role access is server-only and always preceded by Go `owner_id` authorization because it bypasses RLS.
- PostgreSQL and Storage are treated as non-atomic systems using explicit states, idempotent keys, compensation, and recovery.
- All schema, policy, function, bucket-policy, and grant changes are versioned under `supabase/migrations/`.
- Old code remains available until the replacement passes parity and cutover gates.

## Dependency Waves

| Wave | Work | Depends on |
| --- | --- | --- |
| 1 | Phase 0: contracts, fixtures, scaffolding | Architecture |
| 2 | Phase 1: Supabase foundation; Phase 2: Rust decoder | Phase 0 |
| 3 | Phase 3: Go security/application foundation | Phases 0–1 |
| 4 | Phase 4: import pipeline | Phases 1–3 |
| 5 | Phase 5: vault domain and HTTP operations | Phases 1, 3–4 |
| 6 | Phase 6: HTMX/Tailwind frontend | Phases 3–5 |
| 7 | Phase 7: hardening, E2E, delivery | Phases 1–6 |
| 8 | Phase 8: cutover and legacy removal | Phase 7 |

Phases 1 and 2 may proceed in parallel after Phase 0 freezes the database and manifest contracts. Later phases are sequential because each consumes the preceding executable boundary.

## Phase 0 — Contracts, Fixtures, and Walking Skeleton

### Goal

Create reproducible build/test foundations and lock the cross-language contracts before production behavior is written.

### Tasks

1. Inventory current KDBX behavior in `src/keepass/` and document the exact fields, UUIDs, ordering, protected values, icons, and attachment behavior that the new worker must preserve.
2. Create generated or sanitized fixtures under `tests/fixtures/kdbx/` covering password-only, password+keyfile, nested groups, Unicode, custom/protected fields, icons, and attachments. Do not commit personal vaults.
3. Add golden decoded manifests produced from the approved fixtures and validate that no fixture contains real credentials.
4. Define `contracts/decoder-manifest-v1.schema.json`, including schema version, vault metadata, group hierarchy, entry fields, protection metadata, attachment references, and limits.
5. Scaffold:
   - `go.mod`, `cmd/web/`, and `internal/`
   - `decoder/Cargo.toml` and `decoder/src/`
   - `templates/`, `web/styles/`, and `web/static/`
   - `supabase/config.toml`, `supabase/migrations/`, and local seed/test support
6. Add a Go health endpoint, a Rust worker protocol smoke command, a minimal Tailwind asset build, and CI jobs that build the old and new paths side-by-side.
7. Add documented local commands to the package/build configuration without changing production routing.
8. Add `scripts/check-go-format.sh`; resolve tracked `.go` files, run `gofmt -l`, and fail without mutating source when any file needs formatting.

### TDD and Verification

- Write contract validation and fixture-integrity tests before the worker implementation.
- Run `go test ./internal/contract/... -run 'TestDecoderManifest|TestFixtureIntegrity'` and record the expected RED failure before adding the contract implementation; rerun to GREEN afterward.
- Verify the new Go server and Rust worker build independently.
- Verify the frontend build scans Go templates and produces a deterministic CSS asset.

### Acceptance Criteria

- [ ] Sanitized fixtures cover every decoder behavior required by the architecture.
- [ ] Manifest v1 is machine-validated from both Go and Rust tests.
- [ ] The walking skeleton builds without replacing the existing application.
- [ ] CI executes Go, Rust, frontend, and legacy smoke builds.
- [ ] No production secret or personal KDBX data exists in fixtures or Git history introduced by this work.

## Phase 1 — Supabase Schema, RLS, and Private Storage

### Goal

Create the complete reproducible persistence and authorization foundation.

### Target Files

- `supabase/migrations/*_create_application_schema.sql`
- `supabase/migrations/*_create_rls_policies.sql`
- `supabase/migrations/*_configure_private_storage.sql`
- `supabase/seed.sql`
- `internal/store/postgres/` integration tests

### Tasks

1. Create enums/check constraints for vault, import, and job states.
2. Create `vaults`, `vault_imports`, `groups`, `entries`, `attachments`, `change_events`, `app_sessions`, and `jobs`.
3. Add primary/foreign keys, uniqueness, ownership, soft-delete, revision, timestamp, size, status-transition, ordering, and encryption-key-version constraints. All encrypted records and attachments must expose their current application key version without exposing plaintext.
4. Index owner lookups, hierarchy traversal, group entries, active jobs, import checksums, revisions, and RLS policy columns.
5. Enable RLS on exposed tables with `authenticated` ownership policies using both `using` and `with check`; grant no anonymous access.
6. Create private KDBX and attachment buckets with restricted MIME/size policies and no public read path.
7. Add local test data for a synthetic user without embedding production credentials.
8. Add migration/RLS tests for owner access, cross-owner denial, invalid state transitions, cascades, soft deletion, and stale revisions.

### TDD and Verification

```bash
supabase db reset
go test ./internal/store/...
```

Run ownership tests both as an authenticated user and through the server’s privileged adapter assumptions.

### Acceptance Criteria

- [ ] A clean local Supabase environment is reproducible from migrations and seed data.
- [ ] Every architecture table, constraint, index, RLS policy, and private bucket exists.
- [ ] Cross-owner access fails; service-role tests prove Go authorization is still required.
- [ ] Invalid revisions and lifecycle transitions fail at the database boundary.
- [ ] No decoded field requires a plaintext column.

## Phase 2 — Rust KDBX Decoder Worker

### Goal

Extract and prove a minimal, deterministic, security-bounded decoder without carrying forward the old web application.

### Target Files

- `decoder/src/main.rs`
- `decoder/src/protocol.rs`
- `decoder/src/decode.rs`
- `decoder/src/normalize.rs`
- `decoder/src/attachments.rs`
- `decoder/src/error.rs`
- `decoder/src/limits.rs`
- `decoder/tests/`

### Tasks

1. Pin the existing compatible `keepass-rs` dependency and review required features and licensing.
2. Implement the versioned input protocol. Accept encrypted KDBX location through a trusted path/descriptor and credentials only through stdin or restricted descriptors.
3. Reuse only database-key construction, `Database::open`, traversal, protected/custom field extraction, UUID preservation, icons, tags, and attachment extraction.
4. Normalize data deterministically to manifest v1; keep stdout protocol-only and stderr redacted.
5. Stage attachments under generated names in a `0700` job directory with `0600` files; never use original filenames as paths.
6. Enforce input, output, entry, group, field, depth, attachment, memory, and execution limits.
7. Define stable redacted errors for bad credentials, malformed/unsupported KDBX, limits, I/O, and internal failures.
8. Add golden fixture tests and Go/Rust contract compatibility tests.

### TDD and Verification

```bash
cargo fmt --manifest-path decoder/Cargo.toml --check
cargo clippy --manifest-path decoder/Cargo.toml --all-targets --all-features -- -D warnings
cargo test --manifest-path decoder/Cargo.toml
cargo build --manifest-path decoder/Cargo.toml --release
```

### Acceptance Criteria

- [ ] All sanitized fixtures decode to deterministic valid manifest v1 output.
- [ ] Wrong credentials and malformed/oversized inputs return stable redacted errors.
- [ ] No credential appears in arguments, environment, stdout diagnostics, stderr, or staging filenames.
- [ ] Staged attachments are bounded, permission-restricted, and traversal-safe.
- [ ] The worker contains no Actix, Supabase, database, session, keyring, or HTTP-fetching code.
- [ ] Go rejects invalid or unsupported manifests even when the worker exits successfully.

## Phase 3 — Go Security and Application Foundation

### Goal

Implement the secure modular Go foundation consumed by every user-facing feature.

### Target Files

- `cmd/web/main.go`
- `internal/config/`
- `internal/auth/`
- `internal/crypto/`
- `internal/crypto/rotation.go`
- `internal/store/`
- `internal/jobs/`
- `internal/jobs/reencrypt.go`
- `internal/web/middleware/`

### Tasks

1. Implement validated configuration for Supabase URLs/credentials, allow-listed email, versioned application keys, cookie settings, worker path, temporary directory, and resource limits.
2. Implement Supabase email/password login with public signup disabled and allow-list enforcement.
3. Create opaque sessions: random browser token, hashed database token, encrypted refresh material, rotation, expiry, revocation, and logout.
4. Add authentication, ownership, CSRF, rate-limit, secure-header, correlation-ID, recovery, request-size, and no-store middleware.
5. Implement versioned AES-256-GCM record envelopes with unique nonces and AAD binding owner, vault, type, and record ID.
6. Implement chunked authenticated attachment encryption with unique per-chunk nonces and authenticated sequence metadata.
7. Implement mixed-version reads and a resumable, owner-scoped re-encryption job for database envelopes and encrypted attachments. Process bounded batches, write new objects idempotently, compensate failed object replacement, persist progress/retries, and resume after interruption.
8. Add an old-key retirement gate that queries every live encrypted record/object version and refuses retirement until re-encryption is complete and verified.
9. Implement PostgreSQL pooling/repositories and private Storage adapters with bounded operations, typed errors, and service-role ownership preconditions.
10. Implement persisted job claim/lease/retry primitives and startup reconciliation.
11. Add liveness and Supabase readiness endpoints without exposing secret configuration.

### TDD and Verification

```bash
./scripts/check-go-format.sh
go test -race ./...
go vet ./...
go build ./cmd/web
```

Tests must cover token/session rotation, CSRF, cross-owner access, mixed key versions, nonce uniqueness, AAD mismatch, tampering, cancellation, pool failures, job leasing, interrupted/retried re-encryption, attachment replacement compensation, and refusal to retire an in-use key.

### Acceptance Criteria

- [ ] Only the allow-listed account can establish an application session.
- [ ] Browser cookies are opaque, secure, HTTP-only, appropriately same-site, rotated, and revocable.
- [ ] Ciphertext cannot be moved between owner/vault/record contexts or modified undetected.
- [ ] Attachment encryption processes bounded chunks and detects reordering/tampering.
- [ ] A mixed-version dataset remains readable while a resumable job migrates records and attachments to the new key.
- [ ] Old-key retirement fails until database and Storage verification proves no live envelope uses it.
- [ ] Service credentials remain inside server adapters and never appear in rendered/config endpoints.
- [ ] Job and repository operations honor context deadlines and release all resources.

## Phase 4 — Transactional Import and Recovery Pipeline

### Goal

Import a KDBX into encrypted Supabase state with atomic user-visible activation and deterministic recovery.

### Target Files

- `internal/import/handler.go`
- `internal/import/service.go`
- `internal/import/worker.go`
- `internal/import/manifest.go`
- `internal/import/compensation.go`
- `internal/jobs/import.go`
- `internal/jobs/cleanup.go`
- `internal/import/worker_isolation_test.go`
- `templates/import/`

### Tasks

1. Add authenticated/CSRF-protected multipart import with explicit KDBX/keyfile limits.
2. Stream KDBX to a random `0600` temporary file while computing SHA-256; never retain the master password/keyfile.
3. Persist an idempotent import job before decoding and expose bounded status polling.
4. Execute the Rust worker with deadline/resource/output limits, drain pipes safely, terminate/reap on failure, and validate the complete manifest.
5. Construct an explicit minimal worker environment that excludes Supabase credentials, database credentials, application encryption keys, session secrets, and unrelated inherited variables. Use an absolute worker path, a restricted working directory, and close all unrelated file descriptors.
6. Detect duplicate checksums. Require explicit confirmation and import duplicates only as separate vaults.
7. Generate application IDs, encrypt group/entry payloads and attachment metadata/bytes, and use opaque idempotent Storage keys.
8. Store the original KDBX unchanged; upload encrypted attachments.
9. Insert database records in `importing` state and activate the vault/import only after all required objects exist.
10. Persist compensation jobs for every created object and reconcile abandoned imports/jobs on startup.
11. Return safe error categories and correlation IDs; never expose raw decoder/Supabase errors.

### Failure-First Tests

- Wrong password/keyfile
- Malformed, truncated, unsupported, oversized, or deeply nested KDBX
- Worker timeout, crash, invalid manifest, excess output, partial staging
- Duplicate request and duplicate checksum
- Storage failure before/after some objects
- Database failure before/after object upload
- Process restart during each lifecycle state
- Cleanup retry and idempotent re-execution
- Worker helper inspection proving forbidden environment variables and unrelated descriptors are absent

Run the import behavior in explicit RED→GREEN slices:

```bash
go test ./internal/import/... -run TestImport
go test ./internal/jobs/... -run 'TestImport|TestCleanup|TestReconcile'
go test -race ./internal/import/... ./internal/jobs/...
```

For each slice, capture the expected failing assertion before implementation, then rerun the same command after the minimal implementation.

### Acceptance Criteria

- [ ] A valid KDBX becomes visible only after all encrypted records and required objects are durable.
- [ ] Failed imports never appear as active vaults.
- [ ] Every partial external write has a persisted, retryable compensation path.
- [ ] Restart reconciliation converges interrupted states without duplicating records or objects.
- [ ] Worker-isolation tests prove server credentials, application keys, unrelated environment variables, and unrelated descriptors are unavailable to Rust.
- [ ] Database/Storage/log/temp inspection finds no persisted decoded plaintext or import credentials.

## Phase 5 — Vault Domain, CRUD, Search, and Attachments

### Goal

Deliver the complete server-side vault experience independently of visual presentation.

### Target Files

- `internal/vault/model.go`
- `internal/vault/service.go`
- `internal/vault/repository.go`
- `internal/web/handlers/vaults.go`
- `internal/web/handlers/entries.go`
- `internal/web/handlers/attachments.go`
- `internal/web/handlers/jobs.go`

### Tasks

1. Implement owner-scoped vault listing, group hierarchy, group entries, entry details, and encrypted display-name updates.
2. Implement entry creation, editing, movement between existing groups, and soft deletion.
3. Require revisions on mutation; update the entry and append `change_events` in one transaction.
4. Return typed `409 Conflict` state for stale revisions.
5. Implement bounded selected-vault search by loading authorized ciphertext, decrypting in Go, filtering configured fields, and bounding terms/results.
6. Implement explicit protected-field reveal with authorization, rate limiting, audit category, and `Cache-Control: no-store`.
7. Implement attachment download by authorizing metadata, streaming from private Storage, authenticating/decrypting chunks, and supplying a safe filename/content type.
8. Implement vault deletion confirmation and a tracked cleanup job for database records, encrypted attachments, and KDBX snapshots.
9. Keep attachment mutation, group mutation, KDBX export, and existing-vault re-import unavailable.

### TDD and Verification

Test all operations for correct owner, missing session, cross-owner ID, deleted record, stale revision, malformed input, tampered ciphertext, missing Storage object, and cancellation.

Run each handler/service behavior RED→GREEN with:

```bash
go test ./internal/vault/... -run 'TestVault|TestEntry|TestSearch|TestAttachment'
go test ./internal/web/handlers/... -run 'TestVault|TestEntry|TestReveal|TestAttachment'
go test -race ./internal/vault/... ./internal/web/handlers/...
```

The focused test must fail for the expected missing behavior before production code is added and pass before the next behavior begins.

### Acceptance Criteria

- [ ] Every read/mutation is constrained by authenticated `owner_id`.
- [ ] Entry CRUD/move/delete produces correct revisions and change events.
- [ ] Stale edits preserve the latest data and return a conflict.
- [ ] Search returns correct bounded results without database plaintext search data.
- [ ] Reveal responses are explicit, uncached, and absent from initial/list HTML.
- [ ] Attachment download is authorized, integrity-checked, bounded, and leaves no plaintext artifact.

## Phase 6 — Mobile-First HTMX and Tailwind Frontend

### Goal

Deliver the complete accessible, mobile-first liquid-glass user experience using server-rendered HTML.

### Execution Requirement

Use `$implement-htmx-frontend` for all work in this phase.

### Target Files

- `templates/layouts/`
- `templates/pages/`
- `templates/fragments/`
- `templates/components/`
- `web/styles/app.css`
- `web/static/app.js`
- `tests/e2e/`
- `playwright.config.*`

### Tasks

1. Define shared liquid-glass tokens, solid fallbacks, type scale, spacing, radii, borders, shadows, focus, reduced-motion, and safe-area behavior.
2. Build full-page and fragment contracts for login, vault dashboard, import/status, vault browser, entry detail, create/edit, search, attachment download, settings, conflicts, errors, and destructive confirmation.
3. Use semantic ordinary links/forms as the baseline; enhance targeted navigation, submissions, polling, history, and swaps with HTMX.
4. Implement mobile drill-down navigation first at 320–375 px; progressively enhance to tablet and desktop multi-panel layouts.
5. Use minimal vanilla JavaScript only for clipboard, timed reveal/conceal, dialog focus, and small transitions.
6. Add visible loading/disabled, empty, validation, conflict, expired-session, permission, offline/request-failure, and retry states.
7. Ensure 44×44 CSS-pixel targets, keyboard operation, ordered headings, labels, landmarks, focus restoration, contrast, wrapping/truncation, and no horizontal overflow.
8. Ensure secrets are not embedded in initial HTML or client persistence and are removed from the DOM after concealment.

### Browser Verification

```bash
npm ci
npm run build
npx playwright test
```

Cover login, multiple imports, navigation/history, search, reveal/copy/conceal, entry CRUD/move/delete, attachment download, stale conflict, session expiry, failure states, and destructive confirmation.

### Acceptance Criteria

- [ ] All version-one screens and states are implemented as full-page and HTMX-compatible responses.
- [ ] Critical flows pass at narrow mobile, tablet, and desktop viewports.
- [ ] Normal forms/links preserve understandable progressive behavior.
- [ ] Automated and manual checks verify keyboard, focus, touch targets, contrast, reduced motion, long content, and empty/error states.
- [ ] No secret leaks through initial HTML, URLs, browser storage, unintended fragments, or caches.
- [ ] React and Ant Design are not introduced.

## Phase 7 — Hardening, Observability, Delivery, and Release Candidate

### Goal

Prove the integrated system under failure and package it as a reproducible, least-privilege release candidate.

### Target Files

- `internal/observability/`
- `internal/jobs/reconcile.go`
- `internal/web/middleware/security.go`
- `internal/web/middleware/limits.go`
- `scripts/verify-all.sh`
- `.github/workflows/test.yml`
- `.github/workflows/container-image.yml`
- `Dockerfile`
- `docker-compose.yml`
- `docs/operations.md`
- `docs/security.md`

### Tasks

1. Add structured logging with correlation/job IDs and allow-listed redacted fields.
2. Verify rate limits, CSP, HSTS where TLS termination permits, frame denial, MIME sniffing protection, referrer policy, cache policy, and secure cookies.
3. Add startup reconciliation, cleanup retries/backoff, liveness/readiness, graceful shutdown, worker reaping, and resource-limit observability.
4. Add integrated tests using local Supabase, the real Rust worker, sanitized KDBX fixtures, and private Storage.
5. Add a multi-stage Dockerfile that builds Tailwind, Rust, and Go and produces a minimal non-root runtime with only a bounded writable temp location.
6. Update CI to run migration reset/tests, Go race/vet/build, Rust fmt/Clippy/test/release, frontend build/Playwright, Docker build, dependency/license checks, and configured secret scanning.
7. Run a threat-focused review of auth/session, service-role use, object access, crypto envelopes/nonces, temporary data, worker IPC, error/log leakage, and destructive actions.
8. Document runtime configuration, key rotation, backup/restore expectations, recovery, and local development without publishing secrets.

### Task Dependencies and Checkpoints

1. Complete middleware/observability and recovery tests before changing delivery files.
2. Add `scripts/verify-all.sh` only as an orchestrator for already passing component commands; it must fail fast and never hide skipped checks.
3. Build the release-candidate image before changing the default Compose/runtime entrypoint.
4. Record the pre-cutover Git SHA, legacy image digest, migration status, and rollback configuration in `docs/operations.md`.
5. Run the full verification script and failure-injection suite against the exact recorded release-candidate image.

### Acceptance Criteria

- [ ] The release candidate passes all language, migration, browser, integration, Docker, and security gates.
- [ ] Failure injection proves cleanup/recovery for every import lifecycle state.
- [ ] The runtime is non-root, contains no build toolchain or source secrets, and has only the required network/temp access.
- [ ] Logs, traces, responses, database, Storage metadata, image layers, and temporary paths pass secret-leak inspection.
- [ ] Health checks distinguish process liveness from Supabase readiness.
- [ ] A rollback can restore the last legacy release without requiring data written by the new app.

## Phase 8 — Parity Gate, Cutover, and Legacy Removal

### Goal

Switch to the new application and remove obsolete code only after objective parity and release acceptance.

### Initial Entry Gate

Do not begin deletion until:

- Rust decoder golden/contract tests pass for every fixture.
- All non-cutover global acceptance criteria and Phase 7 gates pass.
- A release-candidate image completes the full Playwright suite.
- Backup and rollback procedures are exercised and the pre-cutover Git SHA/image digest are recorded.

### Target Files and Inventory

- `artifacts/parity-report.json`
- `migration/legacy-removal-manifest.txt`
- `README.md`
- `AGENTS.md`
- `Dockerfile`
- `docker-compose.yml`
- `package.json`
- `package-lock.json`
- `LICENSE`
- `THIRD_PARTY_NOTICES.md` when third-party notices require a separate file
- Legacy candidates: `src/`, `js/`, `public/`, `seccomp/`, root `Cargo.toml`, root `Cargo.lock`, `config.yml`, `tests/config.test.yml`, obsolete screenshots/docs, and superseded test files

### Tasks

1. Produce `artifacts/parity-report.json` comparing old/new group and entry UUID sets, parent hierarchy, ordering, visible/protected/custom values, tags, icons, attachment filenames, sizes, and byte checksums for every fixture.
2. Generate `migration/legacy-removal-manifest.txt` from the repository inventory. For every candidate, record `remove`, `retain`, or `replace`, its replacement, license impact, and the verification that covers it.

### Hard Cutover Checkpoint

Cutover and deletion are forbidden until an automated parity command exits successfully and `artifacts/parity-report.json` contains zero mismatches for every required field and fixture. A mismatch stops Phase 8, preserves the legacy runtime and files, and routes work back to the owning phase. Human inspection cannot waive a machine-detected mismatch without an approved architecture change.

3. After the hard checkpoint passes, switch the runtime entrypoint and deployment configuration to the exact verified Go/Rust release-candidate image. Verify health, login, and one sanitized import before deleting legacy files.
4. Delete only entries marked `remove` in the reviewed manifest: old Actix server/routes, auth/database backends, sessions, keyring/cache code, old root Rust manifests/config, and superseded tests.
5. Remove React/Browserify sources, generated assets, obsolete icons, npm dependencies/scripts, and seccomp/keyring deployment requirements only where the manifest identifies their replacements.
6. Keep `decoder/`, sanitized fixtures, GPL license, and required third-party notices.
7. Rewrite `README.md`, `AGENTS.md`, Docker/Compose configuration, and development commands for the new application.
8. Re-run every verification gate from a clean checkout and clean local Supabase database.
9. Compare the post-removal repository to the manifest; fail if an unreviewed legacy file remains or a retained/replacement file is missing.

### Acceptance Criteria

- [ ] The production entrypoint uses only the Go app and Rust decoder worker.
- [ ] Machine-readable parity contains zero mismatches before any cutover/deletion commit.
- [ ] A failed parity run leaves the legacy runtime and all deletion candidates untouched.
- [ ] No old Actix, React, Browserify, kernel-keyring, or obsolete backend code/dependency remains.
- [ ] Required license and notice files remain accurate.
- [ ] Clean build, migration, integration, browser, and Docker verification passes after deletion.
- [ ] Rollback points to a known pre-cutover release; no destructive rollback command is embedded in normal deployment.

## Verification Command Matrix

Commands must be wired into project scripts/CI during Phase 0 and kept current:

```bash
# Go
./scripts/check-go-format.sh
go test -race ./...
go vet ./...
go build ./cmd/web

# Rust
cargo fmt --manifest-path decoder/Cargo.toml --check
cargo clippy --manifest-path decoder/Cargo.toml --all-targets --all-features -- -D warnings
cargo test --manifest-path decoder/Cargo.toml
cargo build --manifest-path decoder/Cargo.toml --release

# Supabase
supabase db reset
go test ./internal/store/...

# Frontend/E2E
npm ci
npm run build
npx playwright test

# Packaging
docker build -t keepass-web:local .
```

No phase may claim a command passed unless it was run successfully in the current source state. Environment-dependent tests must report the exact missing dependency rather than being silently skipped.

## Risk Register and Mitigations

| Risk | Mitigation / rollback |
| --- | --- |
| Decoder behavior regresses during extraction | Freeze sanitized golden fixtures and keep old decoder until parity gate |
| Plaintext secrets leak during import/search | Application encryption tests, bounded secret lifetime, no-store/log policies, inspection gate |
| Storage succeeds while PostgreSQL fails | Explicit importing states, idempotent object keys, persisted compensation jobs |
| Service-role bypass defeats RLS | Mandatory Go owner authorization and cross-owner integration tests |
| Worker hangs or floods output | Deadline, process reaping, bounded stdout/stderr, manifest and resource limits |
| Migration damages shared environment | Local reset/tests, reviewed immutable migrations, coordinated remote application |
| Crypto key rotation makes data unreadable | Versioned envelopes, retain old read keys, re-encrypt before retiring keys |
| Worker inherits Go server credentials | Minimal explicit environment, closed unrelated descriptors, restricted working directory, isolation test |
| Mobile glass UI harms readability/performance | Mobile-first budget, limited blur surfaces, solid fallbacks, contrast/reduced-motion checks |
| Legacy removal happens too early | Machine-readable zero-mismatch parity checkpoint, reviewed removal manifest, and pre-cutover rollback point |
| Scope expands into synchronization/export | Enforce deferred-scope list and require a new architecture decision |

## Definition of Done

The implementation is done when every phase acceptance criterion and every global acceptance criterion is checked with current evidence; all verification commands pass from a clean checkout; the release image completes the real Rust/Supabase/Playwright path; secret-leak and failure-recovery inspections pass; documentation and licenses match the shipped system; and legacy code has been removed only after the parity gate.

If any required criterion is incomplete, the plan remains in progress. Deferred items stay deferred rather than being implemented implicitly.
