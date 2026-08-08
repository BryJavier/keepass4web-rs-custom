use serde::Deserialize;

/// Opt-in configuration for the Go-to-Rust listener. The listener performs a
/// second loopback check at startup; this configuration cannot expose it.
#[derive(Clone, Deserialize)]
#[serde(default)]
pub struct PrivateService {
    pub enabled: bool,
    /// Only enable when the deployment isolates this listener on a private
    /// container network with no host-published port.
    pub allow_container_bind: bool,
    pub listen: String,
    pub port: u16,
    pub credential: String,
}

impl Default for PrivateService {
    fn default() -> Self {
        Self {
            enabled: false,
            allow_container_bind: false,
            listen: "127.0.0.1".to_string(),
            port: 8081,
            credential: String::new(),
        }
    }
}
