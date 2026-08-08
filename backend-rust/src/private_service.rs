use std::collections::HashMap;
use std::net::SocketAddr;
use std::time::{Duration, Instant};

use actix_web::{HttpRequest, HttpResponse, HttpServer, Responder, post, web};
use actix_web::dev::Service;
use actix_web::error::InternalError;
use anyhow::{anyhow, Result};
use base64::{Engine, engine::general_purpose};
use constant_time_eq::constant_time_eq;
use rand::distributions::{Alphanumeric, DistString};
use rand::thread_rng;
use secrecy::ExposeSecret;
use serde::{Deserialize, Serialize};
use tokio::sync::{Mutex, RwLock};
use uuid::Uuid;
use zeroize::{Zeroize, Zeroizing};

use crate::config::config::Config;
use crate::keepass::encrypted::Encrypted;
use crate::keepass::{Entry, EntryGroup, Group};
use crate::keepass::keepass::{EntryUpdate, KeePass};
use crate::keepass::key::SecretKey;
use crate::observability;

/// Go permits encrypted KDBX objects up to 50 MiB. JSON/base64 transport adds
/// approximately one third, so the listener allows 70 MiB then enforces the
/// exact decoded-object limit before any KDBX parsing occurs.
const MAX_DECODED_VAULT_BYTES: usize = 50 * 1024 * 1024;
const MAX_PRIVATE_BODY_BYTES: usize = 70 * 1024 * 1024;

fn vault_size_allowed(bytes: usize) -> bool {
    bytes <= MAX_DECODED_VAULT_BYTES
}

/// Opaque handle metadata for a vault that has been successfully opened.
pub struct ActiveVaults {
    handles: RwLock<std::collections::HashMap<String, ActiveVault>>,
}

/// The credential supplied by the Go service on every private request.
/// It is intentionally distinct from browser authentication and never carries
/// a user or vault identity.
#[derive(Clone)]
pub struct ServiceCredential(Box<str>);

impl ServiceCredential {
    pub fn new(value: impl Into<Box<str>>) -> Self {
        Self(value.into())
    }

    pub fn verify_bearer(&self, authorization: Option<&str>) -> Result<()> {
        let presented = authorization
            .and_then(|value| value.strip_prefix("Bearer "))
            .ok_or_else(|| anyhow!("private service authentication failed"))?;

        if self.0.is_empty() || !constant_time_eq(self.0.as_bytes(), presented.as_bytes()) {
            return Err(anyhow!("private service authentication failed"));
        }
        Ok(())
    }
}

struct ActiveVault {
    user_id: Uuid,
    vault_id: Uuid,
    expiry: Instant,
}

impl ActiveVaults {
    pub fn new() -> Self {
        Self {
            handles: RwLock::new(std::collections::HashMap::new()),
        }
    }

    pub async fn open(&self, user_id: Uuid, vault_id: Uuid, timeout: Duration) -> String {
        let handle = Alphanumeric.sample_string(&mut thread_rng(), 48);
        self.handles.write().await.insert(handle.clone(), ActiveVault {
            user_id,
            vault_id,
            expiry: Instant::now() + timeout,
        });
        handle
    }

    pub async fn authorize(&self, handle: &str, user_id: Uuid, vault_id: Uuid) -> Result<()> {
        let mut handles = self.handles.write().await;
        let active = handles.get(handle).ok_or_else(|| anyhow!("active vault not found"))?;
        let expired = Instant::now() >= active.expiry;
        let owner_mismatch = active.user_id != user_id || active.vault_id != vault_id;
        if expired || owner_mismatch {
            // An expired or replayed handle is no longer usable. Removing it here
            // prevents a later request from reviving encrypted vault state.
            handles.remove(handle);
        }
        if expired {
            return Err(anyhow!("active vault expired"));
        }
        if owner_mismatch {
            return Err(anyhow!("active vault owner mismatch"));
        }
        Ok(())
    }

    pub async fn close(&self, handle: &str, user_id: Uuid, vault_id: Uuid) -> Result<()> {
        self.authorize(handle, user_id, vault_id).await?;
        self.handles.write().await.remove(handle);
        Ok(())
    }
}

/// Configuration for the separate Go-to-Rust listener. It deliberately
/// accepts only a loopback address; a public ingress cannot be configured.
#[derive(Clone)]
pub struct PrivateServiceConfig {
    pub bind: SocketAddr,
    pub credential: ServiceCredential,
}

impl PrivateServiceConfig {
    pub fn new(bind: SocketAddr, credential: ServiceCredential, allow_container_bind: bool) -> Result<Self> {
        if !bind.ip().is_loopback() && !allow_container_bind {
            return Err(anyhow!("private service must bind to loopback"));
        }
        if credential.0.is_empty() {
            return Err(anyhow!("private service credential is required"));
        }
        Ok(Self { bind, credential })
    }
}

struct VaultState {
    user_id: Uuid,
    vault_id: Uuid,
    expiry: Instant,
    key: SecretKey,
    encrypted: Encrypted,
}

/// Internal KeePass state. Every operation removes state from the map while
/// decrypting, then replaces it with a newly encrypted value even when a
/// routine lookup fails. Authorization, expiry, and crypto failures instead
/// deliberately discard it.
pub struct PrivateVaultService {
    config: Config,
    active: Mutex<HashMap<String, VaultState>>,
}

impl PrivateVaultService {
    pub fn new(config: Config) -> Self {
        Self { config, active: Mutex::new(HashMap::new()) }
    }

    pub async fn unlock(&self, request: UnlockRequest) -> Result<UnlockResponse> {
        let UnlockRequest { user_id, vault_id, mut database_b64, password, mut keyfile_b64 } = request;
        let mut password = password.map(Zeroizing::new);
        let decoded_database = general_purpose::STANDARD.decode(&database_b64);
        database_b64.zeroize();
        let decoded_keyfile = match keyfile_b64.take() {
            Some(mut encoded) => {
                let decoded = general_purpose::STANDARD.decode(&encoded);
                encoded.zeroize();
                Some(decoded.map(|value| Zeroizing::new(value.into_boxed_slice())))
            }
            None => None,
        };
        let mut keyfile = decoded_keyfile;
        keyfile_b64.zeroize();
        let database = Zeroizing::new(decoded_database?);
        if !vault_size_allowed(database.len()) {
            return Err(anyhow!("vault object exceeds private service limit"));
        }
        let keyfile = keyfile.take().transpose()?;
        let database_result = KeePass::from_bytes(
            &self.config,
            database,
            password.take(),
            keyfile,
        ).await;
        let database = database_result?;
        let (key, encrypted) = database.to_enc()?;
        let handle = Alphanumeric.sample_string(&mut thread_rng(), 48);
        let expiry = encrypted.expiry;
        self.active.lock().await.insert(handle.clone(), VaultState {
            user_id,
            vault_id,
            expiry,
            key,
            encrypted,
        });
        Ok(UnlockResponse { handle, expires_in_seconds: self.config.db_session_timeout.as_secs() })
    }

    async fn with_vault<T, F>(&self, request: &HandleRequest, operation: F) -> Result<T>
    where F: FnOnce(&KeePass) -> Result<T> {
        let mut active = self.active.lock().await;
        let state = active.remove(&request.handle).ok_or_else(|| anyhow!("active vault not found"))?;
        if Instant::now() >= state.expiry || state.user_id != request.user_id || state.vault_id != request.vault_id {
            return Err(anyhow!("active vault unavailable"));
        }
        let database = KeePass::from_enc(&self.config, state.key, state.encrypted)?;
        let result = operation(&database);
        let (key, encrypted) = database.to_enc()?;
        active.insert(request.handle.clone(), VaultState {
            user_id: request.user_id,
            vault_id: request.vault_id,
            expiry: encrypted.expiry,
            key,
            encrypted,
        });
        result
    }

    async fn with_vault_mut<T, F>(&self, request: &HandleRequest, operation: F) -> Result<T>
    where F: FnOnce(&mut KeePass) -> Result<T> {
        let mut active = self.active.lock().await;
        let state = active.remove(&request.handle).ok_or_else(|| anyhow!("active vault not found"))?;
        if Instant::now() >= state.expiry || state.user_id != request.user_id || state.vault_id != request.vault_id {
            return Err(anyhow!("active vault unavailable"));
        }
        let mut database = KeePass::from_enc(&self.config, state.key, state.encrypted)?;
        let result = operation(&mut database);
        let value = match result {
            Ok(value) => value,
            Err(error) => return Err(error),
        };
        let (key, encrypted) = database.to_enc()?;
        active.insert(request.handle.clone(), VaultState {
            user_id: request.user_id,
            vault_id: request.vault_id,
            expiry: encrypted.expiry,
            key,
            encrypted,
        });
        Ok(value)
    }

    pub async fn groups(&self, request: &HandleRequest) -> Result<GroupsResponse> {
        self.with_vault(request, |db| {
            // `#[post("/groups")]` creates an item named `groups`; use a
            // distinct binding so this remains unambiguous to the compiler.
            let (root_group, last_selected) = db.get_groups()?;
            Ok(GroupsResponse { groups: root_group, last_selected })
        }).await
    }

    pub async fn group_entries(&self, request: &HandleRequest, group_id: Uuid) -> Result<EntryGroup> {
        self.with_vault(request, |db| db.get_group_entries_by_id(group_id)).await
    }

    pub async fn entry(&self, request: &HandleRequest, entry_id: Uuid) -> Result<Entry> {
        self.with_vault(request, |db| db.get_entry_by_id(entry_id)).await
    }

    pub async fn search(&self, request: &HandleRequest, term: &str) -> Result<EntryGroup> {
        self.with_vault(request, |db| db.search_entries_by_term(term)).await
    }

    pub async fn reveal(&self, request: &HandleRequest, entry_id: Uuid, field_name: &str) -> Result<RevealResponse> {
        self.with_vault(request, |db| {
            let value = db.get_protected_by_id(entry_id, field_name)?;
            Ok(RevealResponse { value: value.expose_secret().to_owned() })
        }).await
    }

    pub async fn update(&self, request: UpdateRequest) -> Result<UpdateResponse> {
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
            database.update_entry(request.entry_id, EntryUpdate {
                title: request.title,
                username: request.username,
                password: request.password,
                url: request.url,
                notes: request.notes,
            })?;
            let bytes = database.to_kdbx_bytes(password.take(), keyfile.take())?;
            Ok(UpdateResponse { database_b64: general_purpose::STANDARD.encode(bytes.as_slice()) })
        }).await
    }

    pub async fn close(&self, request: &HandleRequest) -> Result<()> {
        let mut active = self.active.lock().await;
        let state = active.remove(&request.handle).ok_or_else(|| anyhow!("active vault not found"))?;
        if Instant::now() >= state.expiry || state.user_id != request.user_id || state.vault_id != request.vault_id {
            return Err(anyhow!("active vault unavailable"));
        }
        Ok(())
    }

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

    pub async fn delete_entry(&self, request: DeleteEntryRequest) -> Result<DeleteEntryResponse> {
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
            let group_id = database.delete_entry(request.entry_id)?;
            let bytes = database.to_kdbx_bytes(password.take(), keyfile.take())?;
            Ok(DeleteEntryResponse {
                entry_id: request.entry_id,
                group_id,
                database_b64: general_purpose::STANDARD.encode(bytes.as_slice()),
            })
        }).await
    }

    pub async fn restore_entry(&self, request: RestoreEntryRequest) -> Result<RestoreEntryResponse> {
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
            database.restore_entry(request.entry_id, request.preferred_group_id)?;
            let bytes = database.to_kdbx_bytes(password.take(), keyfile.take())?;
            Ok(RestoreEntryResponse {
                entry_id: request.entry_id,
                database_b64: general_purpose::STANDARD.encode(bytes.as_slice()),
            })
        }).await
    }
}

#[derive(Deserialize)]
pub struct UnlockRequest {
    pub user_id: Uuid,
    pub vault_id: Uuid,
    pub database_b64: String,
    pub password: Option<String>,
    pub keyfile_b64: Option<String>,
}

#[derive(Deserialize)]
pub struct HandleRequest { pub handle: String, pub user_id: Uuid, pub vault_id: Uuid }
#[derive(Deserialize)]
struct GroupEntriesRequest { #[serde(flatten)] handle: HandleRequest, group_id: Uuid }
#[derive(Deserialize)]
struct EntryRequest { #[serde(flatten)] handle: HandleRequest, entry_id: Uuid }
#[derive(Deserialize)]
struct SearchRequest { #[serde(flatten)] handle: HandleRequest, term: String }
#[derive(Deserialize)]
struct RevealRequest { #[serde(flatten)] handle: HandleRequest, entry_id: Uuid, field_name: String }
#[derive(Deserialize)]
struct UpdateRequest {
    #[serde(flatten)] handle: HandleRequest,
    entry_id: Uuid,
    title: String,
    username: String,
    password: String,
    url: String,
    notes: String,
    master_password: Option<String>,
    keyfile_b64: Option<String>,
}
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
#[derive(Deserialize)]
pub struct DeleteEntryRequest {
    #[serde(flatten)] pub handle: HandleRequest,
    pub entry_id: Uuid,
    pub master_password: Option<String>,
    pub keyfile_b64: Option<String>,
}
#[derive(Deserialize)]
pub struct RestoreEntryRequest {
    #[serde(flatten)] pub handle: HandleRequest,
    pub entry_id: Uuid,
    #[serde(alias = "group_id")]
    pub preferred_group_id: Option<Uuid>,
    pub master_password: Option<String>,
    pub keyfile_b64: Option<String>,
}

#[derive(Serialize)]
pub struct UnlockResponse { pub handle: String, pub expires_in_seconds: u64 }
#[derive(Serialize)]
pub struct GroupsResponse { pub groups: Group, pub last_selected: Option<Uuid> }
#[derive(Serialize)]
pub struct RevealResponse { pub value: String }
#[derive(Serialize)]
pub struct UpdateResponse { pub database_b64: String }
#[derive(Serialize)]
pub struct CreateVaultResponse { pub database_b64: String }
#[derive(Serialize)]
pub struct CreateEntryResponse { pub entry_id: Uuid, pub database_b64: String }
#[derive(Serialize)]
pub struct DeleteEntryResponse { pub entry_id: Uuid, pub group_id: Uuid, pub database_b64: String }
#[derive(Serialize)]
pub struct RestoreEntryResponse { pub entry_id: Uuid, pub database_b64: String }

fn authenticated(request: &HttpRequest, credential: &ServiceCredential) -> bool {
    credential.verify_bearer(request.headers().get("Authorization").and_then(|value| value.to_str().ok())).is_ok()
}

fn private_error(request: &HttpRequest, status: actix_web::http::StatusCode) -> HttpResponse {
    let correlation_id = observability::correlation_id(request);
    HttpResponse::build(status)
        .insert_header(("X-Correlation-ID", correlation_id.clone()))
        .json(serde_json::json!({
            "success": false,
            "message": "private request failed",
            "correlation_id": correlation_id,
        }))
}

#[post("/unlock")]
async fn unlock(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<UnlockRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.unlock(body.into_inner()).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::BAD_REQUEST) }
}

#[post("/groups")]
async fn groups(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<HandleRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.groups(&body).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED) }
}

#[post("/entries")]
async fn entries(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<GroupEntriesRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.group_entries(&body.handle, body.group_id).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED) }
}

#[post("/entry")]
async fn entry(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<EntryRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.entry(&body.handle, body.entry_id).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED) }
}

#[post("/search")]
async fn search(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<SearchRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.search(&body.handle, &body.term).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED) }
}

#[post("/reveal")]
async fn reveal(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<RevealRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.reveal(&body.handle, body.entry_id, &body.field_name).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED) }
}

#[post("/update")]
async fn update(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<UpdateRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.update(body.into_inner()).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::BAD_REQUEST) }
}

#[post("/close")]
async fn close(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<HandleRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.close(&body).await { Ok(()) => HttpResponse::NoContent().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).finish(), Err(_) => private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED) }
}

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

#[post("/entries/delete")]
async fn delete_entry_route(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<DeleteEntryRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.delete_entry(body.into_inner()).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::BAD_REQUEST) }
}

#[post("/entries/restore")]
async fn restore_entry_route(request: HttpRequest, credential: web::Data<ServiceCredential>, service: web::Data<PrivateVaultService>, body: web::Json<RestoreEntryRequest>) -> impl Responder {
    if !authenticated(&request, &credential) { return private_error(&request, actix_web::http::StatusCode::UNAUTHORIZED); }
    match service.restore_entry(body.into_inner()).await { Ok(value) => HttpResponse::Ok().insert_header(("X-Correlation-ID", observability::correlation_id(&request))).json(value), Err(_) => private_error(&request, actix_web::http::StatusCode::BAD_REQUEST) }
}

pub async fn run_private_service(config: PrivateServiceConfig, vault_config: Config) -> std::io::Result<()> {
    let credential = web::Data::new(config.credential);
    let service = web::Data::new(PrivateVaultService::new(vault_config));
    let json_config = web::JsonConfig::default()
        .limit(MAX_PRIVATE_BODY_BYTES)
        .error_handler(|error, request| {
            InternalError::from_response(error, private_error(request, actix_web::http::StatusCode::BAD_REQUEST)).into()
        });
    HttpServer::new(move || actix_web::App::new()
        .app_data(credential.clone())
        .app_data(service.clone())
        .app_data(json_config.clone())
        .wrap_fn(|request, service| {
            let correlation_id = observability::correlation_id(request.request());
            let started = Instant::now();
            let response = service.call(request);
            async move {
                let response = response.await?;
                observability::emit_request(response.request(), &correlation_id, response.status().as_u16(), started.elapsed());
                Ok(response)
            }
        })
        .service(web::scope("/internal/v1")
            .service(unlock).service(groups).service(entries).service(entry).service(update)
            .service(search).service(reveal).service(close)
            .service(create_vault_route).service(create_entry_route)
            .service(delete_entry_route).service(restore_entry_route)))
        .bind(config.bind)?
        .run().await
}

#[cfg(test)]
mod tests {
    use std::time::Duration;

    use super::ActiveVaults;
    use base64::{Engine, engine::general_purpose};
    use uuid::Uuid;

    // A regression where lookup used only the opaque handle would permit a
    // cross-account replay; changing the owner or vault check must fail this test.
    #[tokio::test]
    async fn active_handle_is_bound_to_its_user_and_vault() {
        let active = ActiveVaults::new();
        let owner = Uuid::new_v4();
        let vault = Uuid::new_v4();
        let handle = active.open(owner, vault, Duration::from_secs(60)).await;

        assert!(active.authorize(&handle, owner, vault).await.is_ok());
        assert!(active.authorize(&handle, Uuid::new_v4(), vault).await.is_err());
        assert!(active.authorize(&handle, owner, Uuid::new_v4()).await.is_err());
    }

    #[tokio::test]
    async fn close_removes_only_the_matching_active_vault() {
        let active = ActiveVaults::new();
        let owner = Uuid::new_v4();
        let vault = Uuid::new_v4();
        let handle = active.open(owner, vault, Duration::from_secs(60)).await;

        assert!(active.close(&handle, owner, vault).await.is_ok());
        assert!(active.authorize(&handle, owner, vault).await.is_err());
    }

    #[tokio::test]
    async fn expired_active_handles_are_rejected() {
        let active = ActiveVaults::new();
        let owner = Uuid::new_v4();
        let vault = Uuid::new_v4();
        let handle = active.open(owner, vault, Duration::ZERO).await;

        assert!(active.authorize(&handle, owner, vault).await.is_err());
    }

    #[tokio::test]
    async fn rejected_cross_user_request_invalidates_the_handle() {
        let active = ActiveVaults::new();
        let owner = Uuid::new_v4();
        let vault = Uuid::new_v4();
        let handle = active.open(owner, vault, Duration::from_secs(60)).await;

        assert!(active.authorize(&handle, Uuid::new_v4(), vault).await.is_err());
        assert!(active.authorize(&handle, owner, vault).await.is_err());
    }

    #[test]
    fn private_service_credential_rejects_missing_and_wrong_bearer_tokens() {
        let credential = super::ServiceCredential::new("private-service-token");

        assert!(credential.verify_bearer(Some("Bearer private-service-token")).is_ok());
        assert!(credential.verify_bearer(None).is_err());
        assert!(credential.verify_bearer(Some("Bearer browser-token")).is_err());
        assert!(credential.verify_bearer(Some("Basic private-service-token")).is_err());
    }

    #[test]
    fn private_listener_refuses_a_public_bind_address() {
        let credential = super::ServiceCredential::new("private-service-token");
        let public = "0.0.0.0:8081".parse().unwrap();

        assert!(super::PrivateServiceConfig::new(public, credential, false).is_err());
    }

    #[test]
    fn private_listener_allows_container_bind_only_when_explicitly_enabled() {
        let bind = "0.0.0.0:8080".parse().unwrap();

        assert!(super::PrivateServiceConfig::new(
            bind,
            super::ServiceCredential::new("private-service-token"),
            true,
        ).is_ok());
    }

    #[test]
    fn decoded_vault_limit_matches_the_go_object_limit() {
        assert!(super::vault_size_allowed(50 * 1024 * 1024));
        assert!(!super::vault_size_allowed(50 * 1024 * 1024 + 1));
    }

    // Removing the user/vault comparison in `with_vault` would permit a
    // cross-account replay of a real, successfully decrypted vault handle.
    #[tokio::test]
    async fn encrypted_vault_handle_cannot_be_replayed_by_another_user() {
        let bytes = tokio::fs::read("tests/test.kdbx").await.unwrap();
        let service = super::PrivateVaultService::new(crate::config::config::Config::default());
        let owner = Uuid::new_v4();
        let vault = Uuid::new_v4();
        let unlocked = service.unlock(super::UnlockRequest {
            user_id: owner,
            vault_id: vault,
            database_b64: general_purpose::STANDARD.encode(bytes),
            password: Some("test".to_string()),
            keyfile_b64: None,
        }).await.unwrap();
        let owner_request = super::HandleRequest { handle: unlocked.handle.clone(), user_id: owner, vault_id: vault };
        let replay = super::HandleRequest { handle: unlocked.handle, user_id: Uuid::new_v4(), vault_id: vault };

        assert!(service.groups(&owner_request).await.is_ok());
        assert!(service.groups(&replay).await.is_err());
        assert!(service.groups(&owner_request).await.is_err());
    }

    // A missing entry is a normal caller error, not a reason to discard an
    // otherwise valid active vault.
    #[tokio::test]
    async fn failed_read_keeps_the_encrypted_active_vault_available() {
        let bytes = tokio::fs::read("tests/test.kdbx").await.unwrap();
        let service = super::PrivateVaultService::new(crate::config::config::Config::default());
        let owner = Uuid::new_v4();
        let vault = Uuid::new_v4();
        let unlocked = service.unlock(super::UnlockRequest {
            user_id: owner,
            vault_id: vault,
            database_b64: general_purpose::STANDARD.encode(bytes),
            password: Some("test".to_string()),
            keyfile_b64: None,
        }).await.unwrap();
        let request = super::HandleRequest { handle: unlocked.handle, user_id: owner, vault_id: vault };

        assert!(service.entry(&request, Uuid::new_v4()).await.is_err());
        assert!(service.groups(&request).await.is_ok());
    }

    #[tokio::test]
    async fn create_vault_then_unlock_round_trips() {
        let service = super::PrivateVaultService::new(crate::config::config::Config::default());
        let created = service.create_vault(super::CreateVaultRequest {
            password: Some("test".to_owned()),
            keyfile_b64: None,
        }).await.unwrap();

        let user_id = Uuid::new_v4();
        let vault_id = Uuid::new_v4();
        let unlocked = service.unlock(super::UnlockRequest {
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
        let service = super::PrivateVaultService::new(crate::config::config::Config::default());
        let created = service.create_vault(super::CreateVaultRequest { password: Some("test".to_owned()), keyfile_b64: None }).await.unwrap();
        let user_id = Uuid::new_v4();
        let vault_id = Uuid::new_v4();
        let unlocked = service.unlock(super::UnlockRequest {
            user_id, vault_id, database_b64: created.database_b64, password: Some("test".to_owned()), keyfile_b64: None,
        }).await.unwrap();

        let result = service.create_entry(super::CreateEntryRequest {
            handle: super::HandleRequest { handle: unlocked.handle.clone(), user_id, vault_id },
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

    // Deletion is a mutation, so it must enforce the same handle binding as
    // every other vault operation before touching either KDBX or credentials.
    #[tokio::test]
    async fn delete_entry_rejects_a_handle_owned_by_another_user() {
        let service = super::PrivateVaultService::new(crate::config::config::Config::default());
        let created = service.create_vault(super::CreateVaultRequest { password: Some("test".to_owned()), keyfile_b64: None }).await.unwrap();
        let owner = Uuid::new_v4();
        let vault_id = Uuid::new_v4();
        let unlocked = service.unlock(super::UnlockRequest {
            user_id: owner, vault_id, database_b64: created.database_b64, password: Some("test".to_owned()), keyfile_b64: None,
        }).await.unwrap();

        let result = service.delete_entry(super::DeleteEntryRequest {
            handle: super::HandleRequest { handle: unlocked.handle, user_id: Uuid::new_v4(), vault_id },
            entry_id: Uuid::new_v4(),
            master_password: Some("test".to_owned()),
            keyfile_b64: None,
        }).await;

        assert!(result.is_err());
    }

    #[tokio::test]
    async fn failed_mutation_discards_the_possibly_changed_handle() {
        let service = super::PrivateVaultService::new(crate::config::config::Config::default());
        let created = service.create_vault(super::CreateVaultRequest { password: Some("test".to_owned()), keyfile_b64: None }).await.unwrap();
        let user_id = Uuid::new_v4();
        let vault_id = Uuid::new_v4();
        let unlocked = service.unlock(super::UnlockRequest {
            user_id, vault_id, database_b64: created.database_b64, password: Some("test".to_owned()), keyfile_b64: None,
        }).await.unwrap();
        let handle = super::HandleRequest { handle: unlocked.handle, user_id, vault_id };

        let result = service.delete_entry(super::DeleteEntryRequest {
            handle: super::HandleRequest { handle: handle.handle.clone(), user_id, vault_id },
            entry_id: Uuid::new_v4(), master_password: Some("test".to_owned()), keyfile_b64: None,
        }).await;

        assert!(result.is_err());
        assert!(service.groups(&handle).await.is_err());
    }
}
