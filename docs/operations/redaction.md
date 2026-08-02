# Go redaction and output audit

The Go process excludes sensitive data at the point of emission. Collector-side
filters, retention controls, and access restrictions are defense in depth; they
are not the primary redaction control.

## Allowed output schema

`internal/observability.SafeFields` is the sole allow-list boundary for
structured Go fields. Any key not listed below is dropped before it reaches a
log, trace, error, or future session encoder.

| Sink | Allowed fields |
| --- | --- |
| JSON request log | `method`, `route_path`, `status`, `duration_ms`, `correlation_id` |
| JSON startup/error log | `event`, `config_key`, `correlation_id` |
| Trace attribute set | `method`, `route_path`, `status`, `duration_ms`, `correlation_id` |
| Public error response | Generic `error` text and `correlation_id` only |
| Future browser session | `active_vault_handle` only |

The JSON logger adds standard `time`, `level`, and `msg` fields. The `msg`
value is restricted to a fixed safe event name. Requests record the matched
route pattern (`/healthz` or `/unknown`), never `RequestURI`, raw query text,
or a user-controlled path segment.

## Never emit

Do not put any of the following in logs, traces, errors, responses, sessions,
or browser storage:

- request bodies, form data, URLs with query values, cookies, or arbitrary
  request attributes;
- `Authorization` headers, bearer tokens, Supabase credentials, service
  credentials, or session payloads;
- passwords, key-file bytes, decoded vault content, or protected entry values;
- configuration values. Startup diagnostics may identify a missing
  configuration key name, but never its supplied value.

This is intentionally stricter than downstream filtering. Sensitive fields
must be excluded before a process writes its first byte to an output sink.

## Correlation-ID troubleshooting

1. Obtain the correlation ID shown in a generic server-error response.
2. Search the structured Go logs for that ID only.
3. Use the safe event name, route pattern, status, duration, and permitted
   configuration key (for startup failures) to investigate.
4. Do not request a raw HTTP dump, body replay, authorization header, cookie,
   query value, or session serialization to continue the investigation.

Correlation IDs are 32 lower-case hexadecimal characters. Invalid values are
replaced with `unknown` at the public-error boundary.

## Repeatable audit

Run the process-level sentinel audit from the repository root:

```sh
go test ./tests/operations -run TestGoOutputContract -count=1
```

The audit builds the Go web process, injects unique sentinels through runtime
configuration, headers, query values, cookies, request bodies, and an
unknown-path request, then checks stdout, structured stderr, successful and
failing HTTP bodies, trace fields, and the session-field sanitizer. It fails
when an expected non-empty capture is missing or any sentinel appears.
