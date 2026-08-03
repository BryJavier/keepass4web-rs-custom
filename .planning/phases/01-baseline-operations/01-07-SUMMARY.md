---
phase: 01-baseline-operations
plan: "07"
subsystem: rust-observability
tags: [rust, actix-web, telemetry, redaction, security, testing]
requires:
  - phase: 01-05
    provides: "Fixed-schema Rust telemetry and process-level redaction contracts"
provides:
  - "Post-routing route telemetry that logs only Actix resource patterns or a fixed unmatched value"
  - "Fail-closed redaction assertions for HTTP responses and both Rust process output sinks"
affects: [rust-service, observability, redaction, authentication, deployment]
actuals:
  tokens: 2454
  tasks: 2
  commits: 2
tech-stack:
  added: []
  patterns: ["post-routing telemetry identity", "fixed unmatched-route fallback", "non-empty-before-absence process capture assertions"]
key-files:
  created: []
  modified: [src/observability.rs, src/server/server.rs, tests/redaction_contract.rs]
key-decisions:
  - "Derive request telemetry from Actix HttpRequest::match_pattern only after service completion, mapping default-service requests to the fixed unmatched value."
  - "Require non-empty HTTP, stdout, and stderr captures before every redaction sentinel-absence assertion."
patterns-established:
  - "Telemetry that might reach retained outputs derives its route identity from code-defined routing metadata rather than raw request values."
  - "Output-redaction tests fail closed when any audited capture is empty."
requirements-completed: [FOUND-04]
coverage:
  - id: D1
    description: "Matched dynamic routes emit their Actix resource pattern while default-service routes emit a fixed unmatched value without path-segment disclosure."
    requirement: FOUND-04
    verification:
      - kind: integration
        ref: "tests/redaction_contract.rs#sensitive_sentinels_never_reach_outputs"
        status: pass
    human_judgment: false
  - id: D2
    description: "Every Rust redaction scenario proves HTTP, stdout, and stderr captures are non-empty before accepting sentinel absence."
    requirement: FOUND-04
    verification:
      - kind: integration
        ref: "docker run --rm --security-opt seccomp=$PWD/seccomp/keyring.json ... rust:1.97.1 cargo test --locked --test redaction_contract"
        status: pass
    human_judgment: false
duration: 26m
completed: 2026-08-03
status: complete
---

# Phase 01 Plan 07: Safe Rust Route Telemetry Summary

**Rust request telemetry now derives only matched Actix resource patterns or a fixed unmatched value, with real-process tests that reject empty evidence before redaction checks.**

## Performance

- **Duration:** 26m
- **Started:** 2026-08-03T03:06:45Z
- **Completed:** 2026-08-03T03:33:02Z
- **Tasks:** 2
- **Files modified:** 3

## Accomplishments

- Replaced pre-routing raw-path forwarding with post-routing `HttpRequest::match_pattern()` telemetry and a fixed `unmatched` fallback.
- Added a path-sentinel regression covering the dynamic icon route and default service across HTTP, stdout, and stderr.
- Centralized redaction evidence checks so all scenarios require non-empty captures before checking sentinel absence.

## Task Commits

1. **Task 1: Emit matched or fixed Rust route identities across the real HTTP boundary** — `379a4da` (`feat`)
2. **Task 2: Make every Rust redaction case fail closed before absence assertions** — `2536470` (`test`)

## Files Created/Modified

- `src/observability.rs` — derives telemetry route identity from Actix routing metadata.
- `src/server/server.rs` — passes the completed response request to telemetry after routing.
- `tests/redaction_contract.rs` — proves matched/default routing redaction and non-empty output evidence.

## Decisions Made

- Used Actix `HttpRequest::match_pattern()` after `service.call` completes because it exposes a code-defined resource pattern and returns `None` for default services.
- Mapped that `None` case to the constant `unmatched`, never to an attacker-controlled raw path.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Updated the prior default-service telemetry assertion**
- **Found during:** Task 2: Make every Rust redaction case fail closed before absence assertions
- **Issue:** `hostile_request` still expected the literal `/missing` request path after Task 1 intentionally replaced raw default-service route output with `unmatched`.
- **Fix:** Updated the assertion to require `path=unmatched`.
- **Files modified:** `tests/redaction_contract.rs`
- **Verification:** Full pinned `redaction_contract` suite passed with all four scenarios.
- **Committed in:** `2536470`

---

**Total deviations:** 1 auto-fixed (1 Rule 1).
**Impact on plan:** The correction aligns the older regression with the new fail-closed route contract; no additional feature scope was added.

## Issues Encountered

- The pinned `rust:1.97.1` image does not include `cargo-fmt`; `git diff --check` passed, and the locked targeted and full Rust test suites passed.
- The full suite reports existing dead-code warnings for `DbBackend::as_any` and `File::filename`; neither was introduced or changed by this plan.

## Known Stubs

None.

## User Setup Required

None - no external service configuration is required.

## Next Phase Readiness

- Rust output telemetry no longer accepts a raw request path at its public boundary.
- Future Rust routes must preserve post-routing resource-pattern telemetry and fail-closed output capture tests.

## Self-Check

PASSED

- All three implementation/test files and this summary exist on disk.
- Task commits `379a4da` and `2536470` exist in Git history.
