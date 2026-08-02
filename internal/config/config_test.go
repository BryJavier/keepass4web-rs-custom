package config

import (
	"strings"
	"testing"
)

func TestLoadReportsMissingKeyWithoutConfigurationValue(t *testing.T) {
	values := map[string]string{
		"APP_ENV":             "sentinel-environment",
		"APP_LISTEN_ADDR":     "sentinel-listen-address",
		"APP_SESSION_SECRET":  "sentinel-session-secret",
		"SUPABASE_URL":        "sentinel-supabase-url",
		"SUPABASE_ANON_KEY":   "sentinel-supabase-anon-key",
		"SUPABASE_JWT_ISSUER": "sentinel-jwt-issuer",
		"RUST_SERVICE_URL":    "sentinel-rust-url",
		"RUST_SERVICE_TOKEN":  "sentinel-rust-token",
	}
	delete(values, "RUST_SERVICE_TOKEN")

	_, err := Load(func(key string) string { return values[key] })
	if err == nil {
		t.Fatal("Load() error = nil, want missing configuration error")
	}
	if !strings.Contains(err.Error(), "RUST_SERVICE_TOKEN") {
		t.Fatalf("Load() error = %q, want missing key name", err)
	}
	for _, value := range values {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("Load() error = %q leaked configuration value %q", err, value)
		}
	}
}
