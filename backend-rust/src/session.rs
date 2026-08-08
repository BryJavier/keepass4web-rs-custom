use actix_session::Session;
use serde::de::DeserializeOwned;

use crate::auth::{SESSION_KEY_USER, SESSION_USER_UNKNOWN};
use crate::auth_backend::UserInfo;
use crate::observability::{self, SafeEvent};

pub trait AuthSession {
    fn destroy(&self);
    fn get_key<T: DeserializeOwned>(&self, key: &str) -> Option<T>;
    fn get_user_id(&self) -> String;
    fn is_authorized(&self) -> bool;
}

impl AuthSession for Session {
    fn destroy(&self) {
        self.purge();
    }

    fn get_key<T: DeserializeOwned>(&self, key: &str) -> Option<T> {
        match self.get::<T>(key) {
            Ok(s) => s,
            Err(_) => {
                observability::emit(log::Level::Error, SafeEvent::SessionFailure, "session", None);
                None
            }
        }
    }

    fn get_user_id(&self) -> String {
        match self.get_key::<UserInfo>(SESSION_KEY_USER) {
            Some(u) => u.id,
            None => SESSION_USER_UNKNOWN.to_string(),
        }
    }

    fn is_authorized(&self) -> bool {
        self.get_key::<UserInfo>(SESSION_KEY_USER).is_some()
    }
}
