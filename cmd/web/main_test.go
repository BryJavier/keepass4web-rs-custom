package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
