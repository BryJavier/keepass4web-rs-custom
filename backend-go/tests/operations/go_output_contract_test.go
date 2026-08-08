package operations

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lixmal/keepass4web-rs/backend-go/internal/observability"
)

var goOutputSentinels = []string{
	"sentinel-request-body",
	"sentinel-authorization-header",
	"sentinel-password",
	"sentinel-key-file-bytes",
	"sentinel-token",
	"sentinel-decoded-data",
	"sentinel-protected-value",
	"sentinel-session-data",
	"sentinel-service-role-key",
}

func TestGoOutputContract(t *testing.T) {
	root := repositoryRoot(t)
	binary := buildWebBinary(t, root)
	address := reserveLoopbackAddress(t)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	process := exec.Command(binary)
	process.Dir = root
	process.Env = webEnvironment(address)
	process.Stdout = &stdout
	process.Stderr = &stderr
	if err := process.Start(); err != nil {
		t.Fatalf("start Go web process: %v", err)
	}
	t.Cleanup(func() {
		if process.Process != nil {
			_ = process.Process.Kill()
		}
		_ = process.Wait()
	})

	client := &http.Client{Timeout: 100 * time.Millisecond}
	waitForHealthz(t, client, address)

	success := sendHostileRequest(t, client, "http://"+address+"/healthz?token=sentinel-token", http.StatusOK)
	failure := sendHostileRequest(t, client, "http://"+address+"/missing/sentinel-decoded-data?token=sentinel-token", http.StatusNotFound)

	requireNonEmptyCapture(t, "stdout", stdout.String())
	requireNonEmptyCapture(t, "structured stderr", stderr.String())
	requireNonEmptyCapture(t, "successful HTTP response", success)
	requireNonEmptyCapture(t, "failing HTTP response", failure)

	traceCapture, err := json.Marshal(observability.SafeFields(observability.TraceSink, hostileFields()))
	if err != nil {
		t.Fatalf("marshal trace capture: %v", err)
	}
	sessionCapture, err := json.Marshal(observability.SafeFields(observability.SessionSink, hostileFields()))
	if err != nil {
		t.Fatalf("marshal session capture: %v", err)
	}
	requireNonEmptyCapture(t, "trace fields", string(traceCapture))
	requireNonEmptyCapture(t, "session fields", string(sessionCapture))

	assertNoSentinels(t, "stdout", stdout.String())
	assertNoSentinels(t, "stderr", stderr.String())
	assertNoSentinels(t, "successful HTTP response", success)
	assertNoSentinels(t, "failing HTTP response", failure)
	assertNoSentinels(t, "trace fields", string(traceCapture))
	assertNoSentinels(t, "session fields", string(sessionCapture))
	assertSafeJSONEvents(t, stderr.String())
}

func TestRequireNonEmptyCaptureFailsClosed(t *testing.T) {
	if err := captureError("expected sink", ""); err == nil {
		t.Fatal("empty expected output sink was accepted")
	}
}

func buildWebBinary(t *testing.T, root string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "web")
	command := exec.Command("go", "build", "-o", binary, "./backend-go/cmd/web")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build web binary: %v\n%s", err, output)
	}
	return binary
}

func reserveLoopbackAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback address: %v", err)
	}
	return address
}

func webEnvironment(address string) []string {
	return []string{
		"APP_ENV=sentinel-environment",
		"APP_LISTEN_ADDR=" + address,
		"APP_SESSION_SECRET=sentinel-password",
		"SUPABASE_URL=https://sentinel-token.example",
		"SUPABASE_ANON_KEY=sentinel-authorization-header",
		"SUPABASE_SERVICE_ROLE_KEY=sentinel-service-role-key",
		"SUPABASE_JWT_ISSUER=https://sentinel-jwt-issuer.example/auth/v1",
		"RUST_SERVICE_URL=http://sentinel-decoded-data.example",
		"RUST_SERVICE_TOKEN=sentinel-protected-value",
	}
}

func waitForHealthz(t *testing.T, client *http.Client, address string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/healthz")
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("web process did not serve GET /healthz before deadline")
}

func sendHostileRequest(t *testing.T, client *http.Client, target string, wantStatus int) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, strings.NewReader(strings.Join(goOutputSentinels, ",")))
	if err != nil {
		t.Fatalf("create hostile request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer sentinel-authorization-header")
	request.Header.Set("Cookie", "session=sentinel-session-data")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send hostile request: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read hostile response: %v", err)
	}
	if response.StatusCode != wantStatus {
		t.Fatalf("hostile request status = %d, want %d; body=%q", response.StatusCode, wantStatus, body)
	}
	return string(body)
}

func hostileFields() map[string]any {
	return map[string]any{
		"method":              "GET",
		"route_path":          "/healthz",
		"status":              200,
		"duration_ms":         int64(1),
		"correlation_id":      "0123456789abcdef0123456789abcdef",
		"active_vault_handle": "opaque-active-vault-handle",
		"request_body":        "sentinel-request-body",
		"authorization":       "sentinel-authorization-header",
		"password":            "sentinel-password",
		"key_file_bytes":      "sentinel-key-file-bytes",
		"token":               "sentinel-token",
		"decoded_data":        "sentinel-decoded-data",
		"protected_value":     "sentinel-protected-value",
		"session_data":        "sentinel-session-data",
	}
}

func requireNonEmptyCapture(t *testing.T, sink, capture string) {
	t.Helper()
	if err := captureError(sink, capture); err != nil {
		t.Fatal(err)
	}
}

func captureError(sink, capture string) error {
	if strings.TrimSpace(capture) == "" {
		return fmt.Errorf("%s capture is empty", sink)
	}
	return nil
}

func assertNoSentinels(t *testing.T, sink, capture string) {
	t.Helper()
	for _, sentinel := range goOutputSentinels {
		if strings.Contains(capture, sentinel) {
			t.Fatalf("%s leaked %q: %s", sink, sentinel, capture)
		}
	}
}

func assertSafeJSONEvents(t *testing.T, capture string) {
	t.Helper()
	allowed := map[string]struct{}{
		"time": {}, "level": {}, "msg": {}, "method": {}, "route_path": {}, "status": {}, "duration_ms": {}, "correlation_id": {},
	}
	for _, line := range strings.Split(strings.TrimSpace(capture), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("structured event is not JSON: %v; line=%q", err, line)
		}
		for key := range event {
			if _, ok := allowed[key]; !ok {
				t.Fatalf("structured event contains undocumented key %q: %#v", key, event)
			}
		}
	}
}
