---
phase: 01-baseline-operations
plan: "04"
subsystem: observability
tags: [go, slog, redaction, configuration, security, testing]
requires:
  - phase: 01-01
    provides: "Validated public Go health process and the baseline environment contract"
provides:
  - "Deny-by-default allow lists for Go logs, traces, public errors, and future session fields"
  - "Key-only configuration diagnostics and generic correlated public errors"
  - "Running-process sentinel audit and redaction troubleshooting runbook"
affects: [baseline-operations, go-application, authentication, rust-service, deployment]
actuals:
  tokens: 7972
  tasks: 2
  commits: 2
tech-stack:
  added: ["Go standard library log/slog"]
  patterns: ["per-sink field allow lists", "safe JSON log attribute replacement", "process-level sensitive-output sentinel audit"]
key-files:
  created: [internal/config/config.go, internal/observability/safe.go, tests/operations/go_output_contract_test.go, docs/operations/redaction.md]
  modified: [cmd/web/main.go, cmd/web/main_test.go]
key-decisions:
  - "Use standard-library slog with both producer-side SafeFields allow lists and ReplaceAttr validation as defense in depth."
  - "Use 32-character hexadecimal correlation IDs as the only dynamic value admitted to generic public error responses."
  - "Mirror the same sanitized structured event stream to stdout and stderr so neither audited process sink is empty."
patterns-established:
  - "Untrusted request data is reduced to a fixed method and route pattern before telemetry is emitted."
  - "Process-level redaction tests must require non-empty output captures before sentinel-absence checks."
requirements-completed: [FOUND-02, FOUND-04]
coverage:
  - id: D1
    description: "Required Go configuration is loaded before binding and invalid configuration exposes only the missing key name."
    requirement: FOUND-02
    verification:
      - kind: unit
        ref: "internal/config/config_test.go#TestLoadReportsMissingKeyWithoutConfigurationValue; cmd/web/main_test.go#TestStartupFailureLogsOnlyTheConfigurationKeyAndCorrelationID"
        status: pass
    human_judgment: false
  - id: D2
    description: "Go output sinks exclude all FOUND-04 sensitive categories while preserving a documented structured event schema."
    requirement: FOUND-04
    verification:
      - kind: unit
        ref: "internal/observability/safe_test.go#TestSensitiveSentinelsNeverReachSinks"
        status: pass
      - kind: integration
        ref: "tests/operations/go_output_contract_test.go#TestGoOutputContract"
        status: pass
    human_judgment: false
duration: 6h 49m
completed: 2026-08-03
status: complete
---

# Phase 01 Plan 04: Safe Go Observability Boundary Summary

**Go now emits only allow-listed structured events, correlates generic errors safely, and proves sensitive request/configuration values stay out of every audited output sink.**

## Performance

- **Duration:** 6h 49m
- **Started:** 2026-08-02T23:12:23+08:00
- **Completed:** 2026-08-03T06:01:33+08:00
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments

- Extracted the Go environment contract into `internal/config`; missing values identify their key only and never reflect supplied configuration.
- Added a central `slog` boundary with per-sink allow lists, JSON attribute validation, safe request telemetry, and generic public errors with correlation IDs.
- Added a child-process sentinel audit that inspects non-empty stdout, stderr, HTTP, trace, and session captures, plus an operator redaction runbook.

## Task Commits

1. **Task 1: Carry one hostile request through Go without leaking into any output sink** — `3c69029` (`feat`)
2. **Task 2: Audit the running Go process with the complete FOUND-04 sentinel set** — `ed3d5b0` (`test`)

## Files Created/Modified

- `internal/config/config.go` and `internal/config/config_test.go` — validated environment loading with key-only failures.
- `internal/observability/safe.go` and `internal/observability/safe_test.go` — output-sink allow lists, protected JSON logging, and sensitive-sentinel coverage.
- `cmd/web/main.go` and `cmd/web/main_test.go` — centralized startup/request/error emission with generic correlated responses.
- `tests/operations/go_output_contract_test.go` — running binary audit with hostile environment and request inputs.
- `docs/operations/redaction.md` — safe schema, prohibited categories, troubleshooting, and exact audit command.

## Decisions Made

- Used Go standard-library `slog` rather than a logging dependency, with `SafeFields` as the primary allow-list and `ReplaceAttr` as a second boundary.
- Restricted public correlation IDs to a validated hexadecimal form so callers cannot place arbitrary text into error responses.
- Emitted the same sanitized events to stdout and stderr so both process output sinks are auditable and non-empty.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing Critical Functionality] Made both process output streams auditable**
- **Found during:** Task 2: Audit the running Go process with the complete FOUND-04 sentinel set
- **Issue:** The web process wrote safe events only to stderr, leaving the captured stdout sink empty and allowing an absence assertion to pass without evidence.
- **Fix:** Added a red/green child-process assertion for non-empty stdout and changed the safe logger to mirror its already-sanitized JSON stream to stdout and stderr.
- **Files modified:** `cmd/web/main.go`, `tests/operations/go_output_contract_test.go`
- **Verification:** `TestGoOutputContract` failed with `stdout capture is empty` before the fix and passed afterward; the full Docker test target passed.
- **Committed in:** `ed3d5b0`

---

**Total deviations:** 1 auto-fixed (1 Rule 2).
**Impact on plan:** The fix makes the process-level audit fail closed for every captured process output sink without adding application behavior beyond safe telemetry.

## Issues Encountered

- The local shell has no Go executable. Verification used the project-pinned `golang:1.26.5-alpine` container; the repository's Docker test stage supplied Git required by the existing configuration-template operations test.

## User Setup Required

None - no external service configuration is required.

## Next Phase Readiness

- Future Go handlers must use `observability.SafeFields`/`LogEvent` and preserve the fixed route and attribute schema.
- The redaction runbook provides the repeatable audit command for later authentication and vault work.

## Self-Check

PASSED

- All eight implementation, test, and runbook artifacts exist on disk.
- Task commits `3c69029` and `ed3d5b0` exist in Git history.
