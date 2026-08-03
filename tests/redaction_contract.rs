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
