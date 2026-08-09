package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lixmal/keepass4web-rs/backend-go/internal/config"
	"github.com/lixmal/keepass4web-rs/backend-go/internal/observability"
	"github.com/lixmal/keepass4web-rs/backend-go/internal/privateclient"
	"github.com/lixmal/keepass4web-rs/backend-go/internal/supabase"
	"github.com/lixmal/keepass4web-rs/backend-go/internal/web"
)

type Config = config.Config

// serviceRoleTrashPurger keeps the Supabase service-role credential at the
// composition root. The web package receives only its narrow purge ability.
type serviceRoleTrashPurger struct {
	client         *supabase.VaultClient
	serviceRoleKey string
}

func (p serviceRoleTrashPurger) PurgeExpiredTrash(ctx context.Context) error {
	return p.client.PurgeExpiredTrash(ctx, p.serviceRoleKey)
}

func loadConfig(getenv func(string) string) (Config, error) {
	return config.Load(getenv)
}

func newHandler(logger *slog.Logger) http.Handler {
	return newHandlerWithDependencies(logger, web.Dependencies{})
}

func newHandlerWithDependencies(logger *slog.Logger, deps web.Dependencies) http.Handler {
	if logger == nil {
		logger = observability.NewLogger(io.Discard)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("ok\n"))
	})
	app := web.NewApp(deps)
	mux.Handle("/", app)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir()))))
	return safeRequestEvents(logger, mux)
}

// staticDir locates frontend/web/static relative to the process's working
// directory, walking up from cwd when needed (e.g. under `go test`, which
// runs with cwd set to the package directory rather than the repo root). In
// the runtime container, cwd is the app's WORKDIR and frontend/web/static
// lives directly beneath it, so the walk terminates immediately.
func staticDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return filepath.Join("frontend", "web", "static")
	}
	for {
		candidate := filepath.Join(dir, "frontend", "web", "static")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join("frontend", "web", "static")
		}
		dir = parent
	}
}

func run(getenv func(string) string, serve func(Config, http.Handler) error) error {
	return runWithLogger(getenv, observability.NewLogger(io.Discard), serve)
}

func runWithLogger(getenv func(string) string, logger *slog.Logger, serve func(Config, http.Handler) error) error {
	configuration, err := loadConfig(getenv)
	if err != nil {
		observability.LogEvent(logger, slog.LevelError, "startup_failed", map[string]any{
			"event":          "startup_failed",
			"correlation_id": newCorrelationID(),
			"config_key":     config.MissingKey(err),
		})
		return err
	}
	auth, err := supabase.NewAuthClient(configuration.SupabaseServerURL, configuration.SupabaseAnonKey, configuration.SupabaseJWTIssuer)
	if err != nil { return err }
	vaults, err := supabase.NewVaultClient(configuration.SupabaseServerURL, configuration.SupabaseAnonKey, nil)
	if err != nil { return err }
	var trashPurger web.TrashPurger
	if configuration.SupabaseServiceRoleKey != "" {
		serverVaults, err := supabase.NewVaultClient(configuration.SupabaseServerURL, configuration.SupabaseServiceRoleKey, nil)
		if err != nil { return err }
		trashPurger = serviceRoleTrashPurger{client: serverVaults, serviceRoleKey: configuration.SupabaseServiceRoleKey}
	}
	rust, err := privateclient.New(configuration.RustServiceURL, configuration.RustServiceToken, nil)
	if err != nil { return err }
	return serve(configuration, newHandlerWithDependencies(logger, web.Dependencies{
		Auth:               auth,
		Preferences:        web.SupabaseVaults{Client: vaults},
		SessionVaults:      web.SupabaseVaults{Client: vaults},
		Rust:               rust,
		SecureCookies:      configuration.Environment != "test" && configuration.Environment != "development",
		SupabaseURL:        configuration.SupabaseURL,
		SupabaseAnonKey:    configuration.SupabaseAnonKey,
		TrashPurger:        trashPurger,
		TrashPurgeInterval: configuration.VaultTrashPurgeInterval,
		PurgeError: func() {
			observability.LogEvent(logger, slog.LevelError, "server_error", map[string]any{
				"event":          "server_error",
				"correlation_id": newCorrelationID(),
			})
		},
	}))
}

func writePublicError(writer http.ResponseWriter, logger *slog.Logger, correlationID string) {
	observability.LogEvent(logger, slog.LevelError, "server_error", map[string]any{
		"event":          "server_error",
		"correlation_id": correlationID,
	})
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(http.StatusInternalServerError)
	_, _ = writer.Write(observability.PublicError(correlationID))
}

func safeRequestEvents(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorded := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorded, request)

		observability.LogEvent(logger, slog.LevelInfo, "request_complete", map[string]any{
			"method":         safeMethod(request.Method),
			"route_path":     safeRoutePath(request.URL.Path),
			"status":         recorded.status,
			"duration_ms":    time.Since(started).Milliseconds(),
			"correlation_id": newCorrelationID(),
		})
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (writer *statusRecorder) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusRecorder) Write(body []byte) (int, error) {
	return writer.ResponseWriter.Write(body)
}

func safeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func safeRoutePath(path string) string {
	if path == "/healthz" {
		return path
	}
	return "/unknown"
}

func newCorrelationID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(bytes[:])
}

func main() {
	logger := observability.NewLogger(io.MultiWriter(os.Stdout, os.Stderr))
	if err := runWithLogger(os.Getenv, logger, func(configuration Config, handler http.Handler) error {
		return http.ListenAndServe(configuration.ListenAddr, handler)
	}); err != nil {
		os.Exit(1)
	}
}
