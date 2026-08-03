use std::fs;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

const AUTH_SENTINEL: &str = "authorization-sentinel-01-05";
const QUERY_SENTINEL: &str = "query-sentinel-01-05";
const COOKIE_SENTINEL: &str = "cookie-sentinel-01-05";
const BODY_SENTINEL: &str = "body-sentinel-01-05";
const USERNAME_SENTINEL: &str = "username-sentinel-01-05";
const PASSWORD_SENTINEL: &str = "password-sentinel-01-05";
const KEYFILE_SENTINEL: &str = "keyfile-sentinel-01-05";
const TOKEN_SENTINEL: &str = "token-sentinel-01-05";
const SESSION_SENTINEL: &str = "session-sentinel-01-05";

struct RunningServer {
    child: Child,
    config: PathBuf,
    port: u16,
}

impl RunningServer {
    fn start() -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").expect("reserve loopback port");
        let port = listener.local_addr().expect("read listener address").port();
        drop(listener);

        let config = temporary_config(port);
        let binary = env!("CARGO_BIN_EXE_keepass4web-rs");
        let child = Command::new(binary)
            .arg("--config")
            .arg(&config)
            .env("RUST_LOG", "info")
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .expect("spawn Rust server");

        let server = Self { child, config, port };
        server.wait_until_ready();
        server
    }

    fn request(&self, request: &str) -> String {
        let mut stream = TcpStream::connect(("127.0.0.1", self.port)).expect("connect to server");
        stream.write_all(request.as_bytes()).expect("write request");
        stream.shutdown(std::net::Shutdown::Write).expect("finish request");
        let mut response = String::new();
        stream.read_to_string(&mut response).expect("read response");
        response
    }

    fn wait_until_ready(&self) {
        let deadline = std::time::Instant::now() + Duration::from_secs(10);
        while std::time::Instant::now() < deadline {
            if TcpStream::connect(("127.0.0.1", self.port)).is_ok() {
                return;
            }
            thread::sleep(Duration::from_millis(25));
        }
        panic!("server did not become reachable");
    }

    fn finish(mut self) -> (String, String) {
        let _ = self.child.kill();
        let output = self.child.wait_with_output().expect("collect server output");
        let _ = fs::remove_file(&self.config);
        (
            String::from_utf8(output.stdout).expect("stdout is UTF-8"),
            String::from_utf8(output.stderr).expect("stderr is UTF-8"),
        )
    }
}

fn temporary_config(port: u16) -> PathBuf {
    let mut config = fs::read_to_string("tests/config.test.yml").expect("read test config");
    config = config.replace("port: 8080", &format!("port: {port}"));
    config.push_str("\nsession_secret_key: 0123456789012345678901234567890123456789012345678901234567890123\n");

    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("system clock")
        .as_nanos();
    let path = std::env::temp_dir().join(format!("keepass4web-redaction-{nonce}.yml"));
    fs::write(&path, config).expect("write temporary config");
    path
}

fn assert_non_empty(label: &str, output: &str) {
    assert!(!output.trim().is_empty(), "{label} capture is empty");
}

fn assert_sentinels_absent(output: &str) {
    for sentinel in [AUTH_SENTINEL, QUERY_SENTINEL, COOKIE_SENTINEL, BODY_SENTINEL] {
        assert!(!output.contains(sentinel), "sensitive sentinel leaked: {sentinel}");
    }
}

fn assert_auth_sentinels_absent(output: &str) {
    for sentinel in [
        USERNAME_SENTINEL,
        PASSWORD_SENTINEL,
        KEYFILE_SENTINEL,
        TOKEN_SENTINEL,
        SESSION_SENTINEL,
    ] {
        assert!(!output.contains(sentinel), "sensitive sentinel leaked: {sentinel}");
    }
}

fn response_correlation_id(response: &str) -> String {
    let marker = "\"correlation_id\":\"";
    let start = response.find(marker).expect("generic error exposes a correlation ID") + marker.len();
    let end = response[start..].find('"').expect("correlation ID is terminated") + start;
    response[start..end].to_string()
}

#[test]
fn hostile_request() {
    let server = RunningServer::start();
    let response = server.request(&format!(
        "POST /missing?query={QUERY_SENTINEL} HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: Bearer {AUTH_SENTINEL}\r\nCookie: session={COOKIE_SENTINEL}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{BODY_SENTINEL}",
        BODY_SENTINEL.len(),
    ));
    let (stdout, stderr) = server.finish();

    assert_non_empty("HTTP response", &response);
    assert_non_empty("stdout", &stdout);
    assert_non_empty("stderr", &stderr);
    assert_sentinels_absent(&response);
    assert_sentinels_absent(&stdout);
    assert_sentinels_absent(&stderr);
    assert!(response.contains("\"message\":\"request failed\""), "error response was not generic: {response}");
    let correlation_id = response_correlation_id(&response);
    assert!(stdout.contains(&format!("correlation_id={correlation_id}")), "event does not share error correlation ID: {stdout}");
    for field in ["method=POST", "path=/missing", "status=404", "duration_ms=", "correlation_id="] {
        assert!(stdout.contains(field), "safe request event missing {field}: {stdout}");
    }
    assert!(!stdout.contains("HTTP/1.1"), "raw request line leaked: {stdout}");
}

#[test]
fn auth_and_session() {
    let server = RunningServer::start();
    let login_body = format!("username={USERNAME_SENTINEL}&password={PASSWORD_SENTINEL}");
    let login = server.request(&format!(
        "POST /api/v1/user_login HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{login_body}",
        login_body.len(),
    ));
    assert!(login.contains("HTTP/1.1 200"), "login failed: {login}");

    let cookie = login
        .lines()
        .find_map(|line| line.strip_prefix("set-cookie: ").or_else(|| line.strip_prefix("Set-Cookie: ")))
        .and_then(|value| value.split(';').next())
        .expect("login response sets a session cookie");
    let csrf_marker = "\"csrf_token\":\"";
    let csrf_start = login.find(csrf_marker).expect("login response contains CSRF token") + csrf_marker.len();
    let csrf_end = login[csrf_start..].find('"').expect("CSRF token is terminated") + csrf_start;
    let csrf_token = &login[csrf_start..csrf_end];

    let unlock_body = format!("password={PASSWORD_SENTINEL}&key={KEYFILE_SENTINEL}");
    let unlock = server.request(&format!(
        "POST /api/v1/db_login HTTP/1.1\r\nHost: 127.0.0.1\r\nCookie: {cookie}; supplied={SESSION_SENTINEL}\r\nX-CSRF-Token: {csrf_token}\r\nAuthorization: Bearer {TOKEN_SENTINEL}\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{unlock_body}",
        unlock_body.len(),
    ));
    let (stdout, stderr) = server.finish();

    assert_non_empty("login response", &login);
    assert_non_empty("unlock response", &unlock);
    assert_non_empty("stdout", &stdout);
    assert_non_empty("stderr", &stderr);
    assert_auth_sentinels_absent(&login);
    assert_auth_sentinels_absent(&unlock);
    assert_auth_sentinels_absent(cookie);
    assert_auth_sentinels_absent(&stdout);
    assert_auth_sentinels_absent(&stderr);

    for source in [
        include_str!("../src/auth.rs"),
        include_str!("../src/session.rs"),
        include_str!("../src/server/route/auth.rs"),
        include_str!("../src/server/route/util.rs"),
    ] {
        assert!(!source.contains("info!(") && !source.contains("error!("), "legacy formatted diagnostic remains in an auth/session path");
    }
}

#[test]
fn keepass_and_cache() {
    let server = RunningServer::start();
    let response = server.request(&format!(
        "GET /api/v1/search_entries?term=search-sentinel-01-05&entry_id=entry-sentinel-01-05&name=protected-sentinel-01-05 HTTP/1.1\r\nHost: 127.0.0.1\r\nCookie: cache-session-sentinel-01-05\r\nConnection: close\r\n\r\n",
    ));
    let (stdout, stderr) = server.finish();

    for output in [&response, &stdout, &stderr] {
        for sentinel in ["search-sentinel-01-05", "entry-sentinel-01-05", "protected-sentinel-01-05", "cache-session-sentinel-01-05"] {
            assert!(!output.contains(sentinel), "sensitive KeePass/cache sentinel leaked: {sentinel}");
        }
    }
    for source in [include_str!("../src/server/route/keepass.rs"), include_str!("../src/keepass/db_cache.rs")] {
        assert!(!source.contains("info!(") && !source.contains("error!("), "legacy formatted diagnostic remains in a KeePass/cache path");
    }
}
