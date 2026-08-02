---
phase: 01-baseline-operations
plan: "01"
subsystem: infra
tags: [go, docker-compose, configuration, security, testing]
requires: []
provides:
  - "Validated public Go health service with a private-only Rust Compose peer"
  - "Safe runtime configuration templates and ignore-policy enforcement"
affects: [baseline-operations, auth, rust-service, deployment]
actuals:
  tokens: 3060
  tasks: 2
  commits: 2
tech-stack:
  added: ["Go 1.26.5 standard library", "Docker Compose"]
  patterns: ["pre-bind configuration validation", "Go-public/Rust-private Compose networks", "non-secret configuration templates"]
key-files:
  created: [go.mod, cmd/web/main.go, cmd/web/Dockerfile, tests/operations/config_templates_test.go]
  modified: [docker-compose.yml, .gitignore]
key-decisions:
  - "Pinned the verified official Go builder to golang:1.26.5-alpine because golang:stable is not a published official tag."
  - "Kept Rust internal-only with expose and an internal private network; only Go owns a host port."
  - "Use explicit development, staging, and production ignore rules while retaining trackable *.env.example templates."
patterns-established:
  - "Validate every required runtime key before a public listener is constructed, returning key names only."
  - "Run configuration-policy tests in the Docker test stage with git check-ignore --no-index."
requirements-completed: [FOUND-02, FOUND-03]
coverage:
  - id: D1
    description: "Go health endpoint is publicly published while Rust has only private-network reachability."
    requirement: FOUND-03
    verification:
      - kind: unit
        ref: "cmd/web/main_test.go#TestRunServesHealthzForValidConfiguration"
        status: pass
      - kind: integration
        ref: "docker build --target test -f cmd/web/Dockerfile . && docker compose config --format json"
        status: pass
    human_judgment: false
  - id: D2
    description: "Go, Rust, and Supabase templates are non-secret and all environment-specific copies are ignored."
    requirement: FOUND-02
    verification:
      - kind: unit
        ref: "tests/operations/config_templates_test.go#TestConfigurationTemplates"
        status: pass
    human_judgment: false
duration: 47min
completed: 2026-08-02
status: complete
---

# Phase 01 Plan 01: Public/Private Operations Tracer Summary

**Validated Go health endpoint, private-only Rust Compose topology, and operator-safe configuration templates pinned to Go 1.26.5.**

## Performance

- **Duration:** 47 min
- **Started:** 2026-08-02T13:55:53Z
- **Completed:** 2026-08-02T14:42:42Z
- **Tasks:** 2
- **Files modified:** 10

## Accomplishments

- Added a standard-library Go web process that validates all required configuration before binding and serves only `GET /healthz`.
- Replaced the public Rust Compose port with a two-network topology: Go is published, while Rust is exposed only to the internal private network.
- Added safe Go, Rust, and Supabase environment templates plus an automated contract proving runtime copies are ignored and examples remain trackable.

## Task Commits

1. **Task 1: Run the validated Go health path across the public/private Compose boundary** — `b39b473` (`feat`)
2. **Task 2: Make real configuration uncommittable while preserving operator-ready examples** — `ff8441c` (`feat`)

## Files Created/Modified

- `cmd/web/main.go` — validated startup path and constant local health handler.
- `cmd/web/main_test.go` — test-first health and pre-bind configuration checks.
- `cmd/web/Dockerfile` — pinned Go test/build stages and non-root runtime image.
- `docker-compose.yml` — sole Go host mapping with public/private network boundary.
- `deploy/config/*.env.example` — non-secret service-specific configuration examples.
- `tests/operations/config_templates_test.go` — template safety and Git ignore-policy contract.

## Decisions Made

- Used the official `golang:1.26.5-alpine` image after confirming the plan's literal `golang:stable` tag is unpublished; the numeric patch is pinned in both `go.mod` and the Dockerfile.
- Retained the existing Rust application image but renamed its Compose service to `rust-service`, removed host publication, and restricted it to the internal network.
- Added `WEB_HOST_PORT` as an optional host override for local conflicts while keeping Compose's default external port at 8080.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Replaced the unpublished `golang:stable` image tag**
- **Found during:** Task 1
- **Issue:** Docker Hub's official Go image has no `stable` tag, so the plan's literal version probe could not start.
- **Fix:** Verified official tags and used the current stable numeric release `golang:1.26.5-alpine` in the module and multistage Dockerfile.
- **Verification:** Official image container and all Docker test builds completed with Go 1.26.5.
- **Committed in:** `b39b473`

**2. [Rule 3 - Blocking] Added an optional local host-port override**
- **Found during:** Task 1
- **Issue:** An unrelated local process occupied default host port 8080, blocking the required real Compose health-path exercise.
- **Fix:** Kept 8080 as the Compose default and added `WEB_HOST_PORT` for a temporary local override; the tracer was exercised at port 18080.
- **Verification:** Compose returned `ok` from `http://127.0.0.1:18080/healthz`; `docker compose ps` showed no Rust host mapping.
- **Committed in:** `b39b473`

**3. [Rule 3 - Blocking] Added Git to the Docker test stage**
- **Found during:** Task 2
- **Issue:** The pinned minimal Go image lacks Git, preventing the required `git check-ignore` test from running in `docker build --target test`.
- **Fix:** Installed Git only in the test stage and used `git check-ignore --no-index` so the policy runs without Docker build context Git metadata.
- **Verification:** The Docker test stage runs all Go and operations tests successfully.
- **Committed in:** `ff8441c`

**4. [Rule 1 - Bug] Narrowed the secret-shaped value heuristic**
- **Found during:** Task 2
- **Issue:** The test misclassified the safe Supabase JWT issuer URL as a token because it contains two dots.
- **Fix:** Excluded URLs from the JWT-like token check while retaining blank, `eyJ`, `sk_`, and service-secret rejection.
- **Verification:** Safe templates pass; unsafe blank and JWT-shaped fixtures fail.
- **Committed in:** `ff8441c`

**Total deviations:** 4 auto-fixed (1 Rule 1, 3 Rule 3).

## Issues Encountered

- Docker's default 8080 port was already in use. The development-only `WEB_HOST_PORT=18080` override allowed the required real Compose smoke test without disrupting the unrelated process.

## User Setup Required

None — operators copy a committed example to an ignored environment-specific sibling and inject real production values from a managed secret store.

## Next Phase Readiness

- The public-Go/private-Rust boundary and safe configuration contract are ready for the remaining baseline plans.
- Local users with port 8080 occupied can set `WEB_HOST_PORT` for the health tracer; deployments should keep the default external port or place ingress before Go only.

## Self-Check: PASSED

- All 10 implementation/configuration/test artifacts and this summary exist on disk.
- Task commits `b39b473` and `ff8441c` exist in Git history.
