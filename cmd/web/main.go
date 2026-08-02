package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

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

type configField struct {
	key string
	set func(*Config, string)
}

var requiredConfigFields = []configField{
	{"APP_ENV", func(config *Config, value string) { config.Environment = value }},
	{"APP_LISTEN_ADDR", func(config *Config, value string) { config.ListenAddr = value }},
	{"APP_SESSION_SECRET", func(config *Config, value string) { config.SessionSecret = value }},
	{"SUPABASE_URL", func(config *Config, value string) { config.SupabaseURL = value }},
	{"SUPABASE_ANON_KEY", func(config *Config, value string) { config.SupabaseAnonKey = value }},
	{"SUPABASE_JWT_ISSUER", func(config *Config, value string) { config.SupabaseJWTIssuer = value }},
	{"RUST_SERVICE_URL", func(config *Config, value string) { config.RustServiceURL = value }},
	{"RUST_SERVICE_TOKEN", func(config *Config, value string) { config.RustServiceToken = value }},
}

func loadConfig(getenv func(string) string) (Config, error) {
	var config Config
	for _, field := range requiredConfigFields {
		value := strings.TrimSpace(getenv(field.key))
		if value == "" {
			return Config{}, fmt.Errorf("missing required configuration: %s", field.key)
		}
		field.set(&config, value)
	}
	return config, nil
}

func newHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("ok\n"))
	})
	return mux
}

func run(getenv func(string) string, serve func(Config, http.Handler) error) error {
	config, err := loadConfig(getenv)
	if err != nil {
		return err
	}
	return serve(config, newHandler())
}

func main() {
	if err := run(os.Getenv, func(config Config, handler http.Handler) error {
		return http.ListenAndServe(config.ListenAddr, handler)
	}); err != nil {
		log.Fatal(err)
	}
}
