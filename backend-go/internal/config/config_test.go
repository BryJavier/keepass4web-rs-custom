package config

import (
	"strings"
	"testing"
	"time"
)

func completeEnvironment() map[string]string {
	return map[string]string{
		"APP_ENV":                   "production",
		"APP_LISTEN_ADDR":           ":8080",
		"APP_SESSION_SECRET":        "session-secret",
		"SUPABASE_URL":              "https://project.supabase.co",
		"SUPABASE_ANON_KEY":         "anon-key",
		"SUPABASE_JWT_ISSUER":       "https://project.supabase.co/auth/v1",
		"RUST_SERVICE_URL":          "http://rust-service:8080",
		"RUST_SERVICE_TOKEN":        "rust-service-token",
		"SUPABASE_SERVICE_ROLE_KEY": "service-role-key",
	}
}

func TestLoadReportsMissingKeyWithoutConfigurationValue(t *testing.T) {
	values := completeEnvironment()
	values["APP_ENV"] = "sentinel-environment"
	values["APP_LISTEN_ADDR"] = "sentinel-listen-address"
	values["APP_SESSION_SECRET"] = "sentinel-session-secret"
	values["SUPABASE_URL"] = "sentinel-supabase-url"
	values["SUPABASE_ANON_KEY"] = "sentinel-supabase-anon-key"
	values["SUPABASE_JWT_ISSUER"] = "sentinel-jwt-issuer"
	values["RUST_SERVICE_URL"] = "sentinel-rust-url"
	values["RUST_SERVICE_TOKEN"] = "sentinel-rust-token"
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

func TestLoadRequiresServiceRoleKeyInProductionWithoutLeakingIt(t *testing.T) {
	values := completeEnvironment()
	values["APP_SESSION_SECRET"] = "sentinel-session-secret"
	delete(values, "SUPABASE_SERVICE_ROLE_KEY")

	_, err := Load(func(key string) string { return values[key] })
	if err == nil || !strings.Contains(err.Error(), "SUPABASE_SERVICE_ROLE_KEY") {
		t.Fatalf("Load() error = %v, want missing service-role key", err)
	}
	if strings.Contains(err.Error(), "sentinel-session-secret") {
		t.Fatalf("Load() error leaked configuration value: %q", err)
	}
}

func TestLoadUsesDefaultPurgeIntervalAndRejectsNonPositiveValues(t *testing.T) {
	values := completeEnvironment()
	configuration, err := Load(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	if configuration.VaultTrashPurgeInterval != time.Hour {
		t.Fatalf("default purge interval = %s, want %s", configuration.VaultTrashPurgeInterval, time.Hour)
	}
	values["VAULT_TRASH_PURGE_INTERVAL"] = "2m"
	configuration, err = Load(func(key string) string { return values[key] })
	if err != nil || configuration.VaultTrashPurgeInterval != 2*time.Minute {
		t.Fatalf("custom purge interval = %s, error = %v; want 2m", configuration.VaultTrashPurgeInterval, err)
	}

	for _, value := range []string{"0", "-1m", "not-a-duration"} {
		t.Run(value, func(t *testing.T) {
			values := completeEnvironment()
			values["VAULT_TRASH_PURGE_INTERVAL"] = value
			_, err := Load(func(key string) string { return values[key] })
			if err == nil || !strings.Contains(err.Error(), "VAULT_TRASH_PURGE_INTERVAL") {
				t.Fatalf("Load() error = %v, want invalid purge interval", err)
			}
		})
	}
}
