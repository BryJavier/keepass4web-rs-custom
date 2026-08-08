// Package observability provides the Go process's deny-by-default output boundary.
package observability

import (
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
)

// Sink identifies an output boundary with its own allow list.
type Sink string

const (
	LogSink     Sink = "log"
	TraceSink   Sink = "trace"
	ErrorSink   Sink = "error"
	SessionSink Sink = "session"
)

var allowedFields = map[Sink]map[string]struct{}{
	LogSink: {
		"event": {}, "method": {}, "route_path": {}, "status": {}, "duration_ms": {}, "correlation_id": {}, "config_key": {},
	},
	TraceSink: {
		"method": {}, "route_path": {}, "status": {}, "duration_ms": {}, "correlation_id": {},
	},
	ErrorSink: {
		"correlation_id": {},
	},
	SessionSink: {
		"active_vault_handle": {},
	},
}

// SafeFields drops every field that the given sink does not explicitly admit.
func SafeFields(sink Sink, fields map[string]any) map[string]any {
	allowed := allowedFields[sink]
	safe := make(map[string]any, len(allowed))
	for key, value := range fields {
		if _, ok := allowed[key]; ok {
			safe[key] = value
		}
	}
	return safe
}

// NewLogger returns a JSON logger whose handler drops unrecognized or grouped attributes.
func NewLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: replaceAttr}))
}

func replaceAttr(groups []string, attribute slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return slog.Attr{}
	}

	switch attribute.Key {
	case slog.TimeKey, slog.LevelKey:
		return attribute
	case slog.MessageKey:
		if isSafeMessage(attribute.Value.String()) {
			return attribute
		}
		return slog.String(slog.MessageKey, "safe_event")
	case "event":
		if isSafeEvent(attribute.Value.String()) {
			return attribute
		}
	case "method":
		if isSafeMethod(attribute.Value.String()) {
			return attribute
		}
	case "route_path":
		if attribute.Value.String() == "/healthz" || attribute.Value.String() == "/unknown" {
			return attribute
		}
	case "status":
		if attribute.Value.Kind() == slog.KindInt64 && attribute.Value.Int64() >= 100 && attribute.Value.Int64() <= 599 {
			return attribute
		}
	case "duration_ms":
		if duration, ok := attribute.Value.Any().(int64); ok && duration >= 0 {
			return attribute
		}
	case "correlation_id":
		if isSafeCorrelationID(attribute.Value.String()) {
			return attribute
		}
	case "config_key":
		if isSafeConfigKey(attribute.Value.String()) {
			return attribute
		}
	}
	return slog.Attr{}
}

// LogEvent emits only fields accepted by the log sink.
func LogEvent(logger *slog.Logger, level slog.Level, message string, fields map[string]any) {
	safe := SafeFields(LogSink, fields)
	attributes := make([]slog.Attr, 0, len(safe))
	for key, value := range safe {
		attributes = append(attributes, slog.Any(key, value))
	}
	logger.LogAttrs(nil, level, message, attributes...)
}

// PublicError produces the only public internal-server-error response body.
func PublicError(correlationID string) []byte {
	if !isSafeCorrelationID(correlationID) {
		correlationID = "unknown"
	}
	return []byte(fmt.Sprintf("{\"error\":\"internal server error\",\"correlation_id\":\"%s\"}\n", correlationID))
}

func isSafeMessage(message string) bool {
	return message == "request_complete" || message == "startup_failed" || message == "server_error"
}

func isSafeEvent(event string) bool {
	return event == "request_complete" || event == "startup_failed" || event == "server_error"
}

func isSafeMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	default:
		return false
	}
}

func isSafeConfigKey(key string) bool {
	switch key {
	case "APP_ENV", "APP_LISTEN_ADDR", "APP_SESSION_SECRET", "SUPABASE_URL", "SUPABASE_ANON_KEY", "SUPABASE_JWT_ISSUER", "RUST_SERVICE_URL", "RUST_SERVICE_TOKEN":
		return true
	default:
		return false
	}
}

func isSafeCorrelationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
