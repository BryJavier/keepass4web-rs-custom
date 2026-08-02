package main

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/lixmal/keepass4web-rs/internal/config"
	"github.com/lixmal/keepass4web-rs/internal/observability"
)

type Config = config.Config

func loadConfig(getenv func(string) string) (Config, error) {
	return config.Load(getenv)
}

func newHandler(logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = observability.NewLogger(io.Discard)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("ok\n"))
	})
	return safeRequestEvents(logger, mux)
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
	return serve(configuration, newHandler(logger))
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
