---
phase: 01-baseline-operations
plan: "02"
subsystem: infra
tags: [toolchain, ci, docker, rust, go, node, supabase, testing]
requires:
  - phase: 01-01
    provides: "Pinned Go module baseline and public/private operations tracer"
provides:
  - "Exact numeric Go, Rust, Node, Supabase CLI, and Docker Compose version contract"
  - "One developer/CI baseline verifier with locked Cargo resolution"
  - "Checksum-verified CI provisioning for Supabase CLI and Docker Compose"
affects: [baseline-operations, ci, deployment, go, rust-service, frontend]
actuals:
  tokens: 4448
  tasks: 2
  commits: 3
tech-stack:
  added: ["Rust 1.97.1 toolchain pin", "Node 20.20.2 pin", "GitHub Actions exact-version gates"]
  patterns: ["machine-readable tool contract", "shared CI/developer verifier", "checksum before executable installation"]
key-files:
  created: [tools/versions.env, tools/checksums.env, scripts/verify-baseline.sh, rust-toolchain.toml, .node-version, docs/operations/toolchain.md, tests/operations/ci_toolchain.test.js]
  modified: [.github/workflows/test.yml]
key-decisions:
  - "Capture exact numeric versions only after successful official-image probes, then enforce the same contract locally and in CI."
  - "Install Supabase CLI and Docker Compose from official versioned releases and validate repository-recorded SHA-256 values before use."
  - "Use the repository keyring seccomp profile for containerized Rust tests so the existing encrypted-cache contract remains exercised."
patterns-established:
  - "Source tools/versions.env before any version-sensitive baseline work."
  - "Fail version gates before package managers or test commands can produce a misleading result."
requirements-completed: [FOUND-01]
coverage:
  - id: D1
    description: "Exact supported tool versions and one container-capable baseline gate protect developer and CI reproducibility."
    requirement: FOUND-01
    verification:
      - kind: integration
        ref: "scripts/verify-baseline.sh --versions-only --containerized"
        status: pass
      - kind: integration
        ref: "docker run --rm --security-opt seccomp=seccomp/keyring.json rust:1.97.1 cargo test --locked (6/6 pass; user verified)"
        status: pass
    human_judgment: false
  - id: D2
    description: "CI downloads checksum-verified Supabase CLI and Docker Compose releases, gates both versions, and invokes the shared verifier."
    requirement: FOUND-01
    verification:
      - kind: unit
        ref: "tests/operations/ci_toolchain.test.js#CI installs and checks the exact signed baseline before invoking it"
        status: pass
    human_judgment: false
duration: 9h 40m
completed: 2026-08-03
status: complete
---

# Phase 01 Plan 02: Reproducible Toolchain Baseline Summary

**Exact Go, Rust, Node, Supabase CLI, and Compose versions now gate one locked developer and CI baseline.**

## Performance

- **Duration:** 9h 40m
- **Started:** 2026-08-02T14:57:19Z
- **Completed:** 2026-08-03T00:37:56Z
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments

- Captured the first green numeric toolchain contract: Go 1.26.5, Rust 1.97.1, Node 20.20.2, Supabase CLI 2.109.1, and Docker Compose 5.1.2.
- Added a POSIX verifier that fails exact-version checks before running `cargo test --locked`, Go, frontend build, or Playwright baselines.
- Reworked CI to resolve the shared contract, provision checksum-verified Supabase and Compose binaries, gate their reported versions, and call the shared verifier while retaining Playwright artifact upload.

## Task Commits

1. **Task 1: Capture one green exact-version baseline behind a single command** — `dcb30c4` (`feat`)
2. **Task 2: Make CI and the developer runbook execute the identical baseline contract** — `bb60ab9` (`feat`)
3. **Task 1 follow-up: enable the documented Rust keyring profile** — `1460f7d` (`fix`)

## Files Created/Modified

- `tools/versions.env` — canonical numeric versions for all five supported tools.
- `scripts/verify-baseline.sh` — local, containerized, version-only, and CI baseline entry point using locked Cargo resolution.
- `rust-toolchain.toml` and `.node-version` — exact repository toolchain declarations aligned with the contract.
- `tools/checksums.env` — SHA-256 values for official Supabase CLI and Compose release assets.
- `.github/workflows/test.yml` — exact setup, verified release provisioning, version gates, shared baseline invocation, and Playwright artifacts.
- `tests/operations/ci_toolchain.test.js` — dependency-free workflow ordering and security contract test.
- `docs/operations/toolchain.md` — installation sources, refresh procedure, commands, prerequisites, and drift recovery runbook.

## Decisions Made

- Used numeric versions captured from successful probes instead of floating Go, Rust, or Node selectors.
- Treated lockfiles as immutable baseline inputs: Cargo runs with `--locked`, while frontend installation uses `npm ci`.
- Used the repository's `seccomp/keyring.json` profile for Rust containers because the existing encrypted-key tests require Linux keyring syscalls.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Replaced unavailable Rust `stable` tag with captured numeric pin**
- **Found during:** Task 1
- **Issue:** Docker Hub does not publish the plan's literal `rust:stable` tag.
- **Fix:** Captured Rust 1.97.1 from the official current Rust image, then pinned and verified `rust:1.97.1` everywhere.
- **Files modified:** `rust-toolchain.toml`, `tools/versions.env`, `scripts/verify-baseline.sh`
- **Verification:** Version-only container gate passes; user verified all six locked Rust tests pass with this compiler.
- **Committed in:** `dcb30c4`

**2. [Rule 3 - Blocking] Enabled the documented Linux keyring seccomp profile for Rust baseline tests**
- **Found during:** Task 1
- **Issue:** The existing key round-trip test fails under Docker's default seccomp policy because keyring syscalls are blocked.
- **Fix:** Applied the repository's existing `seccomp/keyring.json` profile to the containerized Rust verifier.
- **Files modified:** `scripts/verify-baseline.sh`
- **Verification:** Keyring tests pass with the profile; the user-confirmed full locked suite passed 6/6 in 34.45 seconds.
- **Committed in:** `1460f7d`

**Total deviations:** 2 auto-fixed Rule 3 blocking issues.

## Issues Encountered

- The environment's foreground command window could not hold the full Rust database round-trip test. The user ran the exact documented command without that cap and confirmed all six tests passed in 34.45 seconds.
- The exact Node 20.20.2 build reported existing dependency audit warnings. No dependencies were changed because this plan requires immutable lockfiles.

## User Setup Required

None — developers need Docker Desktop/Compose and the Supabase CLI for the containerized version gate; the runbook documents the exact commands.

## Next Phase Readiness

- Later phases can depend on a deterministic Go/Rust/frontend baseline and CI receives the same tool contract as local developers.
- Any intentional compiler or CLI upgrade must update `tools/versions.env`, the aligned repository declarations, checksum values, and the green baseline together.

## Self-Check: PASSED

- All eight implementation, workflow, documentation, and test artifacts exist on disk.
- Task commits `dcb30c4`, `bb60ab9`, and `1460f7d` exist in Git history.
