# KeePass4Web UI — Implementation Guide

This bundle contains **ready-to-use Go `html/template` files**, not mockups to port. The markup, Tailwind classes, htmx attributes, and `{{ }}` field expressions are already final. Implementing this UI is now an API/data-wiring task, not a design task — do not redesign, re-markup, or restyle anything below; just make the listed Go fields and routes real.

## What's in this bundle

```
templates/
  head.html        {{define "head"}}, {{define "brand-mark"}}, {{define "corner-marks"}} — shared partials
  sign-in.html      {{define "sign-in"}}
  vault-list.html   {{define "vault-list"}} (fragment) + {{define "vaults"}} (full page wrapper)
  unlock.html       {{define "unlock"}}
  browse.html       {{define "browse"}}
web/static/
  app.js            all client-side behavior (event delegation, search filter, Supabase sign-in call)
  icons/            0.png, 1.png, 19.png, 27.png, 37.png, 48.png, LICENSE — real KeePass entry/vault icons
```

These map onto the existing routes/handlers in `internal/web/app.go` with **zero handler-logic changes** — same CSRF flow, same multipart parsing order, same `hx-post`/`hx-target="#vault-list"`/`hx-swap="outerHTML"` contract `uploadVault`/`selectVault` already expect.

| Template | Route(s) | Handler |
|---|---|---|
| `sign-in` | `GET /sign-in`, `POST /session` | `signInPage`, `createBrowserSession` |
| `vault-list` / `vaults` | `GET /vaults`, `POST /vaults/upload`, `POST /vaults/select` | `vaultsPage`, `uploadVault`, `selectVault` |
| `unlock` | `GET /vaults/unlock`, `POST /vaults/unlock` | `unlockPage`, `unlock` |
| `browse` | `GET /vaults/browse`, `POST /vaults/entries/update`, `POST /vaults/close`, `POST /sign-out` | `browse`, `updateEntry`, `closeVault`, `signOut` |

### New in this revision — vault-management features

Per `docs/api/vault-management.md`, the four routes below are **already implemented and Go-tested on the backend** — this is a frontend-only wiring pass, not new backend work:

| UI addition | Route | Notes |
|---|---|---|
| "Create a new vault" dialog on Vaults | `POST /vaults/create` | htmx-enhanced, same `#vault-list` fragment swap as upload; failures are a plain `http.Error` body per the doc, surfaced generically via `hx-on::response-error` on the form |
| "New entry" dialog on Browse | `POST /vaults/entries/create` | plain form (no htmx — success is a full `303` redirect back to `/vaults/browse`); failures **re-render `browse` with `.Error` set**, shown by the `#browse-error` banner now at the top of `browse.html`'s `<main>` |
| Idle banner, wired for real | `GET /session/status` (poll, no CSRF), `POST /session/heartbeat` (CSRF via form body) | `app.js`'s `startIdlePolling()` polls every 5s, shows the existing `#idle-banner` once `idle_seconds_remaining <= 15` while `vault_active`, and fires a throttled heartbeat on `mousemove`/`keydown`/`click` |
| Clipboard auto-clear | *(frontend-only, no route)* | `app.js`'s `copyWithAutoClear()` replaces the old bare `navigator.clipboard.writeText` calls in both copy handlers; clears 10s after copy unless the clipboard changed in the meantime |

---

## Drop-in steps

1. **Copy files in.**
   - `templates/*.html` → your repo's `templates/` directory.
   - `web/static/app.js` → your repo's `web/static/app.js`.
   - `web/static/icons/*` → your repo's `web/static/icons/` (these are the same files as `public/img/icons/`, already MIT/LGPL-licensed for this project — see the included `LICENSE`).

2. **Point the app at the files instead of the inline string.** In `internal/web/app.go`, replace:
   ```go
   tmpl: template.Must(template.New("pages").Parse(pageTemplates))
   ```
   with:
   ```go
   tmpl: template.Must(template.New("pages").Funcs(template.FuncMap{
       "lower": strings.ToLower,
   }).ParseGlob("templates/*.html"))
   ```
   Delete the `pageTemplates` const. `tailwind.config.js`'s `content` glob already covers `./templates/**/*.html`, so nothing else changes there.

3. **Add the four struct fields the templates read that don't exist yet.** This is the only real implementation work left:

   ```go
   type session struct {
       userID, accessToken, csrfToken, activeHandle, activeVaultID, email string // + email
   }

   type vaultPage struct {
       Vaults          []Vault
       CSRFToken       string
       Fragment        bool
       Selected        Vault
       Email           string // new
       SupabaseURL     string // new — sign-in page only
       SupabaseAnonKey string // new — sign-in page only
       Error           string // new — sign-in and unlock error message
   }
   ```

   Wire them up:
   - `createBrowserSession`: after `a.auth.Validate` succeeds, store `identity.Email` on the session (alongside `identity.ID`) so `.Email` renders in the Vaults/Browse header.
   - `signInPage`: pass `SupabaseURL: configuration.SupabaseURL, SupabaseAnonKey: configuration.SupabaseAnonKey` (already loaded in `cmd/web/main.go` — thread them into `Dependencies`/`App` the same way `secureCookies` is threaded today).
   - `createBrowserSession` failure path: instead of `http.Error(w, "Sign-in failed.", 401)`, re-render `sign-in` with `Error: "Sign-in failed."` set, so the page keeps its CSRF token and the error box the template already has.
   - `unlockPage`: look up the selected vault the same way `unlock()` already does via `a.vault(...)` and pass `Selected: v` — the template's vault-name badge reads `.Selected.Name`.
   - `unlock()` failure path: instead of `http.Error(w, "The vault password or key file is incorrect...", 422)`, re-render `unlock` with `Selected` and `Error` set.
   - `browse()`: also pass `Selected` (same lookup) for the header/back-link vault name.

4. **Add the Tailwind tokens the templates use** to `tailwind.config.js`:
   ```js
   theme: {
     extend: {
       colors: { accent: {50:'#eef2f6',100:'#dbe4ec',200:'#b9cbdb',300:'#93b0c8',400:'#7699b7',500:'#5980a6',600:'#4a6c8c',700:'#3c5771',800:'#2e4258',900:'#212f3f'} },
       fontFamily: { heading: ['Barlow Condensed','sans-serif'], body: ['Barlow','system-ui','sans-serif'] }
     }
   }
   ```
   Rebuild `web/static/tailwind.css` with your existing build script. Vendor Barlow / Barlow Condensed under `public/fonts/` (there's already a `.gitignore` there for vendored fonts) so the page has no external font dependency at request time, matching `docs/operations/topology.md`'s "Go is the only publicly exposed service" posture.

5. **Confirm `web/static/htmx.min.js` is vendored** (the `head` template already references `/static/htmx.min.js`, unchanged from the original). Do **not** add a second htmx `<script>` tag or `cdn.tailwindcss.com` — those only existed in the earlier design mockups for standalone previewing and must not ship.

6. **Enable Supabase email/password sign-in.** `app.js`'s sign-in handler calls `POST {SupabaseURL}/auth/v1/token?grant_type=password` directly from the browser using the anon key already embedded via `data-supabase-url`/`data-supabase-anon-key` on `<body>` in `sign-in.html`. Confirm in your Supabase project that email/password auth is enabled and that the app's origin is allowed — this is a new browser→Supabase call path; previously the backend only validated tokens handed to it, it never issued them.

7. **Add the one new field the vault-management routes need: `Error` on the `browse` render data.** Since `/vaults/entries/create` re-renders `browse` on failure per the API contract, extend the anonymous struct in the `browse()` handler:
   ```go
   a.render(w, "browse", struct {
       vaultPage
       Entries []DecodedEntry
   }{vaultPage: vaultPage{CSRFToken: s.csrfToken, Selected: v, Error: /* set only on the createEntry failure path */ ""}, Entries: entries})
   ```
   and have `createEntry`'s failure branches call `a.browse` (or an equivalent internal render) with `Error` populated instead of `http.Error`, matching how `sign-in`/`unlock` already do it (see step 3 above). `/vaults/create` needs no server-side change — its plain `http.Error` body is exactly what the doc describes, and the modal's `hx-on::response-error` already covers it client-side.

8. **Verify `data-csrf-token` is present wherever `app.js` needs it.** `vault-list.html`'s `#vault-list` div and `browse.html`'s root wrapper both now carry `data-csrf-token="{{.CSRFToken}}"` — `app.js` reads the first `[data-csrf-token]` element on the page for the heartbeat POST. If you restructure either template, keep that attribute somewhere in the DOM.

9. **No backend changes needed for `/vaults/create`, `/vaults/entries/create`, `/session/status`, or `/session/heartbeat` themselves** — per `docs/api/vault-management.md` they're already implemented and covered by Go tests (`app_test.go`'s `TestCreateVault*`, `TestCreateEntry*`, `TestIdleSweep*`, `TestSessionStatus*`, `TestHeartbeat*`). Double-check only that your route paths/field names match this bundle's forms exactly (`name`+`password` for create-vault; `title`/`username`/`password`/`url`/`notes`/`master_password`/`key_file` for create-entry, no `entry_id`).

## Left untouched (verify, don't rewrite)

CSRF validation, multipart parsing order, session/cookie handling, and every call into `internal/privateclient` (Rust) and `internal/supabase` — none of that needs to change. Passwords are still rendered directly into `value="..."` on the decoded-entry form, matching current behavior; that's a pre-existing security tradeoff, not something introduced by this UI, and is out of scope here.

## Verification checklist

- `go build ./...` — confirms `ParseGlob` finds all five `{{define}}` blocks (five files can `panic` at startup if one has a typo; check the error message names the exact template).
- `go test ./...`, including `internal/web/app_test.go` (status codes/redirects are unaffected — only rendered HTML changes) and `go test ./tests/operations -run TestGoOutputContract -count=1`.
- `npx playwright test tests/e2e/main-flow.spec.js` — update selectors if they targeted the old bare markup.
- Manual pass at ~375px and ~1280px: sign in (email+password) → upload a `.kdbx` → open it → unlock → expand an entry, reveal/copy the password, edit a field, confirm through the re-encrypt dialog → close vault → sign out.
- New this revision: create a vault via the "Create a new vault" dialog; add an entry via "New entry" and confirm it appears after the redirect; force a create-entry failure (wrong master password) and confirm the `#browse-error` banner renders; watch the idle banner appear after real inactivity and disappear after moving the mouse; copy a password and confirm the clipboard is empty ~10s later (unless overwritten sooner).
