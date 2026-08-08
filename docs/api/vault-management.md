# Vault Management API Contract

Backend routes for: creating a new vault, creating a new entry, 60s vault-idle
auto-close, and the (frontend-only) 10s clipboard auto-clear. Written for the
UI/design pass — every route below is implemented and covered by Go tests in
`backend-go/internal/web/app_test.go`.

All routes follow the app's existing conventions (see
`frontend/templates/vault-list.html` and `frontend/templates/browse.html`,
which already call the sibling routes these extend):

- Session auth via the `keepass4web_session` cookie (`HttpOnly`); no bearer
  tokens in the browser beyond the one-time Supabase sign-in exchange.
- Every mutating POST requires a hidden `csrf_token` field whose value comes
  from the page that rendered the form (`{{.CSRFToken}}` in existing
  templates). A missing/stale token returns `403`.
- Non-htmx form submissions redirect (`303`) on success. htmx requests
  (`HX-Request: true` header) get either a re-rendered fragment or an
  `HX-Redirect` response header, matching how `/vaults/select` already works.
- Errors render the same page with a `.Error` string set (see `sign-in.html`
  and `unlock.html` for the pattern) rather than a bare error page, for the
  two routes that have a natural "form" to re-render on failure
  (`/vaults/entries/create`); `/vaults/create` currently returns a plain
  `http.Error` body on failure (no dedicated create-vault page exists yet —
  the UI pass should decide whether to add one or keep this a modal on
  `/vaults` and surface the error via htmx's error-swap).

## Create a new vault

`POST /vaults/create`

| Field | Required | Notes |
|---|---|---|
| `csrf_token` | yes | from the `/vaults` page |
| `name` | yes | vault display name, 1-255 chars |
| `password` | yes | master password for the new vault; never stored |

Response: `303` redirect to `/vaults` (non-htmx) or a re-rendered
`#vault-list` fragment (htmx, same as `/vaults/upload`) including the new
vault in the list. No key-file support on creation (matches the "just get a
vault started" scope of this feature — key files can be added later via a
vault's normal unlock/re-encrypt flow once that's built out).

Failure statuses: `400` (missing name/password), `403` (bad/missing CSRF),
`500`/`503` (backend/creation failure).

## Create a new entry

`POST /vaults/entries/create`

Requires an unlocked vault (i.e. the session must already have visited
`/vaults/unlock` successfully — same precondition as
`/vaults/entries/update`).

| Field | Required | Notes |
|---|---|---|
| `csrf_token` | yes | from the `/vaults/browse` page |
| `title` | yes | non-empty |
| `username` | no | |
| `password` | no | |
| `url` | no | |
| `notes` | no | |
| `group_id` | no | UUID of an existing group from `GET /vaults/browse`'s data; omit for the vault's root group |
| `master_password` | yes | required to re-encrypt and persist the vault, same as the existing "Confirm & re-encrypt" dialog on `/vaults/entries/update` |
| `key_file` | no | file upload, only if the vault was unlocked with one |

Response: `303` redirect to `/vaults/browse` on success (the newly created
entry appears in the refreshed entry list — there is no separate "get the new
entry's ID back" response; reload state from `/vaults/browse` same as after
any other save).

Failure statuses: `400` (no unlocked vault, or missing title), `403` (bad
CSRF), `422` (master password/key file rejected by Rust), `500`/`502`/`503`
(storage/backend failures).

**UI suggestion:** reuse the existing "Save entry" → `<dialog>` re-encrypt
confirmation pattern from `browse.html`'s entry cards, just with blank
starting field values and no `entry_id` hidden input.

## Auto-close vault after 60s idle (server-enforced)

The Go process itself closes the active vault handle 60 seconds after the
session's last authenticated request, independent of any frontend code. Two
supporting routes exist so the UI can show an accurate countdown rather than
guessing:

`GET /session/status` — poll this to render/refresh an idle-warning banner.
No CSRF required (read-only, does not reset the idle clock). Returns `200`
with JSON:

```json
{ "active": true, "vault_active": true, "idle_seconds_remaining": 42 }
```

`active` is false (with the other two fields zeroed) if there's no valid
session cookie at all. `vault_active` is false once no vault is unlocked
(nothing to auto-close). `idle_seconds_remaining` counts down to 0 from the
60s budget; once it hits 0 the vault handle is already closed server-side
(the sweep runs every 5s, so treat 0 as "may already be closed, don't rely on
exactly-0 as the trigger — instead notice `vault_active` flip to `false` on
your next poll, or that a subsequent page navigation redirects to
`/vaults/unlock`").

`POST /session/heartbeat` — call this on detected real user interaction
(mouse move / keypress / click, reasonably throttled — e.g. at most once
every few seconds) to reset the idle clock without triggering a full page
navigation. Requires `csrf_token` (form-encoded body, one field). Returns
`204` on success, `403` on bad/missing CSRF, `303` redirect to `/sign-in` if
the session itself is gone.

Note: every existing authenticated route (loading `/vaults/browse`,
saving an entry, etc.) already resets the idle clock as a side effect — the
heartbeat route only matters for "user is present but hasn't triggered any
HTTP request in a while" (e.g. reading a decrypted entry on screen).

**UI suggestion:** the existing `#idle-banner` markup in `vault-list.html`
and `browse.html` (with `#idle-seconds` and a dismiss button) already
anticipates this — wire it to poll `/session/status` every few seconds while
`vault_active` is true, show the banner once `idle_seconds_remaining` drops
under some threshold (e.g. 15s), and call `/session/heartbeat` on real
interaction to make the banner go back away.

## Auto-clear clipboard after 10s (frontend-only, no backend route)

Not a backend concern. When the existing `data-action="copy"` /
`data-action="copy-by-id"` handlers in `app.js` call
`navigator.clipboard.writeText(value)`, they should also schedule
`navigator.clipboard.writeText('')` (or check-then-clear, to avoid
clobbering something the user copied from elsewhere in the meantime) via
`setTimeout(..., 10000)`, clearing any prior pending timeout first so rapid
repeated copies don't fire early clears. No API contract needed — this is
pure client-side behavior.
