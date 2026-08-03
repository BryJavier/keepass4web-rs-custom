use clap::Parser;

use crate::config::config::Config;
use crate::server::server::Server;

mod auth_backend;
mod db_backend;
mod config;
mod server;
mod auth;
mod keepass;
mod observability;
mod session;

const CONFIG_FILE: &str = "config.yml";


#[derive(Parser)]
#[command(author, version, about)]
struct Args {
    #[arg(short, long, default_value = CONFIG_FILE)]
    config: std::path::PathBuf,
}

#[actix_web::main]
async fn main() {
    let args = Args::parse();
    observability::initialize();
    let config = match Config::from_file(args.config) {
        Ok(config) => config,
        Err(_) => {
            observability::emit(log::Level::Error, observability::SafeEvent::StartupFailure, "startup", None);
            return;
        }
    };

    if Server::new(config).await.is_err() {
        observability::emit(log::Level::Error, observability::SafeEvent::StartupFailure, "startup", None);
    }
}
