package observability

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func sensitiveSentinels() map[string]any {
	return map[string]any{
		"request_body":    "sentinel-request-body",
		"authorization":   "sentinel-authorization-header",
		"password":        "sentinel-password",
		"key_file_bytes":  "sentinel-key-file-bytes",
		"token":           "sentinel-token",
		"decoded_data":    "sentinel-decoded-data",
		"protected_value": "sentinel-protected-value",
		"session_data":    "sentinel-session-data",
	}
}

func TestSensitiveSentinelsNeverReachSinks(t *testing.T) {
	sentinels := sensitiveSentinels()
	for _, sink := range []Sink{LogSink, TraceSink, ErrorSink, SessionSink} {
		fields := map[string]any{"correlation_id": "corr-test", "active_vault_handle": "opaque-handle"}
		for key, value := range sentinels {
			fields[key] = value
		}

		for key, value := range SafeFields(sink, fields) {
			if key != "active_vault_handle" && key != "correlation_id" {
				t.Fatalf("SafeFields(%q) admitted unexpected key %q", sink, key)
			}
			for _, sentinel := range sentinels {
				if value == sentinel {
					t.Fatalf("SafeFields(%q) leaked %q", sink, key)
				}
			}
		}
	}

	var output bytes.Buffer
	logger := NewLogger(&output)
	attributes := make([]any, 0, len(sentinels))
	for key, value := range sentinels {
		attributes = append(attributes, slog.Any(key, value))
	}
	logger.Info("request", attributes...)
	for _, value := range sentinels {
		if strings.Contains(output.String(), value.(string)) {
			t.Fatalf("JSON log leaked sensitive sentinel %q: %s", value, output.String())
		}
	}
}

func TestSafeFieldsAllowsOnlyDocumentedRequestAttributes(t *testing.T) {
	fields := map[string]any{
		"method":         "POST",
		"route_path":     "/healthz",
		"status":         200,
		"duration_ms":    4,
		"correlation_id": "corr-test",
		"raw_url":        "/healthz?secret=sentinel-query",
		"authorization":  "Bearer sentinel-authorization-header",
		"cookie":         "session=sentinel-cookie",
		"request_body":   "sentinel-request-body",
		"arbitrary":      "sentinel-arbitrary",
	}

	safe := SafeFields(LogSink, fields)
	if len(safe) != 5 {
		t.Fatalf("SafeFields(LogSink) field count = %d, want 5: %#v", len(safe), safe)
	}
	for _, key := range []string{"method", "route_path", "status", "duration_ms", "correlation_id"} {
		if _, ok := safe[key]; !ok {
			t.Fatalf("SafeFields(LogSink) missing allowed key %q", key)
		}
	}
	for _, forbidden := range []string{"raw_url", "authorization", "cookie", "request_body", "arbitrary"} {
		if _, ok := safe[forbidden]; ok {
			t.Fatalf("SafeFields(LogSink) admitted forbidden key %q", forbidden)
		}
	}
}

func TestPublicErrorContainsOnlyGenericMessageAndCorrelationID(t *testing.T) {
	correlationID := "0123456789abcdef0123456789abcdef"
	response := string(PublicError(correlationID))
	if !strings.Contains(response, correlationID) {
		t.Fatalf("PublicError() = %q, want correlation ID", response)
	}
	if !strings.Contains(response, "internal server error") {
		t.Fatalf("PublicError() = %q, want generic error message", response)
	}
	for _, value := range sensitiveSentinels() {
		if strings.Contains(response, value.(string)) {
			t.Fatalf("PublicError() leaked sensitive sentinel %q", value)
		}
	}
}
