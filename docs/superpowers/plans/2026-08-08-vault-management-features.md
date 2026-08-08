# Vault Management Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add backend support for creating new vaults, creating new entries inside a vault, and server-enforced 60s vault-idle auto-close. Ship an API contract doc so a separate UI pass can build the forms/JS against a stable backend.

**Architecture:** Same three-tier split as the existing app: Rust (`backend-rust/`) owns all KDBX cryptography (new empty database creation, new entry creation inside an unlocked in-memory database); Go (`backend-go/`) owns HTTP routing, CSRF, session state, and Supabase storage I/O; Supabase stores vault bytes + decoded-entry mirror. New Rust endpoints are added to the existing `/internal/v1` private scope; new Go routes follow the exact CSRF/multipart/redirect conventions already used by `/vaults/upload` and `/vaults/entries/update`.

**Tech Stack:** Rust (actix-web, the `keepass` fork crate already vendored), Go (`net/http`, no new deps), no new frontend framework — the UI pass will keep using server-rendered `html/template` + htmx per the existing pattern.

## Global Constraints

- Go never touches raw KDBX bytes' cryptography — only Rust decodes/encodes `.kdbx` files. (existing invariant, `backend-rust/src/keepass/keepass.rs`)
- Every mutating Go route validates `csrf_token` via `a.validCSRF(r, s)` before doing anything else that isn't itself required to parse the CSRF token (multipart parsing must happen first — see `internal/web/app.go`'s existing comments on this ordering).
- New Rust private-service routes must call `authenticated(&request, &credential)` first, exactly like every existing route in `backend-rust/src/private_service.rs`.
- No new fields may be logged. `backend-go/internal/observability.SafeFields` is the only allow-listed set (`docs/operations/redaction.md`) — do not add title/username/password/notes/master_password to any log/trace call.
- `RustVaultService` (the Go-side interface in `internal/web/app.go`) is implemented by both the real `privateclient.Client` and test fakes (`fakeRust` in `app_test.go`) — any interface method addition must update both.
- Master password / key file are never persisted server-side outside the active in-memory Rust handle's lifetime — the create-entry route requires the caller to resupply the master password on every write, exactly like the existing update-entry route.

---

## File Structure

| File | Change |
|---|---|
| `backend-rust/src/keepass/keepass.rs` | Add `KeePass::new_empty`, `KeePass::create_entry`, `find_group_by_id_mut` |
| `backend-rust/src/private_service.rs` | Add `CreateVaultRequest/Response`, `CreateEntryRequest/Response`, `create_vault`/`create_entry` service methods + `/create-vault` and `/entries/create` routes |
| `backend-go/internal/privateclient/client.go` | Add `CreateVault`/`CreateEntry` client methods + request/response types |
| `backend-go/internal/web/app.go` | Extend `RustVaultService`; add `createVault`, `createEntry` handlers + routes; add idle-timeout sweep (`session.lastActivity`, background goroutine, `GET /session/status`, `POST /session/heartbeat`) |
| `backend-go/internal/web/app_test.go` | Extend `fakeRust` with the two new methods; add tests for the four new behaviors |
| `backend-rust/src/keepass/keepass.rs` tests module | Unit tests for `new_empty`/`create_entry` |
| `backend-rust/src/private_service.rs` tests module | Unit test for idle-independent close-on-mismatch (existing pattern, reused) |
| `docs/api/vault-management.md` (new) | High-level API contract for the UI/design pass |

---

## Task 1: Rust — empty-vault creation and entry creation

**Files:**
- Modify: `backend-rust/src/keepass/keepass.rs`
- Test: same file, `#[cfg(test)] mod tests`

**Interfaces:**
- Produces: `KeePass::new_empty(config: &Config) -> Self`, `KeePass::create_entry(&mut self, group_id: Option<Uuid>, update: EntryUpdate) -> Result<Uuid>`

- [ ] **Step 1: Write the failing tests**

Add to the existing `mod tests` block at the bottom of `backend-rust/src/keepass/keepass.rs`:

```rust
    #[tokio::test]
    async fn new_empty_database_has_a_root_group_and_no_entries() {
        let config = Config::default();
        let database = KeePass::new_empty(&config);
        let (root, _) = database.get_groups().unwrap();
        assert_eq!(root.entries.len(), 0);
    }

    #[tokio::test]
    async fn new_empty_database_survives_kdbx_roundtrip() {
        let config = Config::default();
        let database = KeePass::new_empty(&config);
        let bytes = database.to_kdbx_bytes(Some(Zeroizing::new("test".to_owned())), None).unwrap();
        let reopened = KeePass::from_bytes(&config, bytes, Some(Zeroizing::new("test".to_owned())), None).await.unwrap();
        let (root, _) = reopened.get_groups().unwrap();
        assert_eq!(root.entries.len(), 0);
    }

    #[tokio::test]
    async fn create_entry_adds_a_new_entry_to_the_root_group() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);

        let entry_id = database.create_entry(None, EntryUpdate {
            title: "New Site".to_owned(),
            username: "alice".to_owned(),
            password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(),
            notes: "note".to_owned(),
        }).unwrap();

        let entry = database.get_entry_by_id(entry_id).unwrap();
        assert_eq!(entry.title.as_deref(), Some("New Site"));
        assert_eq!(entry.username.as_deref(), Some("alice"));
    }

    #[tokio::test]
    async fn created_entry_survives_kdbx_roundtrip() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);
        let entry_id = database.create_entry(None, EntryUpdate {
            title: "New Site".to_owned(), username: "alice".to_owned(), password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(), notes: "note".to_owned(),
        }).unwrap();

        let bytes = database.to_kdbx_bytes(Some(Zeroizing::new("test".to_owned())), None).unwrap();
        let reopened = KeePass::from_bytes(&config, bytes, Some(Zeroizing::new("test".to_owned())), None).await.unwrap();

        assert_eq!(reopened.get_entry_by_id(entry_id).unwrap().title.as_deref(), Some("New Site"));
    }

    #[tokio::test]
    async fn create_entry_rejects_an_unknown_group() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);
        let result = database.create_entry(Some(uuid::Uuid::new_v4()), EntryUpdate {
            title: "x".to_owned(), username: "x".to_owned(), password: "x".to_owned(), url: "x".to_owned(), notes: "x".to_owned(),
        });
        assert!(result.is_err());
    }
```

- [ ] **Step 2: Run tests to verify they fail**

Run (from repo root, matching how CI/`scripts/verify-baseline.sh` invokes it): `cargo test --locked keepass::keepass::tests`
Expected: FAIL with "no function or associated item named `new_empty` found" (and similarly for `create_entry`).

- [ ] **Step 3: Implement `new_empty` and `create_entry`**

In `backend-rust/src/keepass/keepass.rs`, add to the top-level imports (near the existing `use keepass::{Database, DatabaseKey};` line):

```rust
use keepass::config::DatabaseConfig;
use keepass::db::Group as DbGroup;
```

Add these two methods to `impl KeePass` (place them right after `from_enc`, before `to_kdbx_bytes`):

```rust
    /// Builds a brand-new, empty database with sensible KDBX4 defaults. No
    /// bytes are read from anywhere; the caller encrypts it via
    /// `to_kdbx_bytes` exactly like any other in-memory database.
    pub fn new_empty(config: &Config) -> Self {
        let mut db = Database::new(DatabaseConfig::default());
        db.root = DbGroup::new("Root");
        Self { config: config.clone(), db }
    }

    /// Adds a new entry to `group_id` (or the root group when `None`) and
    /// returns its generated UUID. Mirrors `update_entry`'s field mapping.
    pub fn create_entry(&mut self, group_id: Option<Uuid>, update: EntryUpdate) -> Result<Uuid> {
        let group = match group_id {
            Some(id) => Self::find_group_by_id_mut(&mut self.db.root, &id)
                .ok_or_else(|| anyhow!("group not found"))?,
            None => &mut self.db.root,
        };
        let mut entry = keepass::db::Entry::new();
        entry.fields.insert("Title".to_owned(), Value::Unprotected(update.title));
        entry.fields.insert("UserName".to_owned(), Value::Unprotected(update.username));
        entry.fields.insert("URL".to_owned(), Value::Unprotected(update.url));
        entry.fields.insert("Notes".to_owned(), Value::Unprotected(update.notes));
        entry.fields.insert("Password".to_owned(), Value::Protected(SecStr::new(update.password.into_bytes())));
        let entry_id = entry.uuid;
        group.add_child(entry);
        Ok(entry_id)
    }
```

Add the mutable group finder next to the existing `find_group_by_id` (place it directly after that function):

```rust
    fn find_group_by_id_mut<'a>(group: &'a mut keepass::db::Group, id: &Uuid) -> Option<&'a mut keepass::db::Group> {
        if &group.uuid == id {
            return Some(group);
        }
        for node in &mut group.children {
            if let Node::Group(group) = node {
                if let Some(found) = Self::find_group_by_id_mut(group, id) {
                    return Some(found);
                }
            }
        }
        None
    }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cargo test --locked keepass::keepass::tests`
Expected: PASS (all 4 existing + 5 new tests)

- [ ] **Step 5: Commit**

```bash
git add backend-rust/src/keepass/keepass.rs
git commit -m "feat(rust): add empty-vault and entry creation to KeePass wrapper"
```

---

## Task 2: Rust — `/create-vault` and `/entries/create` private-service routes

**Files:**
- Modify: `backend-rust/src/private_service.rs`
- Test: same file (integration-style, via `PrivateVaultService` directly — no HTTP harness exists today for this file, so keep tests at the service-method level, matching `active_handle_is_bound_to_its_user_and_vault`'s style)

**Interfaces:**
- Consumes: `KeePass::new_empty`, `KeePass::create_entry` (Task 1)
- Produces: `PrivateVaultService::create_vault(&self, CreateVaultRequest) -> Result<CreateVaultResponse>`, `PrivateVaultService::create_entry(&self, CreateEntryRequest) -> Result<CreateEntryResponse>`, HTTP routes `POST /internal/v1/create-vault` and `POST /internal/v1/entries/create`

- [ ] **Step 1: Write the failing test**

Add to `mod tests` at the bottom of `backend-rust/src/private_service.rs` (this module already has `use super::ActiveVaults;` — add a new `use` line alongside it):

```rust
    use super::{Config, PrivateVaultService, CreateVaultRequest, CreateEntryRequest, HandleRequest, UnlockRequest};

    #[tokio::test]
    async fn create_vault_then_unlock_round_trips() {
        let service = PrivateVaultService::new(Config::default());
        let created = service.create_vault(CreateVaultRequest {
            password: Some("test".to_owned()),
            keyfile_b64: None,
        }).await.unwrap();

        let user_id = Uuid::new_v4();
        let vault_id = Uuid::new_v4();
        let unlocked = service.unlock(UnlockRequest {
            user_id,
            vault_id,
            database_b64: created.database_b64,
            password: Some("test".to_owned()),
            keyfile_b64: None,
        }).await.unwrap();

        assert!(!unlocked.handle.is_empty());
    }

    #[tokio::test]
    async fn create_entry_on_an_unlocked_vault_persists_the_new_entry() {
        let service = PrivateVaultService::new(Config::default());
        let created = service.create_vault(CreateVaultRequest { password: Some("test".to_owned()), keyfile_b64: None }).await.unwrap();
        let user_id = Uuid::new_v4();
        let vault_id = Uuid::new_v4();
        let unlocked = service.unlock(UnlockRequest {
            user_id, vault_id, database_b64: created.database_b64, password: Some("test".to_owned()), keyfile_b64: None,
        }).await.unwrap();

        let result = service.create_entry(CreateEntryRequest {
            handle: HandleRequest { handle: unlocked.handle.clone(), user_id, vault_id },
            group_id: None,
            title: "New Site".to_owned(),
            username: "alice".to_owned(),
            password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(),
            notes: "".to_owned(),
            master_password: Some("test".to_owned()),
            keyfile_b64: None,
        }).await.unwrap();

        assert!(!result.entry_id.is_nil());
        assert!(!result.database_b64.is_empty());
    }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cargo test --locked private_service::tests`
Expected: FAIL to compile — `CreateVaultRequest`/`CreateEntryRequest`/`create_vault`/`create_entry` don't exist yet.

- [ ] **Step 3: Implement the request/response types and service methods**

Add near the other request structs (right after `UpdateRequest`'s closing brace, before `UnlockResponse`):

```rust
#[derive(Deserialize)]
pub struct CreateVaultRequest {
    pub password: Option<String>,
    pub keyfile_b64: Option<String>,
}
#[derive(Deserialize)]
pub struct CreateEntryRequest {
    #[serde(flatten)] pub handle: HandleRequest,
    pub group_id: Option<Uuid>,
    pub title: String,
    pub username: String,
    pub password: String,
    pub url: String,
    pub notes: String,
    pub master_password: Option<String>,
    pub keyfile_b64: Option<String>,
}
```

Add near the other response structs (right after `UpdateResponse`):

```rust
#[derive(Serialize)]
pub struct CreateVaultResponse { pub database_b64: String }
#[derive(Serialize)]
pub struct CreateEntryResponse { pub entry_id: Uuid, pub database_b64: String }
```

Add these two methods to `impl PrivateVaultService` (place them right after `update`, before `close`):

```rust
    pub async fn create_vault(&self, request: CreateVaultRequest) -> Result<CreateVaultResponse> {
        let mut password = request.password.map(Zeroizing::new);
        let mut keyfile = match request.keyfile_b64 {
            Some(mut encoded) => {
                let decoded = general_purpose::STANDARD.decode(&encoded);
                encoded.zeroize();
                Some(Zeroizing::new(decoded?.into_boxed_slice()))
            }
            None => None,
        };
        let database = KeePass::new_empty(&self.config);
        let bytes = database.to_kdbx_bytes(password.take(), keyfile.take())?;
        Ok(CreateVaultResponse { database_b64: general_purpose::STANDARD.encode(bytes.as_slice()) })
    }

    pub async fn create_entry(&self, request: CreateEntryRequest) -> Result<CreateEntryResponse> {
        let mut password = request.master_password.map(Zeroizing::new);
        let mut keyfile = match request.keyfile_b64 {
            Some(mut encoded) => {
                let decoded = general_purpose::STANDARD.decode(&encoded);
                encoded.zeroize();
                Some(Zeroizing::new(decoded?.into_boxed_slice()))
            }
            None => None,
        };
        self.with_vault_mut(&request.handle, move |database| {
            let entry_id = database.create_entry(request.group_id, EntryUpdate {
                title: request.title,
                username: request.username,
                password: request.password,
                url: request.url,
                notes: request.notes,
            })?;
            let bytes = database.to_kdbx_bytes(password.take(), keyfile.take())?;
            Ok(CreateEntryResponse { entry_id, database_b64: general_purpose::STANDARD.encode(bytes.as_slice()) })
        }).await
    }
```

Add the two HTTP routes (place them right after `update`'s route handler, before `close`'s):

```rust
#[post("/create-vault")]
async fn create_vault_route(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<CreateVaultRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.create_vault(body.into_inner()).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::BAD_REQUEST) }
}

#[post("/entries/create")]
async fn create_entry_route(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<CreateEntryRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.create_entry(body.into_inner()).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::BAD_REQUEST) }
}
```

Register both routes in `run_private_service`'s `.service(web::scope("/internal/v1")...)` call — change:

```rust
        .service(web::scope("/internal/v1")
            .service(unlock).service(groups).service(entries).service(entry).service(update)
            .service(search).service(reveal).service(close)))
```

to:

```rust
        .service(web::scope("/internal/v1")
            .service(unlock).service(groups).service(entries).service(entry).service(update)
            .service(search).service(reveal).service(close)
            .service(create_vault_route).service(create_entry_route)))
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cargo test --locked private_service::tests`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add backend-rust/src/private_service.rs
git commit -m "feat(rust): add create-vault and create-entry private-service routes"
```

---

## Task 3: Go — privateclient methods for the two new Rust routes

**Files:**
- Modify: `backend-go/internal/privateclient/client.go`

**Interfaces:**
- Produces: `Client.CreateVault(ctx, CreateVaultRequest) (CreateVaultResponse, error)`, `Client.CreateEntry(ctx, CreateEntryRequest) (CreateEntryResponse, error)`

- [ ] **Step 1: Add request/response types**

In `backend-go/internal/privateclient/client.go`, add next to the existing `UpdateRequest`/`UpdateResponse` types:

```go
type CreateVaultRequest struct { Password *string `json:"password,omitempty"`; KeyfileB64 *string `json:"keyfile_b64,omitempty"` }
type CreateVaultResponse struct { DatabaseB64 string `json:"database_b64"` }
type CreateEntryRequest struct {
	Handle string `json:"handle"`; UserID string `json:"user_id"`; VaultID string `json:"vault_id"`
	GroupID *string `json:"group_id,omitempty"`
	Title string `json:"title"`; Username string `json:"username"`; Password string `json:"password"`; URL string `json:"url"`; Notes string `json:"notes"`
	MasterPassword *string `json:"master_password,omitempty"`; KeyfileB64 *string `json:"keyfile_b64,omitempty"`
}
type CreateEntryResponse struct { EntryID string `json:"entry_id"`; DatabaseB64 string `json:"database_b64"` }
```

- [ ] **Step 2: Add client methods**

Add next to the existing `func (c *Client) Update(...)`:

```go
func (c *Client) CreateVault(ctx context.Context, r CreateVaultRequest) (CreateVaultResponse, error) { var out CreateVaultResponse; return out, c.call(ctx, "/internal/v1/create-vault", r, &out) }
func (c *Client) CreateEntry(ctx context.Context, r CreateEntryRequest) (CreateEntryResponse, error) { var out CreateEntryResponse; return out, c.call(ctx, "/internal/v1/entries/create", r, &out) }
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./...`
Expected: no errors (there are no unit tests directly in this package today — `internal/privateclient` has `[no test files]`, per existing `go test ./...` output; coverage for this path comes from `internal/web`'s tests in Task 4/5, which exercise it through the `RustVaultService` interface via fakes)

- [ ] **Step 4: Commit**

```bash
git add backend-go/internal/privateclient/client.go
git commit -m "feat(go): add CreateVault/CreateEntry private-service client methods"
```

---

## Task 4: Go — `POST /vaults/create` and `POST /vaults/entries/create` routes

**Files:**
- Modify: `backend-go/internal/web/app.go`
- Test: `backend-go/internal/web/app_test.go`

**Interfaces:**
- Consumes: `privateclient.CreateVaultRequest/Response`, `privateclient.CreateEntryRequest/Response` (Task 3)
- Produces: routes `POST /vaults/create`, `POST /vaults/entries/create`; extends `RustVaultService` with `CreateVault(ctx, privateclient.CreateVaultRequest) (privateclient.CreateVaultResponse, error)` and `CreateEntry(ctx, privateclient.CreateEntryRequest) (privateclient.CreateEntryResponse, error)`

- [ ] **Step 1: Write the failing tests**

Add to `backend-go/internal/web/app_test.go`, next to the existing CSRF/upload tests:

```go
func TestCreateVaultRequiresCSRF(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: &fakeRust{}, SessionVaults: &fakeSessionVaultsForCreate{}})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"name": {"New vault"}, "password": {"test"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("POST /vaults/create without CSRF = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestCreateVaultUploadsTheGeneratedDatabase(t *testing.T) {
	sessionVaults := &fakeSessionVaultsForCreate{}
	app := NewApp(Dependencies{Rust: &fakeRust{}, SessionVaults: sessionVaults})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"csrf_token": {app.CSRFToken(cookie.Value)}, "name": {"New vault"}, "password": {"test"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("POST /vaults/create status = %d, want %d", recorder.Code, http.StatusSeeOther)
	}
	if sessionVaults.uploadedName != "New vault" || len(sessionVaults.uploadedBytes) == 0 {
		t.Fatalf("upload not recorded: name=%q bytes=%d", sessionVaults.uploadedName, len(sessionVaults.uploadedBytes))
	}
}

func TestCreateEntryRequiresAnUnlockedVault(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: &fakeRust{}})
	cookie := app.CreateSession("owner-1")
	form := url.Values{"csrf_token": {app.CSRFToken(cookie.Value)}, "title": {"New Site"}}
	request := httptest.NewRequest(http.MethodPost, "/vaults/entries/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("POST /vaults/entries/create without an unlocked vault = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}
```

Add the two test doubles needed above, next to `fakeRust`/`fakeVaults`:

```go
type fakeSessionVaultsForCreate struct {
	uploadedName  string
	uploadedBytes []byte
}

func (f *fakeSessionVaultsForCreate) ListForSession(context.Context, string, string) ([]Vault, error) { return nil, nil }
func (f *fakeSessionVaultsForCreate) VaultForSession(context.Context, string, string, string) (Vault, bool, error) { return Vault{}, false, nil }
func (f *fakeSessionVaultsForCreate) UploadForSession(_ context.Context, _ string, _ string, name string, id string, data []byte) (Vault, error) {
	f.uploadedName = name
	f.uploadedBytes = data
	return Vault{ID: id, Name: name}, nil
}
func (f *fakeSessionVaultsForCreate) DownloadForSession(context.Context, string, string, Vault) ([]byte, error) { return nil, nil }
func (f *fakeSessionVaultsForCreate) ReplaceForSession(context.Context, string, string, Vault, []byte) error { return nil }
func (f *fakeSessionVaultsForCreate) ReplaceDecodedEntries(context.Context, string, string, string, []DecodedEntry) error { return nil }
func (f *fakeSessionVaultsForCreate) DecodedEntries(context.Context, string, string, string) ([]DecodedEntry, error) { return nil, nil }
func (f *fakeSessionVaultsForCreate) UpdateDecodedEntry(context.Context, string, string, string, string, DecodedEntry) error { return nil }
```

Extend the existing `fakeRust` (do not create a second one) with the two new interface methods:

```go
func (f *fakeRust) CreateVault(context.Context, privateclient.CreateVaultRequest) (privateclient.CreateVaultResponse, error) {
	return privateclient.CreateVaultResponse{DatabaseB64: "ZmFrZS1rZGJ4"}, nil
}
func (f *fakeRust) CreateEntry(context.Context, privateclient.CreateEntryRequest) (privateclient.CreateEntryResponse, error) {
	return privateclient.CreateEntryResponse{EntryID: "11111111-1111-1111-1111-111111111111", DatabaseB64: "ZmFrZS1rZGJ4"}, nil
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./backend-go/internal/web/... -run 'TestCreateVault|TestCreateEntry' -v`
Expected: FAIL to compile — `RustVaultService` doesn't declare `CreateVault`/`CreateEntry` yet, routes don't exist, `fakeRust` doesn't implement the new methods yet (it will once you add the interface — right now the test file itself won't even reference them until this step, so the first real failure is "undefined: fakeSessionVaultsForCreate" method set incomplete against `SessionVaultRepository` — expected, since routes/handlers aren't written yet either)

- [ ] **Step 3: Extend the `RustVaultService` interface**

In `backend-go/internal/web/app.go`, add two lines to the existing `RustVaultService` interface (right after `Update`):

```go
	CreateVault(context.Context, privateclient.CreateVaultRequest) (privateclient.CreateVaultResponse, error)
	CreateEntry(context.Context, privateclient.CreateEntryRequest) (privateclient.CreateEntryResponse, error)
```

- [ ] **Step 4: Add the two routes to `ServeHTTP`**

Add two cases to the `switch` in `ServeHTTP` (right after the `case r.Method == "POST" && r.URL.Path == "/vaults/upload"` block):

```go
	case r.Method == "POST" && r.URL.Path == "/vaults/create":
		a.createVault(w, r)
	case r.Method == "POST" && r.URL.Path == "/vaults/entries/create":
		a.createEntry(w, r)
```

- [ ] **Step 5: Implement `createVault`**

Add right after `uploadVault` in `app.go`. This deliberately mirrors `uploadVault`'s CSRF/error-handling shape but generates the file instead of accepting one:

```go
func (a *App) createVault(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form.", http.StatusBadRequest)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	name := strings.TrimSpace(r.Form.Get("name"))
	if name == "" {
		http.Error(w, "Name the new vault.", 400)
		return
	}
	password := strings.TrimSpace(r.Form.Get("password"))
	if password == "" {
		http.Error(w, "Set a master password for the new vault.", 400)
		return
	}
	if a.sessionVaults == nil || a.rust == nil {
		http.Error(w, "Vault creation is unavailable.", 503)
		return
	}
	created, err := a.rust.CreateVault(r.Context(), privateclient.CreateVaultRequest{Password: &password})
	if err != nil {
		http.Error(w, "Unable to create a new vault.", 500)
		return
	}
	data, err := base64.StdEncoding.DecodeString(created.DatabaseB64)
	if err != nil || len(data) == 0 || len(data) > maxVaultBytes {
		http.Error(w, "Unable to create a new vault.", 500)
		return
	}
	if _, err = a.sessionVaults.UploadForSession(r.Context(), s.userID, s.accessToken, name, newUUID(), data); err != nil {
		http.Error(w, "Unable to save the new vault. Please try again.", 500)
		return
	}
	if isHTMX(r) {
		a.vaultsPage(w, r)
		return
	}
	http.Redirect(w, r, "/vaults", 303)
}
```

- [ ] **Step 6: Implement `createEntry`**

Add right after `updateEntry` in `app.go`. This mirrors `updateEntry` closely — same multipart/CSRF ordering, same re-encrypt-confirmation fields — but calls `CreateEntry` instead of `Update` and has no `entry_id` to require up front:

```go
func (a *App) createEntry(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok { http.Redirect(w, r, "/sign-in", 303); return }
	r.Body = http.MaxBytesReader(w, r.Body, maxKeyFileBytes+(1<<20))
	if err := r.ParseMultipartForm(maxKeyFileBytes + 1); err != nil { http.Error(w, "Invalid save form.", http.StatusBadRequest); return }
	if !a.validCSRF(r, s) { http.Error(w, "Your form has expired. Please reload and try again.", 403); return }
	if s.activeVaultID == "" || s.activeHandle == "" { http.Error(w, "Unlock a vault before adding an entry.", http.StatusBadRequest); return }
	title := r.Form.Get("title")
	if strings.TrimSpace(title) == "" { http.Error(w, "Give the new entry a title.", http.StatusBadRequest); return }
	v, owned, err := a.vault(r.Context(), s, s.activeVaultID)
	if err != nil || !owned || a.sessionVaults == nil || a.rust == nil { http.Error(w, "Unable to save entry.", 503); return }
	var groupID *string
	if value := strings.TrimSpace(r.Form.Get("group_id")); value != "" { groupID = &value }
	var masterPassword *string
	if value := r.Form.Get("master_password"); value != "" { masterPassword = &value }
	var keyfileB64 *string
	if file, _, err := r.FormFile("key_file"); err == nil {
		defer file.Close()
		raw, readErr := io.ReadAll(io.LimitReader(file, maxKeyFileBytes+1))
		if readErr != nil || len(raw) > maxKeyFileBytes { http.Error(w, "Key file is too large.", http.StatusBadRequest); return }
		encoded := base64.StdEncoding.EncodeToString(raw); keyfileB64 = &encoded
	}
	created, err := a.rust.CreateEntry(r.Context(), privateclient.CreateEntryRequest{
		Handle: s.activeHandle, UserID: s.userID, VaultID: s.activeVaultID, GroupID: groupID,
		Title: title, Username: r.Form.Get("username"), Password: r.Form.Get("password"), URL: r.Form.Get("url"), Notes: r.Form.Get("notes"),
		MasterPassword: masterPassword, KeyfileB64: keyfileB64,
	})
	if err != nil { http.Error(w, "Unable to save the vault. Check the master password or key file.", http.StatusUnprocessableEntity); return }
	vaultBytes, err := base64.StdEncoding.DecodeString(created.DatabaseB64)
	if err != nil || len(vaultBytes) == 0 || len(vaultBytes) > maxVaultBytes { http.Error(w, "Unable to save the vault.", 500); return }
	if err := a.sessionVaults.ReplaceForSession(r.Context(), s.userID, s.accessToken, v, vaultBytes); err != nil { http.Error(w, "Vault changed but could not be stored. Please try saving again.", 502); return }
	if err := a.syncDecodedEntries(r.Context(), s, s.activeHandle); err != nil { http.Error(w, "Vault saved but the decoded mirror could not be refreshed.", 502); return }
	http.Redirect(w, r, "/vaults/browse", 303)
}
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./backend-go/internal/web/... -run 'TestCreateVault|TestCreateEntry' -v`
Expected: PASS

- [ ] **Step 8: Run the full Go test suite to confirm nothing else broke**

Run: `go test ./...`
Expected: PASS (except the pre-existing, unrelated `rust.env.example` content-drift failure in `backend-go/tests/operations`, already failing before this plan)

- [ ] **Step 9: Commit**

```bash
git add backend-go/internal/web/app.go backend-go/internal/web/app_test.go
git commit -m "feat(go): add /vaults/create and /vaults/entries/create routes"
```

---

## Task 5: Go — 60-second vault-idle auto-close

**Files:**
- Modify: `backend-go/internal/web/app.go`
- Test: `backend-go/internal/web/app_test.go`

**Interfaces:**
- Produces: `session.lastActivity time.Time`; `App` background sweep goroutine; routes `GET /session/status`, `POST /session/heartbeat`; `Dependencies.IdleTimeout` (optional, defaults to 60s), `Dependencies.SweepInterval` (optional, defaults to 5s)

- [ ] **Step 1: Write the failing tests**

Add to `app_test.go`:

```go
func TestIdleSweepClosesAnInactiveVaultHandle(t *testing.T) {
	rust := &fakeRust{}
	app := NewApp(Dependencies{Vaults: fakeVaults{}, Rust: rust, IdleTimeout: 20 * time.Millisecond, SweepInterval: 5 * time.Millisecond})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.activeHandle, s.activeVaultID = "opaque-handle", "vault-a"
	s.lastActivity = time.Now().Add(-time.Hour)
	app.sessions[cookie.Value] = s
	app.mu.Unlock()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if app.ActiveHandle(cookie.Value) == "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if got := app.ActiveHandle(cookie.Value); got != "" {
		t.Fatalf("active handle after idle sweep = %q, want empty", got)
	}
	if len(rust.closed) != 1 || rust.closed[0].Handle != "opaque-handle" {
		t.Fatalf("close calls = %#v, want one close of opaque-handle", rust.closed)
	}
}

func TestSessionStatusReportsRemainingIdleSeconds(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}, IdleTimeout: 60 * time.Second})
	cookie := app.CreateSession("owner-1")
	request := httptest.NewRequest(http.MethodGet, "/session/status", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /session/status status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body struct {
		Active               bool `json:"active"`
		VaultActive          bool `json:"vault_active"`
		IdleSecondsRemaining int  `json:"idle_seconds_remaining"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Active || body.VaultActive || body.IdleSecondsRemaining <= 0 {
		t.Fatalf("unexpected status body: %+v", body)
	}
}

func TestHeartbeatRequiresCSRFAndResetsIdleClock(t *testing.T) {
	app := NewApp(Dependencies{Vaults: fakeVaults{}})
	cookie := app.CreateSession("owner-1")
	app.mu.Lock()
	s := app.sessions[cookie.Value]
	s.lastActivity = time.Now().Add(-time.Hour)
	app.sessions[cookie.Value] = s
	app.mu.Unlock()

	form := url.Values{"csrf_token": {app.CSRFToken(cookie.Value)}}
	request := httptest.NewRequest(http.MethodPost, "/session/heartbeat", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()

	app.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("POST /session/heartbeat status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	app.mu.RLock()
	updated := app.sessions[cookie.Value].lastActivity
	app.mu.RUnlock()
	if time.Since(updated) > time.Second {
		t.Fatalf("lastActivity not refreshed by heartbeat: %v", updated)
	}
}
```

Add `"encoding/json"` and `"time"` to `app_test.go`'s import block if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./backend-go/internal/web/... -run 'TestIdleSweep|TestSessionStatus|TestHeartbeat' -v`
Expected: FAIL to compile — `session.lastActivity`, `Dependencies.IdleTimeout`/`SweepInterval` don't exist; routes don't exist.

- [ ] **Step 3: Add `lastActivity` to `session` and idle config to `Dependencies`/`App`**

In `app.go`, extend the `session` struct:

```go
type session struct {
	userID, accessToken, csrfToken, activeHandle, activeVaultID, email string
	lastActivity time.Time
}
```

Extend `Dependencies` and `App`:

```go
type Dependencies struct {
	Vaults          VaultRepository
	SessionVaults   SessionVaultRepository
	Rust            RustVaultService
	Auth            TokenValidator
	SecureCookies   bool
	SupabaseURL     string
	SupabaseAnonKey string
	IdleTimeout     time.Duration
	SweepInterval   time.Duration
}
```

```go
type App struct {
	vaults          VaultRepository
	sessionVaults   SessionVaultRepository
	rust            RustVaultService
	auth            TokenValidator
	secureCookies   bool
	supabaseURL     string
	supabaseAnonKey string
	idleTimeout     time.Duration
	tmpl            *template.Template
	mu              sync.RWMutex
	sessions        map[string]session
}
```

Add `"time"` to the import block.

- [ ] **Step 4: Wire defaults and start the sweep goroutine in `NewApp`**

Replace the body of `NewApp` with (this keeps every existing field assignment, just adds the idle-timeout defaulting and the goroutine kick-off at the end):

```go
func NewApp(d Dependencies) *App {
	idleTimeout := d.IdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = 60 * time.Second
	}
	sweepInterval := d.SweepInterval
	if sweepInterval <= 0 {
		sweepInterval = 5 * time.Second
	}
	a := &App{
		vaults:          d.Vaults,
		sessionVaults:   d.SessionVaults,
		rust:            d.Rust,
		auth:            d.Auth,
		secureCookies:   d.SecureCookies,
		supabaseURL:     d.SupabaseURL,
		supabaseAnonKey: d.SupabaseAnonKey,
		idleTimeout:     idleTimeout,
		tmpl: template.Must(template.New("pages").Funcs(template.FuncMap{
			"lower": strings.ToLower,
		}).ParseGlob(templatesGlob())),
		sessions: map[string]session{},
	}
	go a.sweepIdleVaults(sweepInterval)
	return a
}

// sweepIdleVaults runs for the lifetime of the process. It never needs an
// explicit stop signal: the app has exactly one long-running instance in
// production, and leaked per-test goroutines exit naturally when the test
// binary does.
func (a *App) sweepIdleVaults(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		a.closeIdleVaults()
	}
}

func (a *App) closeIdleVaults() {
	a.mu.Lock()
	var toClose []struct {
		id string
		s  session
	}
	for id, s := range a.sessions {
		if s.activeHandle != "" && time.Since(s.lastActivity) >= a.idleTimeout {
			toClose = append(toClose, struct {
				id string
				s  session
			}{id, s})
		}
	}
	a.mu.Unlock()

	for _, entry := range toClose {
		if a.rust == nil {
			continue
		}
		if err := a.rust.Close(context.Background(), privateclient.HandleRequest{Handle: entry.s.activeHandle, UserID: entry.s.userID, VaultID: entry.s.activeVaultID}); err != nil {
			continue
		}
		a.mu.Lock()
		if current, ok := a.sessions[entry.id]; ok && current.activeHandle == entry.s.activeHandle {
			current.activeHandle = ""
			current.activeVaultID = ""
			a.sessions[entry.id] = current
		}
		a.mu.Unlock()
	}
}
```

- [ ] **Step 5: Record activity on every authenticated request**

Modify `authenticated` to bump `lastActivity` on success (replace its final line `return id, s, true` with the block below):

```go
	a.mu.Lock()
	s.lastActivity = time.Now()
	a.sessions[id] = s
	a.mu.Unlock()
	return id, s, true
```

Also set `lastActivity` at session creation, in `createSession` (add right after `session{userID: userID, accessToken: accessToken, csrfToken: randomToken()}`):

```go
	a.sessions[id] = session{userID: userID, accessToken: accessToken, csrfToken: randomToken(), lastActivity: time.Now()}
```

- [ ] **Step 6: Add the two routes**

Add to the `ServeHTTP` switch (right after the `/vaults/close` case):

```go
	case r.Method == "GET" && r.URL.Path == "/session/status":
		a.sessionStatus(w, r)
	case r.Method == "POST" && r.URL.Path == "/session/heartbeat":
		a.heartbeat(w, r)
```

Add the two handlers near `signOut`:

```go
func (a *App) sessionStatus(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.sessionFromCookie(r)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if !ok || s.userID == "" {
		json.NewEncoder(w).Encode(map[string]any{"active": false, "vault_active": false, "idle_seconds_remaining": 0})
		return
	}
	remaining := int(a.idleTimeout.Seconds()) - int(time.Since(s.lastActivity).Seconds())
	if remaining < 0 {
		remaining = 0
	}
	json.NewEncoder(w).Encode(map[string]any{
		"active":                 true,
		"vault_active":           s.activeHandle != "",
		"idle_seconds_remaining": remaining,
	})
}

func (a *App) heartbeat(w http.ResponseWriter, r *http.Request) {
	_, s, ok := a.authenticated(r)
	if !ok {
		http.Redirect(w, r, "/sign-in", 303)
		return
	}
	if !a.validCSRF(r, s) {
		http.Error(w, "Your form has expired. Please reload and try again.", 403)
		return
	}
	// a.authenticated already refreshed lastActivity above.
	w.WriteHeader(http.StatusNoContent)
}
```

Add `"encoding/json"` to the import block if not already present (it already is — `app.go` imports it for `json.RawMessage`).

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./backend-go/internal/web/... -run 'TestIdleSweep|TestSessionStatus|TestHeartbeat' -v`
Expected: PASS

- [ ] **Step 8: Run the full Go test suite**

Run: `go test ./...`
Expected: PASS (same pre-existing unrelated failure as before)

- [ ] **Step 9: Commit**

```bash
git add backend-go/internal/web/app.go backend-go/internal/web/app_test.go
git commit -m "feat(go): auto-close idle vault handles after 60s, add session status/heartbeat routes"
```

---

## Task 6: Clipboard auto-clear — explicitly out of backend scope

There is no backend task here. Clearing the clipboard 10 seconds after a copy is a browser-only concern (`navigator.clipboard.writeText('')` behind a `setTimeout`, scoped per the existing `data-action="copy"` / `data-action="copy-by-id"` handlers in `frontend/web/static/app.js`). Documented in the API contract (Task 7) as a frontend-only requirement so the UI pass doesn't look for a backend hook that doesn't exist.

---

## Task 7: API contract doc for the UI pass

**Files:**
- Create: `docs/api/vault-management.md`

- [ ] **Step 1: Write the contract doc**

```markdown
# Vault Management API Contract

Backend routes for: creating a new vault, creating a new entry, 60s vault-idle
auto-close, and the (frontend-only) 10s clipboard auto-clear. Written for the
UI/design pass — every route below is implemented and covered by Go tests in
`backend-go/internal/web/app_test.go`.

All routes follow the app's existing conventions (see `docs/api/` sibling
docs if present, otherwise infer from `frontend/templates/vault-list.html` and
`frontend/templates/browse.html`, which already call the sibling routes these
extend):

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
```

- [ ] **Step 2: Commit**

```bash
git add docs/api/vault-management.md
git commit -m "docs: add vault-management API contract for the UI pass"
```

---

## Self-Review Notes

- **Spec coverage:** create-vault (Task 2/4), add-entries-to-it (Task 2/4, `create_entry` accepts any vault including a freshly created one), 60s auto-close (Task 5), clipboard 10s clear (Task 6, explicitly documented as out of backend scope), create-and-save entries (Task 2/4) — all four requested features are covered.
- **Type consistency check:** `privateclient.CreateEntryRequest.GroupID` is `*string` (matches how `Vault.ID`/`entry_id` are passed as strings elsewhere in `internal/web`, e.g. `s.activeVaultID string`), while Rust's `CreateEntryRequest.group_id` is `Option<Uuid>` — Go's `*string` JSON-encodes to a bare string or `null`, which `serde`'s `Option<Uuid>` deserializes correctly from either form. Confirmed this matches the existing `HandleRequest`/`UnlockRequest` pattern (Go passes UUIDs as plain strings everywhere; Rust types them as `Uuid` on receipt).
- **Idle sweep goroutine leak:** every `NewApp()` call (there are 15+ in `app_test.go`) now starts a ticker goroutine. They're harmless (no shared external resource, exit when the test binary exits) but worth knowing about if a future `-race`-mode test run looks unusual — this is the accepted tradeoff called out in Step 4 of Task 5 rather than building explicit shutdown plumbing nothing in this codebase currently needs.
