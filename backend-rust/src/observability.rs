use std::time::Duration;

use actix_web::{HttpMessage, HttpRequest};
use log::Level;
use rand::distributions::{Alphanumeric, DistString};
use rand::thread_rng;
use serde_json::{json, Value};

#[derive(Clone, Copy)]
pub enum SafeEvent {
    StartupFailure,
    RequestCompleted,
    RequestRejected,
    AuthenticationFailure,
    SessionFailure,
    VaultOperationFailure,
    CacheLifecycle,
}

impl SafeEvent {
    fn name(self) -> &'static str {
        match self {
            Self::StartupFailure => "startup_failure",
            Self::RequestCompleted => "request_completed",
            Self::RequestRejected => "request_rejected",
            Self::AuthenticationFailure => "authentication_failure",
            Self::SessionFailure => "session_failure",
            Self::VaultOperationFailure => "vault_operation_failure",
            Self::CacheLifecycle => "cache_lifecycle",
        }
    }
}

pub fn initialize() {}

pub fn emit(level: Level, event: SafeEvent, correlation_id: &str, status: Option<u16>) {
    let status = status.map(|value| value.to_string()).unwrap_or_else(|| "none".to_string());
    let event = format!(
        "level={} event={} correlation_id={} status={}",
        level,
        event.name(),
        correlation_id,
        status,
    );

    println!("{event}");
    eprintln!("{event}");
}

pub fn emit_request(
    request: &HttpRequest,
    correlation_id: &str,
    status: u16,
    duration: Duration,
) {
    let route = request
        .match_pattern()
        .unwrap_or_else(|| "unmatched".to_string());
    let event = format!(
        "level={} event={} correlation_id={} status={} method={} path={} duration_ms={}",
        Level::Info,
        SafeEvent::RequestCompleted.name(),
        correlation_id,
        status,
        request.method(),
        route,
        duration.as_millis(),
    );

    println!("{event}");
    eprintln!("{event}");
}

pub fn correlation_id(request: &actix_web::HttpRequest) -> String {
    if let Some(correlation_id) = request.extensions().get::<CorrelationId>() {
        return correlation_id.0.clone();
    }

    // Go supplies this on internal calls. Only accept a constrained value so a
    // caller cannot inject log fields through a correlation header.
    let correlation_id = request.headers().get("X-Correlation-ID")
        .and_then(|value| value.to_str().ok())
        .filter(|value| valid_correlation_id(value))
        .map(str::to_owned)
        .unwrap_or_else(new_correlation_id);
    request.extensions_mut().insert(CorrelationId(correlation_id.clone()));
    correlation_id
}

fn valid_correlation_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value.bytes().all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
}

pub fn public_error(correlation_id: &str) -> Value {
    json!({
        "success": false,
        "message": "request failed",
        "correlation_id": correlation_id,
    })
}

#[derive(Clone)]
struct CorrelationId(String);

fn new_correlation_id() -> String {
    Alphanumeric.sample_string(&mut thread_rng(), 32)
}
