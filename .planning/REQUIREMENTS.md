# Requirements: KeePass4Web

**Defined:** 2026-08-02
**Core Value:** A signed-in user can upload, select, and securely open two private KeePass vaults through the HTMX interface, while cross-user access is denied.

## v1 Requirements

### Baseline & Operations

- [ ] **FOUND-01**: A developer can run the captured baseline tests with the supported Go, Rust, and Supabase tool versions.
- [x] **FOUND-02**: An operator can configure development, staging, and production from ignored Go, Rust, and Supabase configuration templates without committing a real secret.
- [x] **FOUND-03**: An operator can deploy a topology in which Go is the only public service and Rust is reachable only on its private network.
- [x] **FOUND-04**: A developer can verify that request bodies, authorization headers, passwords, key-file bytes, tokens, decoded data, and protected values are redacted from logs, traces, errors, and sessions.

### Authentication & Private Vaults

- [ ] **AUTH-01**: A person can establish and end a Supabase-authenticated session for the personal account flow.
- [ ] **VAULT-01**: A signed-in owner can upload an encrypted `.kdbx` file with a display name to private Storage.
- [ ] **VAULT-02**: A signed-in owner can retain and select at least two of their own uploaded vaults.
- [ ] **VAULT-03**: A signed-in owner can delete a vault they own and its corresponding private object.
- [ ] **VAULT-04**: A signed-in owner can access only vault metadata whose `owner_id` matches their authenticated identity.
- [ ] **VAULT-05**: A signed-in owner can access only private `.kdbx` objects whose object path begins with their authenticated user ID.

### Private Rust KeePass Service

- [ ] **RUST-01**: An authorized internal Go caller can unlock an owned fixture vault through Rust using the existing `keepass-rs` decoding path and receive an opaque active-vault handle.
- [ ] **RUST-02**: An authorized internal caller can use its active-vault handle to retrieve the permitted group tree, entries, entry detail, and search results.
- [ ] **RUST-03**: An authorized internal caller can request one protected field from one permitted entry only when explicitly revealing it.
- [ ] **RUST-04**: Public-origin traffic cannot reach or use the Rust KeePass service.
- [ ] **RUST-05**: A handle cannot be replayed for a different user or vault, including when a caller supplies a forged ownership claim.
- [ ] **RUST-06**: Closing, expiring, logging out of, or failing authorization for an active vault clears its encrypted cache and key material.
- [ ] **RUST-07**: Malformed requests, unauthorized requests, and decode failures return safe errors without vault data or request secrets.

### Authorized Go Application

- [ ] **GO-01**: Go validates the Supabase session on every protected request before serving vault data or actions.
- [ ] **GO-02**: Go verifies vault ownership before retrieving a Storage object or calling Rust for that vault.
- [ ] **GO-03**: Go forwards unlock password and optional key-file data only transiently to Rust and never persists either.
- [ ] **GO-04**: The browser session contains only an opaque active-vault handle, never decoded vault data, protected values, passwords, key files, bearer tokens, or Rust credentials.
- [ ] **GO-05**: Closing a vault or ending a session through Go causes the corresponding Rust active-vault state to be cleaned up.
- [ ] **GO-06**: Go presents generic user-safe errors and keeps service-role credentials and other private configuration out of rendered pages and browser assets.

### HTMX Vault Interface

- [ ] **UI-01**: A signed-in user can complete critical vault actions with ordinary server-rendered HTML forms when JavaScript is unavailable.
- [ ] **UI-02**: A signed-in user can use HTMX-enhanced pages and fragments to upload, select, and unlock an owned vault.
- [ ] **UI-03**: An unlocked user can navigate groups, browse entries, inspect safe entry detail, and search within the active vault.
- [ ] **UI-04**: An unlocked user can explicitly reveal a permitted protected field without that value appearing in page source or browser storage before the request.
- [ ] **UI-05**: A user can close an active vault, and expiry or logout returns the interface to a state that cannot access the prior vault.
- [ ] **UI-06**: Vault mutations reject missing or invalid CSRF protection, and session cookies are Secure, HttpOnly, and SameSite-protected.

### Verification & Release Readiness

- [ ] **VER-01**: A developer can run Go tests covering session validation, ownership checks, handlers, Rust-client behavior, and log redaction.
- [ ] **VER-02**: A developer can run Rust contract and cache-cleanup tests covering authorization, malformed requests, close, and expiry.
- [ ] **VER-03**: A developer can run browser tests proving sign-in, two-vault upload and selection, unlock, browse, search, protected reveal, close, expiry, logout, and account isolation.
- [ ] **VER-04**: An operator can validate RLS and Storage policies in a non-production Supabase project and inspect logs, HTML, and assets for secret exposure.
- [ ] **VER-05**: An operator can follow deployment, configuration, backup/restore, rollback, and account-recovery guidance without bypassing the private-Rust boundary.

## v2 Requirements

(None planned. Deferred product capabilities are explicitly listed below.)

## Out of Scope

| Feature | Reason |
|---------|--------|
| Vault sharing | v1 is designed for a single owner and must preserve strict private ownership. |
| Organizations and invitations | Collaborative account management is outside the personal-vault model. |
| Concurrent vault editing and conflict resolution | v1 delivers secure read-only access, not editing workflows. |
| Vault synchronization | Storage of original encrypted objects is sufficient for v1; sync policy is deferred. |
| KeePass decoding in Go or JavaScript | Existing Rust `keepass-rs` decoding remains the sole decoder. |

## Traceability

| Requirement | Phase | Status |
|-------------|-------|--------|
| FOUND-01 | Phase 1 | Pending |
| FOUND-02 | Phase 1 | Complete |
| FOUND-03 | Phase 1 | Complete |
| FOUND-04 | Phase 1 | Complete |
| AUTH-01 | Phase 2 | Pending |
| VAULT-01 | Phase 2 | Pending |
| VAULT-02 | Phase 2 | Pending |
| VAULT-03 | Phase 2 | Pending |
| VAULT-04 | Phase 2 | Pending |
| VAULT-05 | Phase 2 | Pending |
| RUST-01 | Phase 3 | Pending |
| RUST-02 | Phase 3 | Pending |
| RUST-03 | Phase 3 | Pending |
| RUST-04 | Phase 3 | Pending |
| RUST-05 | Phase 3 | Pending |
| RUST-06 | Phase 3 | Pending |
| RUST-07 | Phase 3 | Pending |
| GO-01 | Phase 4 | Pending |
| GO-02 | Phase 4 | Pending |
| GO-03 | Phase 4 | Pending |
| GO-04 | Phase 4 | Pending |
| GO-05 | Phase 4 | Pending |
| GO-06 | Phase 4 | Pending |
| UI-01 | Phase 5 | Pending |
| UI-02 | Phase 5 | Pending |
| UI-03 | Phase 5 | Pending |
| UI-04 | Phase 5 | Pending |
| UI-05 | Phase 5 | Pending |
| UI-06 | Phase 5 | Pending |
| VER-01 | Phase 6 | Pending |
| VER-02 | Phase 6 | Pending |
| VER-03 | Phase 6 | Pending |
| VER-04 | Phase 6 | Pending |
| VER-05 | Phase 6 | Pending |

**Coverage:**

- v1 requirements: 34 total
- Mapped to phases: 34
- Unmapped: 0 ✓

---
*Requirements defined: 2026-08-02*
*Last updated: 2026-08-02 after approved workflow ingest and initial roadmap*
