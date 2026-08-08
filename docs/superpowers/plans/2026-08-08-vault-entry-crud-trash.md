# Vault and Entry CRUD with 30-Day Trash Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver complete backend CRUD for vaults and entries, active-vault KDBX downloads, owner-only 30-day trash/restore flows, and automatic permanent purge.

**Architecture:** Rust owns KDBX mutation; Go owns HTTP, CSRF, ownership, and orchestration; Supabase stores lifecycle metadata and encrypted objects. Owner JWTs serve browser paths. A narrow, server-only service-role client runs the purge worker because owner-scoped RLS cannot purge every account's expired data.

**Tech Stack:** Rust/actix-web and existing `keepass`; Go `net/http`; Supabase Postgres, Storage, and RLS; no frontend changes or dependencies.

## Global Constraints

- KDBX crypto and entry mutation stay in `backend-rust`; Go never decodes KDBX.
- Browser mutations validate existing CSRF before state changes; multipart routes parse first.
- `SUPABASE_SERVICE_ROLE_KEY` is server-only and never reaches browser code, logs, or telemetry.
- Soft-delete preserves its first `purge_after`; restore does not extend retention; permanent deletion is internal-only.
- Retention is exactly `trashed_at + interval '30 days'` in UTC.
- Do not log KDBX bytes, entry fields, passwords, key files, or base64 credentials.

---

## File Structure

| File | Change |
|---|---|
| `supabase/migrations/0004_vault_trash.sql` | Lifecycle columns, constraints, and indexes. |
| `supabase/tests/vault_access.sql` | Active/trash RLS and retention checks. |
| `backend-go/internal/supabase/vaults.go` | Lifecycle, active sync, download, and purge operations. |
| `backend-go/internal/supabase/client_test.go` | Repository request-shape tests. |
| `backend-rust/src/keepass/keepass.rs` | Remove/restore entry helpers with root fallback. |
| `backend-rust/src/private_service.rs` | Authenticated entry delete/restore private endpoints. |
| `backend-go/internal/privateclient/client.go` | Typed private-service contracts. |
| `backend-go/internal/web/app.go` | Browser handlers, download, and purge scheduling. |
| `backend-go/internal/web/app_test.go` | Handler, fake, idempotency, and error tests. |
| `backend-go/internal/config/config.go`, `cmd/web/main.go` | Server-only purge configuration/client wiring. |
| `docs/api/vault-crud.md` | UI API contract. |

### Task 1: Model 30-day trash in Supabase

**Files:**
- Create: `supabase/migrations/0004_vault_trash.sql`
- Modify: `supabase/tests/vault_access.sql`

**Interfaces:**
- Produces `trashed_at` and `purge_after` timestamps on `vaults` and `decoded_vault_entries`.
- Produces lifecycle data that the server-only purge client can query and delete through the Storage and PostgREST APIs.

- [ ] **Step 1: Write failing SQL assertions**

Extend the pgTAP plan to prove both timestamp pairs exist; active owner queries exclude trashed rows; owners can restore only their own rows; and a tombstone payload remains unreadable to another user.

- [ ] **Step 2: Verify that those assertions fail**

Run: `supabase test db`

Expected: failures for the absent lifecycle schema.

- [ ] **Step 3: Add the migration**

Add nullable timestamps with this constraint to both tables:

```sql
check ((trashed_at is null and purge_after is null)
   or (trashed_at is not null and purge_after = trashed_at + interval '30 days'))
```

Add owner/trash indexes while retaining the existing owner-only RLS. Do not add direct `storage.objects` SQL deletion: the existing access test prohibits it. The Go purge worker will use the Storage API with its server-only credential, delete the object before metadata, and use `purge_after <= now` predicates to make retries safe.

- [ ] **Step 4: Verify database behavior**

Run: `supabase test db`

Expected: PASS, including RLS isolation and exact-retention checks.

- [ ] **Step 5: Commit**

```bash
git add supabase/migrations/0004_vault_trash.sql supabase/tests/vault_access.sql
git commit -m "feat(db): add recoverable vault and entry trash"
```

### Task 2: Add Supabase lifecycle repository operations

**Files:**
- Modify: `backend-go/internal/supabase/vaults.go`
- Modify: `backend-go/internal/supabase/client_test.go`

**Interfaces:**
- Produces `Rename`, `TrashVault`, `RestoreVault`, `ListTrashedVaults`, `TrashEntry`, `RestoreEntry`, `ListTrashedEntries`, `SyncActiveDecodedEntries`, and `PurgeExpiredTrash`.
- Existing `List`, `Owns`, and `DecodedEntries` return active rows only.

- [ ] **Step 1: Write failing request-shape tests**

Use `recordingHTTP` to assert active filters use `trashed_at=is.null`; trash queries use non-null unexpired lifecycle fields; delete patches do not replace a preexisting deadline; restore clears both fields; and purge lists expired vaults, deletes each Storage object, deletes its guarded metadata, then removes expired standalone entry tombstones with the server credential.

- [ ] **Step 2: Verify failure**

Run: `go test ./backend-go/internal/supabase -run 'Test(List|Trash|Restore|Purge)' -count=1`

Expected: compile failures for new methods.

- [ ] **Step 3: Implement lifecycle-safe methods**

Extend `Vault` and `DecodedEntry` with lifecycle timestamps. Replace `ReplaceDecodedEntries` with `SyncActiveDecodedEntries`: upsert supplied active entries and remove only active rows absent from the KDBX—never a tombstone. Keep object-path sanitization and add encrypted-byte download for active vaults.

- [ ] **Step 4: Verify**

Run: `go test ./backend-go/internal/supabase -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend-go/internal/supabase/vaults.go backend-go/internal/supabase/client_test.go
git commit -m "feat(go): add Supabase vault trash lifecycle operations"
```

### Task 3: Implement KDBX entry remove and restore

**Files:**
- Modify: `backend-rust/src/keepass/keepass.rs`
- Modify: `backend-rust/src/private_service.rs`

**Interfaces:**
- Produces `KeePass::remove_entry(entry_id) -> Result<()>`.
- Produces `KeePass::restore_entry(entry_id, preferred_group_id, EntryUpdate) -> Result<()>`.
- Produces `POST /internal/v1/entries/delete` and `POST /internal/v1/entries/restore`, returning `database_b64`.

- [ ] **Step 1: Write failing Rust tests**

Test removal survives KDBX round-trip, restoration preserves the supplied UUID/fields, original-group restore works, absent-group restore uses root, and private calls reject handle/user/vault mismatches.

- [ ] **Step 2: Verify failure**

Run: `cargo test --locked 'keepass::keepass::tests|private_service::tests'`

Expected: missing operation and request-type compilation failures.

- [ ] **Step 3: Implement minimal helpers and routes**

Remove an entry through a recursive parent-group search. Restore an entry using its stored UUID before insertion; use the existing mutable group finder then root fallback. Mirror `UpdateRequest`'s zeroization and re-encryption flow in delete/restore requests and register routes inside authenticated `/internal/v1`.

- [ ] **Step 4: Verify**

Run: `cargo test --locked`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend-rust/src/keepass/keepass.rs backend-rust/src/private_service.rs
git commit -m "feat(rust): support entry trash deletion and restore"
```

### Task 4: Extend the Go-to-Rust contract

**Files:**
- Modify: `backend-go/internal/privateclient/client.go`
- Modify: `backend-go/internal/web/app_test.go`

**Interfaces:**
- Produces `DeleteEntry(context.Context, DeleteEntryRequest) (DatabaseResponse, error)`.
- Produces `RestoreEntry(context.Context, RestoreEntryRequest) (DatabaseResponse, error)`.

- [ ] **Step 1: Write failing fake-service tests**

Add delete/restore counters, captured requests, replacement-KDBX fixtures, and errors to `fakeRust`; enforce compile-time conformance to `RustVaultService`.

- [ ] **Step 2: Verify failure**

Run: `go test ./backend-go/internal/web -run 'Test(Delete|Restore)Entry' -count=1`

Expected: missing-method compilation failure.

- [ ] **Step 3: Add typed client methods**

Define exact JSON request fields for handle/user/vault/entry IDs, credentials, and restore snapshot/group data. Route only to the Task 3 private endpoints. Reuse a neutral `DatabaseResponse` with existing `database_b64` JSON.

- [ ] **Step 4: Verify**

Run: `gofmt -w backend-go/internal/privateclient/client.go backend-go/internal/web/app_test.go && go test ./backend-go/internal/privateclient ./backend-go/internal/web -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend-go/internal/privateclient/client.go backend-go/internal/web/app_test.go
git commit -m "feat(go): add private entry trash client contract"
```

### Task 5: Implement Go vault CRUD, entry trash flows, and download

**Files:**
- Modify: `backend-go/internal/web/app.go`
- Modify: `backend-go/internal/web/app_test.go`

**Interfaces:**
- Produces `POST /vaults/rename`, `GET /vaults/download`, `POST /vaults/delete`, `GET /vaults/trash`, `POST /vaults/restore`, `POST /vaults/entries/delete`, `GET /vaults/entries/trash`, and `POST /vaults/entries/restore`.
- Extends `SessionVaultRepository` with Task 2 operations.

- [ ] **Step 1: Write failing handler tests**

Cover CSRF no-op; inaccessible/trashed vault download is `404`; valid download returns original encrypted bytes with octet-stream, no-store, and sanitized `.kdbx` attachment headers; vault trash closes/clears an active session; entry delete/restore requires handle and credentials; root fallback request is forwarded; replacement failure leaves the tombstone unchanged.

- [ ] **Step 2: Verify failure**

Run: `go test ./backend-go/internal/web -run 'Test(VaultRename|VaultDownload|VaultTrash|VaultRestore|EntryTrash|EntryRestore)' -count=1`

Expected: missing routes/methods.

- [ ] **Step 3: Implement routes and handlers**

Use form `vault_id` and `entry_id` values. Parse entry delete/restore multipart forms, invoke Rust, validate returned bytes, replace Storage, then alter decoded lifecycle state. Load the owner tombstone before restore. For vault trash, set lifecycle state then close/clear the matching session; reverse the lifecycle patch if safe closure fails. Generate download filenames from sanitized display names, never object paths.

- [ ] **Step 4: Verify**

Run: `gofmt -w backend-go/internal/web/app.go backend-go/internal/web/app_test.go && go test ./backend-go/internal/web ./backend-go/internal/privateclient ./backend-go/internal/supabase -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend-go/internal/web/app.go backend-go/internal/web/app_test.go
git commit -m "feat(go): add vault and entry CRUD trash handlers"
```

### Task 6: Schedule protected automatic purge

**Files:**
- Modify: `backend-go/internal/config/config.go`
- Modify: `backend-go/internal/config/config_test.go`
- Modify: `backend-go/cmd/web/main.go`
- Modify: `backend-go/internal/web/app.go`
- Modify: `backend-go/internal/web/app_test.go`

**Interfaces:**
- Adds `SUPABASE_SERVICE_ROLE_KEY` and `VAULT_TRASH_PURGE_INTERVAL` (default `1h`).
- Produces a narrow `TrashPurger` dependency and `App.purgeExpiredTrash(context.Context)`.

- [ ] **Step 1: Write failing config and scheduler tests**

Assert production rejects a missing service-role key and non-positive purge interval. With a short injected interval, assert exactly one repository call per tick, no request-handler blocking, and retry after a failure without logging identifiers/payloads.

- [ ] **Step 2: Verify failure**

Run: `go test ./backend-go/internal/config ./backend-go/internal/web -run 'Test.*Purge|Test.*ServiceRole' -count=1`

Expected: missing config and worker failures.

- [ ] **Step 3: Wire the server-only client and worker**

Construct a second Supabase client in `cmd/web/main.go`, pass only the purge interface to `App`, run a separate ticker with bounded background context, and expose no browser purge route.

- [ ] **Step 4: Run full verification**

Run: `cargo test --locked && go test ./backend-go/... -count=1 && npm test -- --runInBand`

Expected: PASS. Also run `supabase test db` when local Supabase is available.

- [ ] **Step 5: Commit**

```bash
git add backend-go/internal/config backend-go/cmd/web/main.go backend-go/internal/web
git commit -m "feat(go): purge expired vault and entry trash"
```

## Plan self-review

- Coverage includes vault list/create/read/download/rename/delete/restore, entry browse/create/update/delete/restore, retention, purge, security, and contract.
- Interfaces are ordered: database lifecycle → repository → KDBX → private client → HTTP → purge worker.
- Storage replacement precedes mirror lifecycle changes; only the purge worker receives the service-role capability.

## Corrective implementation gate

Before continuing the existing code changes, replace the name-based KDBX tombstone design with the approved state-machine design. Add a migration for vault/entry lifecycle states and vault revision; use an atomic database claim for `trashed → purging`; require expected revision when replacing a KDBX; persist entry snapshots and transitional states before mutation; invalidate all vault handles on vault trash; enforce active-parent reads; and add server-only Compose/runtime wiring for `SUPABASE_SERVICE_ROLE_KEY`. Do not merge the current draft implementation until these changes and their failure-injection tests pass.
