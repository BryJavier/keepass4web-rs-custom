# Roadmap: KeePass4Web

## Overview

KeePass4Web moves from a safe operating baseline to owner-isolated Supabase vault storage, a private Rust KeePass service, and an authorized Go application before exposing the complete progressive HTML/HTMX vault workflow. Phase numbers use the new-project convention (1–6); they preserve the approved workflow's delivery order of Baseline and Operations (original Phase 0) through Verification and Release Readiness (original Phase 5).

## Phases

**Phase Numbering:**

- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Baseline & Operations** - Establish safe configuration, a private-service topology, and sensitive-data redaction.
- [ ] **Phase 2: Supabase Multi-Vault Foundation** - Give each authenticated owner multiple encrypted vaults protected by RLS and Storage policies.
- [ ] **Phase 3: Private Rust KeePass Service** - Expose the existing decoder and active-vault lifecycle through a trusted internal contract.
- [ ] **Phase 4: Authorized Go Application** - Authenticate every protected request, enforce ownership, and bridge Go to Rust without persisting secrets.
- [ ] **Phase 5: HTMX Vault Interface** - Deliver a secure, progressively enhanced browser workflow for private-vault access.
- [ ] **Phase 6: Verification & Release Readiness** - Prove isolation and redaction end to end, then make the approved topology operable.

## Phase Details

### Phase 1: Baseline & Operations

**Goal**: Developers and operators can build and run KeePass4Web safely without exposing secrets or public Rust access.
**Depends on**: Nothing (first phase)
**Requirements**: FOUND-01, FOUND-02, FOUND-03, FOUND-04
**Success Criteria** (what must be TRUE):

  1. A developer can run the captured baseline tests using the supported tool versions.
  2. An operator can configure development, staging, and production from ignored templates without a real secret entering source control.
  3. An operator can confirm that Go is the only public service and Rust accepts traffic only on its private network.
  4. A developer can verify that sensitive request and vault data are absent from logs, traces, errors, and sessions.

**Plans**: 1/5 plans executed

- [x] 01-01-PLAN.md
- [ ] 01-02-PLAN.md
- [ ] 01-03-PLAN.md
- [ ] 01-04-PLAN.md
- [ ] 01-05-PLAN.md

### Phase 2: Supabase Multi-Vault Foundation

**Goal**: Authenticated owners can manage multiple private encrypted vault objects, while every cross-user metadata and object access is denied.
**Depends on**: Phase 1
**Requirements**: AUTH-01, VAULT-01, VAULT-02, VAULT-03, VAULT-04, VAULT-05
**Success Criteria** (what must be TRUE):

  1. A person can establish and end the configured Supabase-authenticated session for the personal account flow.
  2. A signed-in owner can upload a named encrypted `.kdbx` vault and retain at least two of their private vaults.
  3. A signed-in owner can list, select, and delete only vaults they own.
  4. A second account cannot discover the first account's vault metadata or read or write its private Storage objects.

**Plans**: TBD

### Phase 3: Private Rust KeePass Service

**Goal**: Only authorized internal Go requests can use the existing Rust KeePass decoder to read an owner's active vault through a handle that cannot cross security boundaries.
**Depends on**: Phase 2
**Requirements**: RUST-01, RUST-02, RUST-03, RUST-04, RUST-05, RUST-06, RUST-07
**Success Criteria** (what must be TRUE):

  1. An authorized internal caller can unlock a fixture vault through the existing `keepass-rs` decoder and receive an opaque active-vault handle.
  2. An authorized internal caller can retrieve permitted groups, entries, safe entry detail, search results, and an explicitly requested protected field for its active vault.
  3. Public-origin traffic and callers replaying a handle for a different user or vault are rejected without data disclosure.
  4. Closing, expiring, logging out of, or failing authorization for an active vault makes its cached state unavailable.
  5. Malformed requests, unauthorized requests, and decode failures return safe errors without request secrets or decoded vault data.

**Plans**: TBD

### Phase 4: Authorized Go Application

**Goal**: The public Go application permits only a session-authenticated owner to retrieve an owned object and invoke the corresponding private Rust vault session.
**Depends on**: Phase 3
**Requirements**: GO-01, GO-02, GO-03, GO-04, GO-05, GO-06
**Success Criteria** (what must be TRUE):

  1. A protected Go request without a valid Supabase session cannot perform a vault action or receive vault data.
  2. An authenticated owner can invoke Go only for a vault they own; Go checks ownership before Storage retrieval and every Rust call.
  3. Unlock data is forwarded transiently to Rust, while the browser session retains only an opaque active-vault handle.
  4. Closing a vault or ending the Go session makes the corresponding Rust active-vault state unavailable.
  5. Users receive generic safe errors, and rendered pages and browser assets expose neither private configuration nor service credentials.

**Plans**: TBD

### Phase 5: HTMX Vault Interface

**Goal**: A signed-in owner can complete the private multi-vault workflow through secure server-rendered HTML, with HTMX enhancing rather than replacing critical forms.
**Depends on**: Phase 4
**Requirements**: UI-01, UI-02, UI-03, UI-04, UI-05, UI-06
**Success Criteria** (what must be TRUE):

  1. A signed-in user can complete critical vault actions through ordinary HTML forms, whose mutations reject invalid CSRF protection and use secure HttpOnly SameSite session cookies.
  2. A signed-in user can use HTMX-enhanced views to upload, select, and unlock either of two owned vaults.
  3. An unlocked user can navigate groups, browse entries, inspect safe entry detail, and search the active vault.
  4. An unlocked user can explicitly reveal a permitted protected field without the value appearing in page source or browser storage before the request.
  5. A user can close an active vault, and expiry or logout returns the interface to a state that cannot access the prior vault.

**Plans**: TBD
**UI hint**: yes

### Phase 6: Verification & Release Readiness

**Goal**: Developers and operators can prove the full private-vault workflow and deploy it without weakening its authorization or secrecy guarantees.
**Depends on**: Phase 5
**Requirements**: VER-01, VER-02, VER-03, VER-04, VER-05
**Success Criteria** (what must be TRUE):

  1. A developer can run Go tests that prove session validation, ownership checks, Go-to-Rust behavior, handler safety, and log redaction.
  2. A developer can run Rust contract and cleanup tests for authorization, malformed requests, close, and expiry.
  3. A developer can run browser tests in which a signed-in user uploads, selects, and securely opens two private vaults, while a second account is denied access.
  4. An operator can validate non-production RLS and Storage policies and inspect logs, generated HTML, and assets without finding secret exposure.
  5. An operator can deploy, configure, back up and restore, roll back, and recover the account while keeping Rust private.

**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5 → 6

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Baseline & Operations | 1/5 | In Progress|  |
| 2. Supabase Multi-Vault Foundation | 0/TBD | Not started | - |
| 3. Private Rust KeePass Service | 0/TBD | Not started | - |
| 4. Authorized Go Application | 0/TBD | Not started | - |
| 5. HTMX Vault Interface | 0/TBD | Not started | - |
| 6. Verification & Release Readiness | 0/TBD | Not started | - |
