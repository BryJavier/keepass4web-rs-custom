use std::fs::File;
use std::path::PathBuf;
use std::net::SocketAddr;
use std::time::Duration;

use actix_web::cookie;
use anyhow::Result;
use serde::Deserialize;
use serde_yaml::from_reader;

use crate::{auth_backend, db_backend};
use crate::config::backend::{AuthBackend, DbBackend};
use crate::config::cookie::SameSiteDef;
use crate::config::filesystem::Filesystem;
use crate::config::htpasswd::Htpasswd;
use crate::config::http::Http;
use crate::config::key::Key;
use crate::config::ldap::Ldap;
use crate::config::oidc::Oidc;
use crate::config::private_service::PrivateService;
use crate::config::search::Search;

#[derive(Clone, Deserialize)]
#[serde(default)]
pub struct Config {
    #[serde(alias = "server")]
    pub listen: String,
    pub port: u16,
    #[serde(with = "humantime_serde")]
    pub db_session_timeout: Duration,
    #[serde(with = "humantime_serde")]
    pub auth_check_interval: Duration,
    pub auth_backend: AuthBackend,
    pub db_backend: DbBackend,
    pub session_secret_key: Key,
    #[serde(with = "humantime_serde")]
    pub session_lifetime: Duration,
    #[serde(with = "SameSiteDef")]
    pub cookie_samesite: cookie::SameSite,
    pub search: Search,
    #[serde(alias = "LDAP", alias = "Ldap")]
    pub ldap: Ldap,
    #[serde(alias = "OIDC", alias = "Oidc")]
    pub oidc: Oidc,
    #[serde(alias = "Htpasswd")]
    pub htpasswd: Htpasswd,
    #[serde(alias = "Filesystem")]
    pub filesystem: Filesystem,
    #[serde(alias = "HTTP", alias = "Http")]
    pub http: Http,
    pub private_service: PrivateService,
}

impl Default for Config {
    fn default() -> Self {
        Config {
            listen: "127.0.0.1".to_string(),
            port: 8080,
            // 10 minutes
            db_session_timeout: Duration::from_secs(10 * 60),
            // 1 hour, 5 minutes
            auth_check_interval: Duration::from_secs(60 * 60 + 5 * 60),
            auth_backend: Default::default(),
            db_backend: Default::default(),
            session_secret_key: Key(cookie::Key::generate()),
            // 1 hour
            session_lifetime: Duration::from_secs(60 * 60),
            cookie_samesite: cookie::SameSite::Strict,
            search: Default::default(),
            ldap: Default::default(),
            oidc: Default::default(),
            htpasswd: Default::default(),
            filesystem: Default::default(),
            http: Default::default(),
            private_service: Default::default(),
        }
    }
}

impl Config {
    pub fn from_file(filename: PathBuf) -> Result<Self> {
        let file = File::open(filename)?;
        let mut conf: Config = from_reader(file)?;
        conf.apply_private_service_environment()?;

        auth_backend::new(&conf).validate_config()?;
        db_backend::new(&conf).validate_config()?;

        Ok(conf)
    }

    fn apply_private_service_environment(&mut self) -> Result<()> {
        if let Ok(listen) = std::env::var("RUST_PRIVATE_LISTEN") {
            let bind: SocketAddr = listen.parse()
                .map_err(|_| anyhow::anyhow!("RUST_PRIVATE_LISTEN must be a socket address"))?;
            self.private_service.listen = bind.ip().to_string();
            self.private_service.port = bind.port();
        }
        let service_token_from_env = if let Ok(token) = std::env::var("RUST_SERVICE_TOKEN") {
            self.private_service.credential = token;
            true
        } else { false };
        if let Ok(value) = std::env::var("PRIVATE_SERVICE_ALLOW_CONTAINER_BIND") {
            self.private_service.allow_container_bind = match value.as_str() {
                "true" => true,
                "false" | "" => false,
                _ => anyhow::bail!("PRIVATE_SERVICE_ALLOW_CONTAINER_BIND must be true or false"),
            };
        }
        if self.private_service.enabled && !service_token_from_env {
            anyhow::bail!("RUST_SERVICE_TOKEN is required when the private service is enabled");
        }
        Ok(())
    }
}
