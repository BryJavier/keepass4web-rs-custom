# Vault and Browse CRUD API Contract

Backend contract for a future UI pass. Browser calls use the existing `keepass4web_session` cookie. Every `POST` includes the page-rendered `csrf_token`. Normal form success is a `303`; htmx success is a fragment refresh or `HX-Redirect` following existing routes.

## Shared rules

- Vault and entry identifiers are UUIDs. Unowned/missing items return `404`.
- Active lists exclude trashed records. Trash lists expose only recoverable owner records, with `purge_after` in RFC 3339 UTC.
- Delete and restore are idempotent. Delete preserves its first 30-day deadline; no browser API permanently deletes data.
- Entry writes use `multipart/form-data` and require `master_password`; `key_file` is optional.
- `422` means KDBX credentials/re-encryption failed. Do not automatically retry with the same credentials.

## Vaults

### List active vaults

`GET /vaults`

Returns the existing vault HTML page or htmx `#vault-list` fragment. Active vault data includes `id` and `name`. Unauthenticated callers receive `303 /sign-in`.

### Create

`POST /vaults/create`

Required form fields: `csrf_token`, `name` (1–255 characters), `password`. Creates/uploads an encrypted empty KDBX.

Success: `303 /vaults` or list refresh. Failures: `400` invalid input, `403` CSRF, `503` unavailable backend, `5xx` creation/storage.

### Rename

`POST /vaults/rename`

Fields: `csrf_token`, `vault_id`, `name` (trimmed, 1–255 characters). Active owner vaults only.

Success: `303 /vaults` or list refresh. Failures: `400`, `403`, `404`, `409` trashed state, `5xx`.

### Download encrypted KDBX

`GET /vaults/download?vault_id={uuid}`

Downloads only an active owner vault's original encrypted bytes. It does not unlock or decrypt the vault and needs no password/key file.

```http
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="{sanitized-name}.kdbx"
Cache-Control: no-store
```

Failures: `303 /sign-in`, `404` missing/unowned/trashed, `5xx` Storage failure.

### Trash and restore

`POST /vaults/delete` fields: `csrf_token`, `vault_id`. It closes a selected/unlocked copy in this session, hides the vault, and retains its KDBX/object metadata for 30 days.

`GET /vaults/trash` returns each recoverable vault's `id`, `name`, `trashed_at`, and `purge_after`.

`POST /vaults/restore` fields: `csrf_token`, `vault_id`. It restores the same ID/object path to the active list.

Success for writes: `303 /vaults` or fragment refresh. Failures: `403`, `404` unowned/expired, `409` invalid state, `503` unsafe close, `5xx`.

## Browse-vault entries

### Read active entries

`GET /vaults/browse`

Returns decoded active entries for the selected, unlocked vault. Locked/unselected state redirects to unlock/vault list. Trashed entries never appear.

### Create or update

`POST /vaults/entries/create` and `POST /vaults/entries/update`

Multipart fields: `csrf_token`, `title` (required for create), `username`, `password`, `url`, `notes`, `master_password`, optional `key_file`. Update also requires `entry_id`; create optionally accepts `group_id` and otherwise targets root.

Success: `303 /vaults/browse`. Failures: `400` state/input, `403` CSRF, `422` re-encryption credentials, `502` Storage/mirror sync after a KDBX write, `5xx`.

### Trash an entry

`POST /vaults/entries/delete`

Multipart fields: `csrf_token`, `entry_id`, `master_password`, optional `key_file`.

The server removes the entry from KDBX, stores replacement bytes, then preserves its decoded payload as a 30-day tombstone. Success: `303 /vaults/browse`. Error codes match create/update; `404` means no active entry in the selected vault.

### List and restore entry trash

`GET /vaults/entries/trash`

Requires an active selected vault. Returns recoverable entry tombstones with `entry_id`, `title`, `username`, `trashed_at`, and `purge_after`.

`POST /vaults/entries/restore`

Multipart fields: `csrf_token`, `entry_id`, `master_password`, optional `key_file`. Restores its original group, or the vault root when that group was removed.

Success: `303 /vaults/browse`. Failures: `400` no unlocked vault, `403`, `404` unowned/expired, `422` credentials, `502` replacement/mirror failure, `5xx`.

## Retention

The backend purges resources once `purge_after` passes. On restore `404`, the UI must remove the item from local trash state and show that the recovery period expired. There is intentionally no empty-trash or delete-now browser action.

