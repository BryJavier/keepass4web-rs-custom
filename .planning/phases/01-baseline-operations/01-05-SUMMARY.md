---
phase: 01-baseline-operations
plan: "05"
subsystem: rust-observability
tags: [rust, actix, redaction, security, testing]
requires:
  - phase: 01-01
    provides: baseline runtime operations
  - phase: 01-04
    provides: public Go output redaction contract
provides:
  - Fixed-schema Rust telemetry and generic correlated public errors
  - Process-level redaction contracts for request, auth/session, and KeePass/cache paths
affects: [rust-service, authentication, sessions, keepass, deployment]
tech-stack:
  added: []
  patterns: [constant SafeEvent identities, path-only request telemetry, process-level sentinel audits]
key-files:
  created: [src/observability.rs, tests/redaction_contract.rs]
  modified: [src/main.rs, src/server/server.rs, src/auth.rs, src/session.rs, src/server/route/auth.rs, src/server/route/util.rs, src/server/route/keepass.rs, src/keepass/db_cache.rs]
decisions:
  - "Rust telemetry accepts fixed event identities and safe request fields only; it never accepts arbitrary error or request text."
  - "Generic errors expose only a server-generated correlation ID and constant message."
actuals:
  tokens: 9500
  tasks: 3
  commits: 3
status: complete
---

# Phase 01 Plan 05: Rust Redaction Boundary Summary

**The retained Rust KeePass process now emits fixed safe events, redacts all request-derived diagnostic values, and verifies the real HTTP process never reflects sentinel secrets.**

## Accomplishments

- Replaced Actix's raw request-line logger with fixed method/path/status/duration/correlation telemetry mirrored safely to stdout and stderr.
- Added generic correlated HTTP errors and removed formatted auth, session, KeePass-route, and cache diagnostics.
- Added locked process-level contracts for hostile HTTP requests, login/unlock/session inputs, and KeePass/cache inputs.

## Task Commits

1. `35a73c6` — `feat(01-05): add safe Rust request telemetry`
2. `e401304` — `fix(01-05): redact auth and session diagnostics`
3. `e9c48c8` — `fix(01-05): redact KeePass cache diagnostics`

## Verification

- `cargo test --locked --test redaction_contract hostile_request -- --exact` — passed in `rust:1.97.1`.
- `cargo test --locked --test redaction_contract auth_and_session -- --exact` — passed in `rust:1.97.1`.
- `cargo test --locked --test redaction_contract keepass_and_cache -- --exact && cargo test --locked` — passed in `rust:1.97.1`.

## Deviations from Plan

### Auto-fixed Issues

1. [Rule 1 - Bug] Added the missing Actix `Service` trait import required by the request middleware.
2. [Rule 2 - Missing Critical Functionality] Added source-boundary assertions so inactive legacy log macros cannot silently reappear while telemetry is disabled.

## Known Stubs

None.

## Self-Check

PASSED — task commits and all created artifacts are present.
