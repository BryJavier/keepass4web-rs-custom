# Vault and Entry CRUD with 30-Day Trash — Backend Design

## Goal

Provide complete backend CRUD for vaults and KeePass entries, with owner-only,
recoverable soft deletion and automatic permanent removal 30 days later. This
spec deliberately excludes template, JavaScript, and visual work; the matching
API contract will let a later UI pass design those experiences.

## Scope

- Vaults: list, create, rename, download as `.kdbx`, soft-delete, list trash,
  and restore.
- Entries in an unlocked vault: list/browse, create, update, soft-delete,
  list trash, and restore.
- A backend purge worker permanently deletes expired vaults and entry
  tombstones.
- Restore returns an entry to its original group when it remains present;
  otherwise it returns the entry to the vault root group.

## Architecture

Rust remains the only process that opens, mutates, or serializes KDBX bytes.
The Go web service authenticates browser requests, validates CSRF and resource
ownership, coordinates Supabase metadata and object storage, and runs the
purge worker. Supabase stores lifecycle metadata and the existing decoded entry
mirror; it never receives a master password or key-file material.

The regular vault and entry queries return only active records. Trash queries
return only records belonging to the current session owner and whose immutable
`purge_after` time has not elapsed. There is no browser route for permanent
deletion.

### Vault lifecycle

1. Creating a vault keeps the existing Rust-create and Go upload flow.
2. Renaming changes only the active vault's metadata name.
3. Download reads only an active, owner-owned vault's encrypted Storage object
   and returns it as a streamed `application/octet-stream` attachment with a
   sanitized `.kdbx` filename. It neither opens the KDBX nor needs master
   password or key-file material. Trashed vaults are not downloadable.
4. Soft deletion atomically marks a vault `trashed_at = now()` and
   `purge_after = now() + 30 days`, then closes any active Rust handle for it.
   Its KDBX object and decoded-entry rows remain available for restoration.
5. Restore clears both lifecycle fields and exposes the same vault ID and
   object path again.
6. The purge worker deletes the Storage object, decoded-entry rows, and vault
   metadata for expired trashed vaults. It is idempotent and safe to retry.

### Entry lifecycle

1. Entry create and update remain KDBX writes: the browser supplies the master
   password and optional key file, Rust produces replacement bytes, and Go
   replaces the Storage object then synchronizes the decoded-entry mirror.
2. Soft deletion invokes Rust to remove the entry from the in-memory KDBX,
   requires the same re-encryption credentials, replaces the KDBX object, and
   marks the entry mirror row with `trashed_at` and `purge_after` rather than
   deleting its payload.
3. Restore uses the stored mirror payload to ask Rust to recreate the entry
   with its original `entry_id` and group. If the group ID cannot be found,
   Rust inserts it into the root group. Go replaces the KDBX object and clears
   the entry lifecycle fields only after the replacement succeeds.
4. The purge worker permanently deletes expired trashed entry rows. It does
   not need to modify KDBX because those entries were removed at soft-delete
   time.

## Data model

Add nullable `trashed_at timestamptz` and `purge_after timestamptz` to
`vaults` and `decoded_vault_entries`. A database check requires both fields to
be null together or both set, and requires `purge_after = trashed_at +
interval '30 days'`. Active-list indexes and owner-scoped trash indexes support
the two query paths. RLS remains owner-only and applies unchanged to rows in
either state.

Decoded mirror replacement must preserve trashed rows. The current
replace-all implementation is therefore replaced with an active-entry sync:
upsert active rows from the KDBX and remove only active rows absent from that
KDBX, never tombstones. This prevents a normal active-entry update from
silently erasing recoverable data.

## Correctness and security

- Every browser mutation requires the existing `csrf_token`; multipart parsing
  precedes validation when a key file may be present.
- Go checks the vault is owned and active before any active-vault action, and
  validates the selected active vault/handle for entry actions.
- The download response sets `Content-Type: application/octet-stream`, an RFC
  5987-safe `Content-Disposition: attachment` filename ending in `.kdbx`, and
  `Cache-Control: no-store`; it never derives the filename from an untrusted
  object path.
- Rust private endpoints authenticate the Go service credential first and bind
  each handle to its user and vault.
- Vault and entry delete/restore are idempotent. Repeated calls do not extend
  the original 30-day deadline. A restore after permanent purge behaves as a
  not-found result.
- Secrets (entry fields, master password, key file, base64 KDBX bytes) are
  never added to telemetry, errors, or route parameters.
- A failed storage replacement leaves the lifecycle mirror state unchanged;
  code reports the failed mutation rather than claiming it completed.

## Verification

Tests cover migrations/RLS, Supabase request shape, Go handlers and session
closure, private-client contracts, Rust KDBX deletion/restoration and root
fallback, idempotency, retry behavior, the 30-day boundary, and redaction.
End-to-end coverage exercises vault and entry delete/restore through the
server-rendered HTTP layer without adding UI work.

## Revision: transactional trash lifecycle

This revision supersedes the earlier KDBX tombstone-group approach.

### Entries

Recoverable entry data remains in the owner-only `decoded_vault_entries` row; no hidden group is created in the KDBX. Add a lifecycle state (`active`, `deleting`, `trashed`, `restoring`) and a vault revision. An entry delete first records a recovery snapshot and moves the row to `deleting`, then removes/re-encrypts/replaces the KDBX using the expected revision, and finally marks the row `trashed`. Restore follows the reverse state sequence and recreates the entry from the row payload in its original group or root fallback.

A failed downstream step leaves a retryable transitional state; active and trash views exclude transitional rows. KDBX replacement must invalidate the current Rust handle or atomically refresh it so a failed/old handle cannot write stale bytes.

### Vaults

Vaults use `active`, `trashed`, and `purging` lifecycle states. The scheduled worker atomically claims an expired trashed vault as `purging` before any Storage call. Restore works only from `trashed`; a `purging` vault is no longer recoverable. Storage deletion happens before final metadata deletion; retries resume the `purging` operation safely.

Trashing a vault invalidates every active in-memory handle for that vault, and browse/read queries verify the parent vault is active before returning decoded fields.

### Server-only purge credential

`SUPABASE_SERVICE_ROLE_KEY` is injected solely into the Go web service's server-side runtime configuration and Compose environment. It is never rendered in page data, browser configuration, logs, or client JavaScript.

### Verification additions

Test state-transition retry/rollback, expected-revision conflicts, stale handle invalidation, purge-vs-restore claiming, parent-vault access filtering, and absence of secret fields in restore requests.
