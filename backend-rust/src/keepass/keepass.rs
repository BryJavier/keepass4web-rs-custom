use actix_web::web::{Path, Query};
use anyhow::{anyhow, bail};
use anyhow::Result;
use base64;
use base64::Engine;
use base64::engine::general_purpose;
use keepass::{Database, DatabaseKey};
use keepass::config::DatabaseConfig;
use keepass::db::{Group as DbGroup, Icon, Node, Value};
use regex::Regex;
use secrecy::{ExposeSecret, SecretString};
use secstr::SecStr;
use serde::Deserialize;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use uuid::Uuid;
use zeroize::{Zeroize, Zeroizing};

use crate::auth::DbLogin;
use crate::auth_backend::UserInfo;
use crate::config::config::Config;
use crate::config::search::Search;
use crate::db_backend::DbBackend;
use crate::keepass::encrypted::Encrypted;
use crate::keepass::entry::{
    Entry,
    EntryGroup,
    Group,
};
use crate::keepass::key::SecretKey;

#[derive(Deserialize)]
pub struct Id {
    pub id: Uuid,
}

#[derive(Deserialize)]
pub struct Protected {
    pub entry_id: Uuid,
    pub name: String,
}

#[derive(Deserialize)]
pub struct File {
    pub entry_id: Uuid,
    pub filename: String,
}

#[derive(Deserialize)]
pub struct SearchTerm {
    pub term: String,
}

pub struct KeePass {
    config: Config,
    db: Database,
}

pub struct EntryUpdate {
    pub title: String,
    pub username: String,
    pub password: String,
    pub url: String,
    pub notes: String,
}

// Deleted entries remain as native KDBX entries, so all protected and custom
// fields survive recovery. This group is intentionally omitted from normal
// browsing and search results.
const TOMBSTONE_GROUP_NAME: &str = ".keepass4web-tombstones";

impl KeePass {
    /// Opens a KDBX payload supplied by the trusted private service caller.
    /// The caller owns authorization; this function deliberately performs no
    /// storage or browser-session work.
    pub async fn from_bytes(
        config: &Config,
        mut bytes: Zeroizing<Vec<u8>>,
        password: Option<Zeroizing<String>>,
        keyfile: Option<Zeroizing<Box<[u8]>>>,
    ) -> Result<Self> {
        // `Zeroizing` covers every early-return path, including key parsing
        // failures and a decoder error in the blocking task.
        let mut db_key = DatabaseKey::new();
        if let Some(keyfile) = keyfile {
            let mut keyfile_slice = keyfile.as_ref();
            db_key = db_key.with_keyfile(&mut keyfile_slice)?;
        }
        if let Some(password) = password {
            db_key = db_key.with_password(&password);
        }
        let db = tokio::task::spawn_blocking(move || {
            let database = Database::open(&mut bytes.as_slice(), db_key);
            database
        }).await??;
        Ok(Self { config: config.clone(), db })
    }
    pub fn from_enc(config: &Config, key: SecretKey, enc: Encrypted) -> Result<Self> {
        // TODO: add some aad from the keepass db
        let ser_db = enc.decrypt(key, &[])?;

        let db: Database = postcard::from_bytes(ser_db.expose_secret())?;
        Ok(
            Self {
                config: config.clone(),
                db,
            }
        )
    }

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

    /// Serializes this decoded database back into a KDBX payload. Credential
    /// material is caller supplied for this one operation and is never kept in
    /// the encrypted in-memory vault state.
    pub fn to_kdbx_bytes(
        &self,
        password: Option<Zeroizing<String>>,
        keyfile: Option<Zeroizing<Box<[u8]>>>,
    ) -> Result<Zeroizing<Vec<u8>>> {
        let db_key = Self::key_from_material(password, keyfile)?;
        let mut bytes = Vec::new();
        let result = self.db.save(&mut bytes, db_key);
        result?;
        Ok(Zeroizing::new(bytes))
    }

    pub fn to_enc(self) -> Result<(SecretKey, Encrypted)> {
        // TODO: avoid vector realloc to make zeroize effective
        let ser_db = postcard::to_stdvec(&self.db)?;
        drop(self.db);

        // TODO: add some aad from the keepass db
        Encrypted::encrypt(ser_db, &[], self.config.db_session_timeout)
    }

    pub async fn from_backend(config: &Config, db_backend: &dyn DbBackend, params: &DbLogin, user_info: &UserInfo) -> Result<Self> {
        let db_key = Self::db_key_from_params(db_backend, params, user_info).await?;

        let mut reader = db_backend.get_db_read(user_info).await?;

        // bridge sync and async by caching the whole file in memory for now
        let mut buf = vec![];
        reader.read_to_end(&mut buf).await?;

        let db = tokio::task::spawn_blocking(move || {
            let db = Database::open(&mut buf.as_slice(), db_key);
            buf.zeroize();
            db
        }).await??;

        Ok(
            KeePass {
                config: config.clone(),
                db,
            }
        )
    }

    #[allow(dead_code)]
    pub async fn to_backend(self, db_backend: &mut dyn DbBackend, params: &DbLogin, user_info: &UserInfo) -> Result<()> {
        let key = Self::db_key_from_params(db_backend, params, user_info).await?;

        let mut buf: Vec<u8> = vec![];
        let (result, mut buf) = tokio::task::spawn_blocking(move || {
            (self.db.save(&mut buf, key), buf)
        }).await?;
        result?;

        let (mut writer, rx) = db_backend.get_db_write(user_info).await?;
        writer.write_all(&buf).await?;
        buf.zeroize();

        // close our side to signal end of data
        // otherwise we could get a deadlock awaiting the channel
        writer.shutdown().await?;
        if let Some(rx) = rx {
            rx.await??;
        }

        Ok(())
    }

    async fn db_key_from_params(db_backend: &dyn DbBackend, params: &DbLogin, user_info: &UserInfo) -> Result<DatabaseKey> {
        let mut db_key = DatabaseKey::new();
        let mut temp1;
        let mut temp2;
        let keyfile;
        if let Some(keyfile_b64) = &params.key {
            // TODO: use constant time decode against timing attacks
            keyfile = general_purpose::STANDARD.decode(keyfile_b64)?;

            temp1 = keyfile.as_slice();
            db_key = db_key.with_keyfile(&mut temp1)?;
        } else if let Some(keyfile) = db_backend.get_key_read(user_info).await {
            temp2 = keyfile?;
            // TODO: fix this
            let mut buf = vec![];
            temp2.read_to_end(&mut buf).await?;
            db_key = db_key.with_keyfile(&mut buf.as_slice())?;
            buf.zeroize();
        }

        if let Some(pw) = &params.password {
            db_key = db_key.with_password(pw);
        }
        Ok(db_key)
    }

    fn key_from_material(
        password: Option<Zeroizing<String>>,
        keyfile: Option<Zeroizing<Box<[u8]>>>,
    ) -> Result<DatabaseKey> {
        let mut db_key = DatabaseKey::new();
        if let Some(keyfile) = keyfile {
            let mut keyfile_slice = keyfile.as_ref();
            db_key = db_key.with_keyfile(&mut keyfile_slice)?;
        }
        if let Some(password) = password {
            db_key = db_key.with_password(&password);
        }
        Ok(db_key)
    }


    pub fn get_groups(&self) -> Result<(Group, Option<Uuid>)> {
        let mut last_selected = self.db.meta.last_selected_group;

        if let Some(v) = last_selected {
            if Self::find_group_by_id(&self.db.root, &v).is_none() {
                last_selected = None;
            }
        }

        Ok(
            (
                Self::find_all_groups(&self.db.root),
                last_selected,
            )
        )
    }

    pub fn get_group_entries(&self, params: &Query<Id>) -> Result<EntryGroup> {
        self.get_group_entries_by_id(params.id)
    }

    pub fn get_group_entries_by_id(&self, group_id: Uuid) -> Result<EntryGroup> {
        let group = Self::find_group_by_id(&self.db.root, &group_id).ok_or(anyhow!("group not found"))?;
        if Self::is_tombstone_group(group) {
            bail!("group not found");
        }

        let mut entries = Vec::with_capacity(group.children.len());
        for node in &group.children {
            if let Node::Entry(entry) = node {
                entries.push(
                    // Populate (potentially) visible fields only
                    Entry {
                        id: entry.uuid,
                        title: entry.get_title().map(String::from),
                        username: entry.get_username().map(String::from),
                        notes: None,
                        strings: None,
                        binary: None,
                        protected: None,
                        tags: None,
                        icon: entry.icon_id,
                        custom_icon_uuid: entry.custom_icon_uuid,
                        url: entry.get_url().map(String::from),
                    }
                )
            }
        }

        Ok(EntryGroup {
            title: group.name.clone(),
            entries,
            icon: group.icon_id,
            custom_icon_uuid: group.custom_icon_uuid,
        })
    }

    pub fn get_entry(&self, params: &Query<Id>) -> Result<Entry> {
        self.get_entry_by_id(params.id)
    }

    pub fn get_entry_by_id(&self, entry_id: Uuid) -> Result<Entry> {
        let entry = Self::find_entry_by_id(&self.db.root, &entry_id).ok_or(anyhow!("entry not found"))?;

        Ok(entry.into())
    }

    pub fn update_entry(&mut self, entry_id: Uuid, update: EntryUpdate) -> Result<()> {
        let entry = Self::find_entry_by_id_mut(&mut self.db.root, &entry_id)
            .ok_or_else(|| anyhow!("entry not found"))?;
        entry.fields.insert("Title".to_owned(), Value::Unprotected(update.title));
        entry.fields.insert("UserName".to_owned(), Value::Unprotected(update.username));
        entry.fields.insert("URL".to_owned(), Value::Unprotected(update.url));
        entry.fields.insert("Notes".to_owned(), Value::Unprotected(update.notes));
        entry.fields.insert("Password".to_owned(), Value::Protected(SecStr::new(update.password.into_bytes())));
        Ok(())
    }

    /// Moves the existing KDBX entry into the tombstone group and returns its
    /// original group UUID. Moving rather than recreating preserves the UUID,
    /// fields, protected values, binaries, and other native metadata.
    pub fn delete_entry(&mut self, entry_id: Uuid) -> Result<Uuid> {
        let Some((entry, original_group_id)) = Self::take_active_entry(&mut self.db.root, &entry_id) else {
            if Self::tombstoned_entry_exists(&self.db.root, &entry_id) {
                return Ok(self.db.root.uuid);
            }
            return Err(anyhow!("entry not found"));
        };
        Self::tombstone_group_mut(&mut self.db.root).add_child(entry);
        Ok(original_group_id)
    }

    /// Restores a tombstoned KDBX entry. If its preferred group no longer
    /// exists, restore directly into the root group instead.
    pub fn restore_entry(&mut self, entry_id: Uuid, preferred_group_id: Option<Uuid>) -> Result<()> {
        let Some(entry) = Self::take_tombstoned_entry(&mut self.db.root, &entry_id) else {
            if Self::find_entry_by_id(&self.db.root, &entry_id).is_some() {
                return Ok(());
            }
            return Err(anyhow!("tombstoned entry not found"));
        };
        let mut entry = Some(entry);
        let restored_to_preferred_group = preferred_group_id.map(|group_id| {
            Self::add_entry_to_active_group(&mut self.db.root, &group_id, &mut entry)
        }).unwrap_or(false);
        if !restored_to_preferred_group {
            self.db.root.add_child(entry.expect("entry must not be consumed before restoration"));
        }
        Ok(())
    }

    pub fn get_protected(&self, params: &Query<Protected>) -> Result<SecretString> {
        self.get_protected_by_id(params.entry_id, &params.name)
    }

    pub fn get_protected_by_id(&self, entry_id: Uuid, name: &str) -> Result<SecretString> {
        let entry = Self::find_entry_by_id(&self.db.root, &entry_id).ok_or(anyhow!("entry not found"))?;

        let field = match name {
            "password" => entry.fields.get("Password").cloned(),
            k => entry.fields.get(k).cloned(),
        };

        let protected = match field {
            Some(v) => match v {
                Value::Protected(p) => p,
                _ => bail!("not a protected field"),
            },
            None => bail!("field not found"),
        };

        Ok(
            SecretString::new(
                String::from_utf8_lossy(protected.unsecure()).to_string()
            )
        )
    }

    pub fn get_file(&self, params: &Query<File>) -> Result<Vec<u8>> {
        let _entry = Self::find_entry_by_id(&self.db.root, &params.entry_id).ok_or(anyhow!("entry not found"))?;

        todo!()
    }

    pub fn search_entries(&self, params: &Query<SearchTerm>) -> Result<EntryGroup> {
        self.search_entries_by_term(&params.term)
    }

    pub fn search_entries_by_term(&self, search_term: &str) -> Result<EntryGroup> {
        let mut term = search_term.to_string();
        if !self.config.search.allow_regex {
            term = regex::escape(search_term);
        }
        let rgx = Regex::new(&format!("(?i){}", term))?;
        let entries = Self::find_entries_by_string(&self.db.root, &rgx, &self.config.search);

        Ok(EntryGroup {
            title: format!("Search results for '{}'", search_term),
            entries,
            // search icon
            icon: Some(40),
            custom_icon_uuid: None,
        })
    }

    pub fn get_icon(&self, params: &Path<Id>) -> Result<Icon> {
        // TODO: can we improve this?
        for icon in &self.db.meta.custom_icons.icons {
            if icon.uuid == params.id {
                return Ok(icon.clone());
            }
        }

        bail!("icon not found")
    }

    pub(crate) fn find_all_groups(group: &keepass::db::Group) -> Group {
        let mut children: Vec<Group> = Vec::with_capacity(group.children.len());
        for node in &group.children {
            if let Node::Group(group) = node {
                if !Self::is_tombstone_group(group) {
                    children.push(Self::find_all_groups(group));
                }
            }
        }
        Group {
            id: group.uuid,
            title: group.name.clone(),
            icon: group.icon_id,
            custom_icon_uuid: None,
            children,
            expanded: group.is_expanded,
        }
    }

    pub(crate) fn find_group_by_id<'a>(group: &'a keepass::db::Group, id: &Uuid) -> Option<&'a keepass::db::Group> {
        if &group.uuid == id {
            return Some(group);
        }
        for node in &group.children {
            if let Node::Group(group) = node {
                let found = Self::find_group_by_id(group, id);
                if found.is_some() {
                    return found;
                }
            }
        }

        None
    }

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

    pub(crate) fn find_entry_by_id<'a>(group: &'a keepass::db::Group, id: &Uuid) -> Option<&'a keepass::db::Entry> {
        for node in &group.children {
            match node {
                Node::Group(group) => {
                    if !Self::is_tombstone_group(group) {
                        let found = Self::find_entry_by_id(group, id);
                        if found.is_some() {
                            return found;
                        }
                    }
                }
                Node::Entry(entry) => {
                    if &entry.uuid == id {
                        return Some(entry);
                    }
                }
            }
        }

        None
    }

    fn find_entry_by_id_mut<'a>(group: &'a mut keepass::db::Group, id: &Uuid) -> Option<&'a mut keepass::db::Entry> {
        for node in &mut group.children {
            match node {
                Node::Group(group) if !Self::is_tombstone_group(group) => if let Some(entry) = Self::find_entry_by_id_mut(group, id) { return Some(entry) },
                Node::Entry(entry) => if &entry.uuid == id { return Some(entry) },
                Node::Group(_) => {},
            }
        }
        None
    }

    pub(crate) fn find_entries_by_string(group: &keepass::db::Group, term: &Regex, config: &Search) -> Vec<Entry> {
        let mut entries = vec![];

        for node in &group.children {
            match node {
                Node::Group(group) => {
                    if !Self::is_tombstone_group(group) {
                        entries.append(&mut Self::find_entries_by_string(group, term, config));
                    }
                }
                Node::Entry(entry) => {
                    let entry: Entry = entry.into();
                    if entry.matches_regex(term, config) {
                        entries.push(entry);
                    }
                }
            }
        }

        entries
    }

    fn is_tombstone_group(group: &keepass::db::Group) -> bool {
        group.name == TOMBSTONE_GROUP_NAME
    }

    fn tombstone_group_mut(root: &mut keepass::db::Group) -> &mut keepass::db::Group {
        let index = root.children.iter().position(|node| {
            matches!(node, Node::Group(group) if Self::is_tombstone_group(group))
        }).unwrap_or_else(|| {
            root.add_child(DbGroup::new(TOMBSTONE_GROUP_NAME));
            root.children.len() - 1
        });
        match root.children.get_mut(index) {
            Some(Node::Group(group)) => group,
            _ => unreachable!("tombstone group must be a group node"),
        }
    }

    fn take_active_entry(group: &mut keepass::db::Group, id: &Uuid) -> Option<(keepass::db::Entry, Uuid)> {
        if let Some(index) = group.children.iter().position(|node| {
            matches!(node, Node::Entry(entry) if &entry.uuid == id)
        }) {
            let Node::Entry(entry) = group.children.remove(index) else {
                unreachable!("entry position must refer to an entry node");
            };
            return Some((entry, group.uuid));
        }
        for node in &mut group.children {
            if let Node::Group(child) = node {
                if !Self::is_tombstone_group(child) {
                    if let Some(entry) = Self::take_active_entry(child, id) {
                        return Some(entry);
                    }
                }
            }
        }
        None
    }

    fn take_tombstoned_entry(root: &mut keepass::db::Group, id: &Uuid) -> Option<keepass::db::Entry> {
        let tombstone_group = root.children.iter_mut().find_map(|node| match node {
            Node::Group(group) if Self::is_tombstone_group(group) => Some(group),
            _ => None,
        })?;
        let index = tombstone_group.children.iter().position(|node| {
            matches!(node, Node::Entry(entry) if &entry.uuid == id)
        })?;
        let Node::Entry(entry) = tombstone_group.children.remove(index) else {
            unreachable!("tombstone entry position must refer to an entry node");
        };
        Some(entry)
    }

    fn tombstoned_entry_exists(root: &keepass::db::Group, id: &Uuid) -> bool {
        root.children.iter().any(|node| match node {
            Node::Group(group) if Self::is_tombstone_group(group) => group.children.iter().any(|child| {
                matches!(child, Node::Entry(entry) if &entry.uuid == id)
            }),
            _ => false,
        })
    }

    fn add_entry_to_active_group(group: &mut keepass::db::Group, id: &Uuid, entry: &mut Option<keepass::db::Entry>) -> bool {
        if &group.uuid == id && !Self::is_tombstone_group(group) {
            group.add_child(entry.take().expect("entry must be available for restoration"));
            return true;
        }
        for node in &mut group.children {
            if let Node::Group(child) = node {
                if !Self::is_tombstone_group(child) && Self::add_entry_to_active_group(child, id, entry) {
                    return true;
                }
            }
        }
        false
    }
}

#[cfg(test)]
mod tests {
    use tokio::fs;

    use crate::config::backend::DbBackend;
    use crate::db_backend;
    use crate::db_backend::test::Test;

    use super::*;

    #[tokio::test]
    async fn database_roundtrip() {
        let params = DbLogin {
            password: Some("test".to_string()),
            key: None,
        };
        let mut config = Config::default();
        config.db_backend = DbBackend::Test;

        let mut db_backend = db_backend::new(&config);
        let test_backend: &mut Test = db_backend.as_any().downcast_mut().unwrap();
        test_backend.buf.extend_from_slice(&fs::read("tests/test.kdbx").await.unwrap());

        let user_info = UserInfo::default();
        let keepass = KeePass::from_backend(&config, test_backend, &params, &user_info).await.unwrap();

        let (mut key, enc) = keepass.to_enc().unwrap();

        key.store(config.db_session_timeout).unwrap();
        let ret_key = SecretKey::retrieve(&key.key_id, config.db_session_timeout).unwrap();

        let dec = KeePass::from_enc(&config, ret_key, enc).unwrap();

        // can't clone, so we read in another one
        let keepass = KeePass::from_backend(&config, test_backend, &params, &user_info).await.unwrap();

        assert_eq!(keepass.db, dec.db);

        test_backend.buf = Vec::new();
        keepass.to_backend(test_backend, &params, &user_info).await.unwrap();

        // TODO: compare KeePass::to_backend result
    }

    #[tokio::test]
    async fn update_entry_changes_the_decoded_database() {
        fn first_entry_id(group: &keepass::db::Group) -> Option<uuid::Uuid> {
            for node in &group.children {
                match node {
                    Node::Entry(entry) => return Some(entry.uuid),
                    Node::Group(group) => if let Some(id) = first_entry_id(group) { return Some(id) },
                }
            }
            None
        }

        let config = Config::default();
        let database = KeePass::from_bytes(
            &config,
            Zeroizing::new(tokio::fs::read("tests/test.kdbx").await.unwrap()),
            Some(Zeroizing::new("test".to_owned())),
            None,
        ).await.unwrap();
        let entry_id = first_entry_id(&database.db.root).unwrap();
        let mut database = database;

        database.update_entry(entry_id, EntryUpdate {
            title: "changed by test".to_owned(),
            username: "test-user".to_owned(),
            password: "test-password".to_owned(),
            url: "https://example.test".to_owned(),
            notes: "test note".to_owned(),
        }).unwrap();

        assert_eq!(database.get_entry_by_id(entry_id).unwrap().title.as_deref(), Some("changed by test"));
    }

    #[tokio::test]
    async fn updated_entry_survives_kdbx_roundtrip() {
        fn first_entry_id(group: &keepass::db::Group) -> Option<uuid::Uuid> {
            for node in &group.children {
                match node {
                    Node::Entry(entry) => return Some(entry.uuid),
                    Node::Group(group) => if let Some(id) = first_entry_id(group) { return Some(id) },
                }
            }
            None
        }

        let config = Config::default();
        let mut database = KeePass::from_bytes(
            &config,
            Zeroizing::new(tokio::fs::read("tests/test.kdbx").await.unwrap()),
            Some(Zeroizing::new("test".to_owned())),
            None,
        ).await.unwrap();
        let entry_id = first_entry_id(&database.db.root).unwrap();
        database.update_entry(entry_id, EntryUpdate {
            title: "persisted title".to_owned(), username: "user".to_owned(), password: "password".to_owned(), url: "https://example.test".to_owned(), notes: "note".to_owned(),
        }).unwrap();
        let bytes = database.to_kdbx_bytes(Some(Zeroizing::new("test".to_owned())), None).unwrap();
        let reopened = KeePass::from_bytes(&config, bytes, Some(Zeroizing::new("test".to_owned())), None).await.unwrap();

        assert_eq!(reopened.get_entry_by_id(entry_id).unwrap().title.as_deref(), Some("persisted title"));
    }

    #[tokio::test]
    async fn new_empty_database_has_a_root_group_and_no_entries() {
        let config = Config::default();
        let database = KeePass::new_empty(&config);
        let (root, _) = database.get_groups().unwrap();
        let root_entries = database.get_group_entries_by_id(root.id).unwrap();
        assert_eq!(root_entries.entries.len(), 0);
    }

    #[tokio::test]
    async fn new_empty_database_survives_kdbx_roundtrip() {
        let config = Config::default();
        let database = KeePass::new_empty(&config);
        let bytes = database.to_kdbx_bytes(Some(Zeroizing::new("test".to_owned())), None).unwrap();
        let reopened = KeePass::from_bytes(&config, bytes, Some(Zeroizing::new("test".to_owned())), None).await.unwrap();
        let (root, _) = reopened.get_groups().unwrap();
        let root_entries = reopened.get_group_entries_by_id(root.id).unwrap();
        assert_eq!(root_entries.entries.len(), 0);
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

    #[tokio::test]
    async fn deleted_entry_remains_tombstoned_after_kdbx_roundtrip() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);
        let entry_id = database.create_entry(None, EntryUpdate {
            title: "Recover me".to_owned(), username: "alice".to_owned(), password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(), notes: "custom fields must survive".to_owned(),
        }).unwrap();

        let original_group_id = database.delete_entry(entry_id).unwrap();
        assert!(database.get_entry_by_id(entry_id).is_err());

        let bytes = database.to_kdbx_bytes(Some(Zeroizing::new("test".to_owned())), None).unwrap();
        let mut reopened = KeePass::from_bytes(&config, bytes, Some(Zeroizing::new("test".to_owned())), None).await.unwrap();
        reopened.restore_entry(entry_id, Some(original_group_id)).unwrap();

        let restored = reopened.get_entry_by_id(entry_id).unwrap();
        assert_eq!(restored.id, entry_id);
        assert_eq!(restored.title.as_deref(), Some("Recover me"));
        assert_eq!(restored.notes.as_deref(), Some("custom fields must survive"));
    }

    #[tokio::test]
    async fn entry_trash_and_restore_are_idempotent_for_transition_retries() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);
        let entry_id = database.create_entry(None, EntryUpdate {
            title: "Retry me".to_owned(), username: "alice".to_owned(), password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(), notes: "note".to_owned(),
        }).unwrap();

        let original_group = database.delete_entry(entry_id).unwrap();
        assert!(database.delete_entry(entry_id).is_ok());
        database.restore_entry(entry_id, Some(original_group)).unwrap();
        assert!(database.restore_entry(entry_id, Some(original_group)).is_ok());
        assert!(database.get_entry_by_id(entry_id).is_ok());
    }

    #[tokio::test]
    async fn restored_entry_keeps_its_original_uuid_and_custom_fields() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);
        let entry_id = database.create_entry(None, EntryUpdate {
            title: "Recover me".to_owned(), username: "alice".to_owned(), password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(), notes: "note".to_owned(),
        }).unwrap();
        let entry = KeePass::find_entry_by_id_mut(&mut database.db.root, &entry_id).unwrap();
        entry.fields.insert("Favourite colour".to_owned(), Value::Unprotected("green".to_owned()));

        let original_group_id = database.delete_entry(entry_id).unwrap();
        let bytes = database.to_kdbx_bytes(Some(Zeroizing::new("test".to_owned())), None).unwrap();
        let mut reopened = KeePass::from_bytes(&config, bytes, Some(Zeroizing::new("test".to_owned())), None).await.unwrap();
        reopened.restore_entry(entry_id, Some(original_group_id)).unwrap();

        let restored = KeePass::find_entry_by_id(&reopened.db.root, &entry_id).unwrap();
        assert_eq!(restored.uuid, entry_id);
        assert!(matches!(restored.fields.get("Favourite colour"), Some(Value::Unprotected(value)) if value == "green"));
    }

    #[tokio::test]
    async fn restoring_to_a_missing_preferred_group_falls_back_to_root() {
        let config = Config::default();
        let mut database = KeePass::new_empty(&config);
        let preferred_group = DbGroup::new("Preferred");
        let preferred_group_id = preferred_group.uuid;
        database.db.root.add_child(preferred_group);
        let entry_id = database.create_entry(Some(preferred_group_id), EntryUpdate {
            title: "Recover me".to_owned(), username: "alice".to_owned(), password: "hunter2".to_owned(),
            url: "https://example.test".to_owned(), notes: "note".to_owned(),
        }).unwrap();

        database.delete_entry(entry_id).unwrap();
        database.db.root.children.retain(|node| !matches!(node, Node::Group(group) if group.uuid == preferred_group_id));
        database.restore_entry(entry_id, Some(preferred_group_id)).unwrap();

        assert!(database.db.root.children.iter().any(|node| matches!(node, Node::Entry(entry) if entry.uuid == entry_id)));
    }
}
