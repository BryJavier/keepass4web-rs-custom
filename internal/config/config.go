package config

import (
	"errors"
	"fmt"
	"strings"
)

// Config contains the runtime configuration required before the public server binds.
type Config struct {
	Environment       string
	ListenAddr        string
	SessionSecret     string
	SupabaseURL       string
	SupabaseAnonKey   string
	SupabaseJWTIssuer string
	RustServiceURL    string
	RustServiceToken  string
}

type field struct {
	key string
	set func(*Config, string)
}

var requiredFields = []field{
	{"APP_ENV", func(config *Config, value string) { config.Environment = value }},
	{"APP_LISTEN_ADDR", func(config *Config, value string) { config.ListenAddr = value }},
	{"APP_SESSION_SECRET", func(config *Config, value string) { config.SessionSecret = value }},
	{"SUPABASE_URL", func(config *Config, value string) { config.SupabaseURL = value }},
	{"SUPABASE_ANON_KEY", func(config *Config, value string) { config.SupabaseAnonKey = value }},
	{"SUPABASE_JWT_ISSUER", func(config *Config, value string) { config.SupabaseJWTIssuer = value }},
	{"RUST_SERVICE_URL", func(config *Config, value string) { config.RustServiceURL = value }},
	{"RUST_SERVICE_TOKEN", func(config *Config, value string) { config.RustServiceToken = value }},
}

// ValidationError identifies an invalid key without retaining its supplied value.
type ValidationError struct {
	Key string
}

func (error *ValidationError) Error() string {
	return fmt.Sprintf("missing required configuration: %s", error.Key)
}

// Load validates the Plan 01 environment contract without exposing runtime values.
func Load(getenv func(string) string) (Config, error) {
	var config Config
	for _, field := range requiredFields {
		value := strings.TrimSpace(getenv(field.key))
		if value == "" {
			return Config{}, &ValidationError{Key: field.key}
		}
		field.set(&config, value)
	}
	return config, nil
}

// MissingKey returns a safe configuration key name, if an error came from Load.
func MissingKey(err error) string {
	var validationError *ValidationError
	if errors.As(err, &validationError) {
		return validationError.Key
	}
	return "UNKNOWN"
}
