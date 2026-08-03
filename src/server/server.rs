use actix_session::{config::PersistentSession, SessionMiddleware, storage::CookieSessionStore};
use std::time::Instant;

use actix_web::{App, HttpRequest, HttpResponse, HttpServer, web};
use actix_web::cookie::time::Duration;
use actix_web::dev::Service;
use anyhow::Result;

use crate::{auth, auth_backend};
use crate::config::config::Config;
use crate::keepass::db_cache::DbCache;
use crate::observability;
use crate::server::route::setup_routes;

pub struct Server;

impl Server {
    pub async fn new(config: Config) -> Result<()> {
        let server = config.listen.clone();
        let port = config.port;
        let secret_key = config.session_secret_key.0.clone();
        let config_data = web::Data::new(config);
        let auth_cache = web::Data::new(auth_backend::new(&config_data).init().await?);
        let db_cache = web::Data::new(DbCache::default());

        HttpServer::new(move || {
            App::new()
                .app_data(db_cache.clone())
                .app_data(auth_cache.clone())
                .app_data(config_data.clone())
                .wrap(auth::CheckAuth)
                .wrap_fn(|request, service| {
                    let method = request.method().as_str().to_string();
                    let path = request.path().to_string();
                    let correlation_id = observability::correlation_id(request.request());
                    let started = Instant::now();
                    let response = service.call(request);

                    async move {
                        let response = response.await?;
                        observability::emit_request(
                            &method,
                            &path,
                            &correlation_id,
                            response.status().as_u16(),
                            started.elapsed(),
                        );
                        Ok(response)
                    }
                })
                .wrap(
                    SessionMiddleware::builder(
                        CookieSessionStore::default(),
                        secret_key.clone(),
                    )
                        .session_lifecycle(
                            PersistentSession::default()
                                .session_ttl(Duration::new(
                                    config_data.session_lifetime.as_secs() as i64,
                                    0,
                                ))
                        )
                        .cookie_same_site(config_data.cookie_samesite)
                        .build(),
                )
                .default_service(web::route().to(|request: HttpRequest| async move {
                    let correlation_id = observability::correlation_id(&request);
                    HttpResponse::NotFound().json(observability::public_error(&correlation_id))
                }))
                .configure(setup_routes)
        }).bind((server, port))?
            .run()
            .await
            .map_err(anyhow::Error::new)
    }
}
