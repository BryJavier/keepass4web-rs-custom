package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lixmal/keepass4web-rs/backend-go/internal/observability"
)

func completeEnvironment() map[string]string {
	return map[string]string{
		"APP_ENV":             "test",
		"APP_LISTEN_ADDR":     ":8080",
		"APP_SESSION_SECRET":  "test-session-secret",
		"SUPABASE_URL":        "https://project.supabase.co",
		"SUPABASE_ANON_KEY":   "test-anon-key",
		"SUPABASE_JWT_ISSUER": "https://project.supabase.co/auth/v1",
		"RUST_SERVICE_URL":    "http://rust-service:8080",
		"RUST_SERVICE_TOKEN":  "test-rust-service-token",
	}
}

func envGetter(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestRunServesHealthzForValidConfiguration(t *testing.T) {
	called := false
	err := run(envGetter(completeEnvironment()), func(config Config, handler http.Handler) error {
		called = true
		if config.ListenAddr != ":8080" {
			t.Fatalf("listen address = %q, want :8080", config.ListenAddr)
		}

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET /healthz status = %d, want %d", recorder.Code, http.StatusOK)
		}
		if recorder.Body.String() != "ok\n" {
			t.Fatalf("GET /healthz body = %q, want %q", recorder.Body.String(), "ok\\n")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !called {
		t.Fatal("server was not started for valid configuration")
	}
}

func TestRunRejectsEveryMissingRequiredKeyBeforeStartingServer(t *testing.T) {
	for missingKey := range completeEnvironment() {
		t.Run(missingKey, func(t *testing.T) {
			values := completeEnvironment()
			delete(values, missingKey)
			called := false

			err := run(envGetter(values), func(Config, http.Handler) error {
				called = true
				return nil
			})
			if err == nil {
				t.Fatal("run() error = nil, want missing configuration error")
			}
			if !strings.Contains(err.Error(), missingKey) {
				t.Fatalf("run() error = %q, want missing key %q", err, missingKey)
			}
			for _, suppliedValue := range values {
				if strings.Contains(err.Error(), suppliedValue) {
					t.Fatalf("run() error = %q leaked supplied configuration", err)
				}
			}
			if called {
				t.Fatal("server was started despite missing configuration")
			}
		})
	}
}

func TestStartupFailureLogsOnlyTheConfigurationKeyAndCorrelationID(t *testing.T) {
	values := completeEnvironment()
	values["APP_SESSION_SECRET"] = "sentinel-session-secret"
	values["RUST_SERVICE_TOKEN"] = "sentinel-rust-service-token"
	delete(values, "SUPABASE_ANON_KEY")

	var output bytes.Buffer
	err := runWithLogger(envGetter(values), observability.NewLogger(&output), func(Config, http.Handler) error {
		t.Fatal("server must not start with invalid configuration")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "SUPABASE_ANON_KEY") {
		t.Fatalf("runWithLogger() error = %v, want missing key name", err)
	}
	for _, sensitive := range []string{"sentinel-session-secret", "sentinel-rust-service-token"} {
		if strings.Contains(err.Error(), sensitive) || strings.Contains(output.String(), sensitive) {
			t.Fatalf("startup configuration failure leaked %q", sensitive)
		}
	}
	if !strings.Contains(output.String(), "SUPABASE_ANON_KEY") || !strings.Contains(output.String(), "correlation_id") {
		t.Fatalf("startup diagnostic is missing safe fields: %s", output.String())
	}
}

func TestHealthRequestEventUsesOnlySafeRequestFields(t *testing.T) {
	var output bytes.Buffer
	logger := observability.NewLogger(&output)
	handler := newHandler(logger)

	request := httptest.NewRequest(http.MethodGet, "/healthz?token=sentinel-query", strings.NewReader("sentinel-request-body"))
	request.Header.Set("Authorization", "Bearer sentinel-authorization")
	request.Header.Set("Cookie", "session=sentinel-cookie")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("request event is not JSON: %v; output=%q", err, output.String())
	}
	for _, key := range []string{"time", "level", "msg", "method", "route_path", "status", "duration_ms", "correlation_id"} {
		if _, ok := event[key]; !ok {
			t.Fatalf("request event missing key %q: %#v", key, event)
		}
	}
	for key := range event {
		switch key {
		case "time", "level", "msg", "method", "route_path", "status", "duration_ms", "correlation_id":
		default:
			t.Fatalf("request event admitted undocumented key %q: %#v", key, event)
		}
	}
	for _, sentinel := range []string{"sentinel-query", "sentinel-request-body", "sentinel-authorization", "sentinel-cookie"} {
		if strings.Contains(output.String(), sentinel) {
			t.Fatalf("request event leaked sentinel %q: %s", sentinel, output.String())
		}
	}
}

func TestServerErrorResponseIsGenericAndCorrelated(t *testing.T) {
	var output bytes.Buffer
	logger := observability.NewLogger(&output)
	recorder := httptest.NewRecorder()

	correlationID := "0123456789abcdef0123456789abcdef"
	writePublicError(recorder, logger, correlationID)

	response, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("server error status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if string(response) != string(observability.PublicError(correlationID)) {
		t.Fatalf("server error response = %q, want generic public error", response)
	}
	if !strings.Contains(output.String(), correlationID) {
		t.Fatalf("safe internal event does not contain correlation ID: %s", output.String())
	}
	for _, sentinel := range []string{"sentinel-request-body", "sentinel-authorization", "sentinel-password", "sentinel-key-file"} {
		if strings.Contains(string(response), sentinel) || strings.Contains(output.String(), sentinel) {
			t.Fatalf("server error boundary leaked sentinel %q", sentinel)
		}
	}
}
