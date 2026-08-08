---
phase: "01"
slug: baseline-operations
status: draft
nyquist_compliant: false
wave_0_complete: false
created: 2026-08-02
---

# Phase 1 — Validation Strategy

> Per-phase validation contract for reproducible baseline, configuration, topology, and sensitive-output feedback during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Cargo/libtest, Go `testing`, Node `node:test`, and existing Playwright |
| **Config files** | `Cargo.toml`, `go.mod`, `package.json`, `playwright.config.js`, `tests/config.test.yml` |
| **Quick run command** | `./scripts/verify-baseline.sh --versions-only --containerized` plus the task-specific command below |
| **Full suite command** | `./scripts/verify-baseline.sh --ci` |
| **Estimated runtime** | Version gate target: under 60 seconds on warm images; full-suite runtime is measured and recorded by the first green 01-02 containerized baseline |

The exact Go, Rust, Node, Supabase CLI, and Docker Compose versions are not guessed in this document. Plan 01-02 captures them from the first green containerized baseline into `tools/versions.env`, after which every command in this contract enforces them.

---

## Sampling Rate

- **After every task commit:** Run that task's exact command from the verification map.
- **After Wave 1:** Run the two 01-01 commands and exercise the documented Compose `GET /healthz` path.
- **After Wave 2:** Run `./scripts/verify-baseline.sh --ci`, the Go output contract, and the CI-tool provisioning test.
- **After Wave 3:** Run `./scripts/verify-baseline.sh --ci`, all topology/configuration operations tests, and the complete Rust redaction contract.
- **Before `$gsd-verify-work`:** The full suite and every map row must be green.
- **Max feedback latency:** Target 60 seconds for targeted commands on warm caches; record the measured full-suite duration during 01-02 rather than inventing a baseline.

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 01-01-01 | 01 | 1 | FOUND-03 | T-01-01, T-01-02 | Go health works while resolved Compose publishes no Rust port | integration/static | `docker build --target test -f cmd/web/Dockerfile . && docker compose config --format json \| node -e 'let s="";process.stdin.on("data",d=>s+=d).on("end",()=>{const c=JSON.parse(s),w=c.services.web,r=c.services["rust-service"];if(!w?.ports?.length\|\|r?.ports?.length\|\|!c.networks.private?.internal\|\|!w.networks?.private\|\|!r.networks?.private\|\|r.networks?.public)process.exit(1)})'` | ❌ W0 | ⬜ pending |
| 01-01-02 | 01 | 1 | FOUND-02 | T-01-01 | Examples contain no real secret; all environment-specific copies are ignored | unit/static | `docker build --target test -f cmd/web/Dockerfile .` | ❌ W0 | ⬜ pending |
| 01-02-01 | 02 | 2 | FOUND-01 | T-01-02-01, T-01-02-02 | Exact versions come from one green container baseline and lockfiles cannot drift | integration | `./scripts/verify-baseline.sh --versions-only --containerized` | ❌ W0 | ⬜ pending |
| 01-02-02 | 02 | 2 | FOUND-01 | T-01-02-01, T-01-SC | CI provisions and checks exact Supabase CLI and Compose versions before the baseline | unit/integration | `node --test tests/operations/ci_toolchain.test.js && ./scripts/verify-baseline.sh --versions-only --containerized` | ❌ W0 | ⬜ pending |
| 01-03-01 | 03 | 3 | FOUND-03 | T-01-03-01 | Every environment resolves with Go-only public exposure and private Rust | integration/static | `go test ./tests/operations -run TestResolvedTopology -count=1` | ❌ W0 | ⬜ pending |
| 01-03-02 | 03 | 3 | FOUND-02, FOUND-03 | T-01-03-02, T-01-03-03 | Pinned images and non-secret Supabase config preserve the tested boundary | integration/static | `go test ./tests/operations -run 'TestResolvedTopology\|TestConfigurationTemplates' -count=1 && docker compose -f docker-compose.yml -f deploy/compose/development.yml config --quiet` | ❌ W0 | ⬜ pending |
| 01-03-03 | 03 | 3 | FOUND-02, FOUND-03 | T-01-03-01, T-01-03-02, T-01-03-05 | Runbooks require managed secrets, TLS ingress, and Go-only exposure without choosing a provider | unit/integration | `go test ./tests/operations -run TestProductionControlContract -count=1` | ❌ W0 | ⬜ pending |
| 01-04-01 | 04 | 2 | FOUND-02, FOUND-04 | T-01-04-01, T-01-04-02 | Hostile Go request/config values cannot enter log, trace, error, or session fields | unit | `go test ./internal/config ./internal/observability ./cmd/web -count=1` | ❌ W0 | ⬜ pending |
| 01-04-02 | 04 | 2 | FOUND-04 | T-01-04-01 | Process-level Go captures are non-empty and contain no sensitive sentinel | integration | `go test ./tests/operations -run TestGoOutputContract -count=1` | ❌ W0 | ⬜ pending |
| 01-05-01 | 05 | 3 | FOUND-04 | T-01-05-01 | Rust request logs/errors exclude headers, query, cookies, and bodies | integration | `cargo test --locked --test redaction_contract hostile_request -- --exact` | ❌ W0 | ⬜ pending |
| 01-05-02 | 05 | 3 | FOUND-04 | T-01-05-02, T-01-05-03 | Rust auth, unlock, session, and cleanup outputs exclude submitted secrets | integration | `cargo test --locked --test redaction_contract auth_and_session -- --exact` | ❌ W0 | ⬜ pending |
| 01-05-03 | 05 | 3 | FOUND-04 | T-01-05-02, T-01-SC | KeePass/cache diagnostics are redacted and the locked Rust baseline remains green | integration/regression | `cargo test --locked --test redaction_contract keepass_and_cache -- --exact && cargo test --locked` | ❌ W0 | ⬜ pending |
| 01-06-01 | 06 | 4 | FOUND-02 | T-01-06-01, T-01-SC | The three Compose runtime paths named by the runbooks are ignored while safe examples remain trackable | unit/static | `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.26.5-alpine sh -ceu 'apk add --no-cache git >/dev/null; go test ./tests/operations -run TestConfigurationTemplates -count=1'` | ❌ W0 | ⬜ pending |
| 01-06-02 | 06 | 4 | FOUND-02 | T-01-06-02, T-01-06-03 | Every documented --env-file occurrence is discovered, constrained to deploy/config, and proven ignored | unit/static | `docker run --rm -v "$PWD:/workspace" -w /workspace golang:1.26.5-alpine sh -ceu 'apk add --no-cache git >/dev/null; go test ./tests/operations -run "TestConfigurationTemplates\|TestDocumentedComposeEnvFilesAreIgnored" -count=1'` | ❌ W0 | ⬜ pending |
| 01-07-01 | 07 | 4 | FOUND-04 | T-01-07-01, T-01-07-02 | Matched dynamic routes emit only their pattern and unmatched requests emit only a fixed route value | integration | `docker run --rm --security-opt "seccomp=$PWD/seccomp/keyring.json" -v "$PWD:/workspace" -w /workspace -e CARGO_HOME=/tmp/cargo -e CARGO_TARGET_DIR=/tmp/target rust:1.97.1 cargo test --locked --test redaction_contract sensitive_sentinels_never_reach_outputs -- --exact` | ❌ W0 | ⬜ pending |
| 01-07-02 | 07 | 4 | FOUND-04 | T-01-07-03 | Every Rust redaction scenario requires non-empty response, stdout, and stderr before sentinel-absence checks | integration/regression | `docker run --rm --security-opt "seccomp=$PWD/seccomp/keyring.json" -v "$PWD:/workspace" -w /workspace -e CARGO_HOME=/tmp/cargo -e CARGO_TARGET_DIR=/tmp/target rust:1.97.1 cargo test --locked --test redaction_contract` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] `cmd/web/main_test.go` — startup/health/config tracer for FOUND-02/03.
- [ ] `tests/operations/config_templates_test.go` — safe-example and ignore-policy contract for FOUND-02.
- [ ] `scripts/verify-baseline.sh` — exact-version and full baseline entry point for FOUND-01.
- [ ] `tests/operations/ci_toolchain.test.js` — CI provisioning/order contract for exact Supabase CLI and Docker Compose versions.
- [ ] `tests/operations/topology_test.go` — resolved Compose policy for all environments and FOUND-03.
- [ ] `tests/operations/production_controls_test.go` — locked provider-neutral production controls and runbook/topology contract for FOUND-02/03.
- [ ] `internal/config/config_test.go` and `internal/observability/safe_test.go` — Go config/output sentinels for FOUND-02/04.
- [ ] `tests/operations/go_output_contract_test.go` — running Go process audit for FOUND-04.
- [ ] `tests/redaction_contract.rs` — running Rust request/auth/session/KeePass/cache audit for FOUND-04.
- [ ] No new test framework or application package is installed; existing Cargo, Go standard library, Node built-ins, and Playwright infrastructure cover the phase.

---

## Manual-Only Verifications

All phase behaviors have automated verification. The runbooks include operator commands, but their mandatory controls are also asserted through configuration/topology tests or the CI baseline.

---

## Locked Production Control Contract

Provider selection is explicitly deferred. Phase 1 remains provider-neutral, but production readiness requires all of the following regardless of vendor:

1. Real production secrets are injected from a managed secret store; ignored environment files are examples/development mechanics, not the production secret system.
2. External traffic terminates TLS at an ingress attached only to the Go service.
3. Go is the only publicly exposed application service; Rust has no public listener, host port, route, or ingress and remains reachable only through the private network.

The selected provider must supply evidence for all three controls before production deployment can pass validation.

---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verification matching a row above.
- [ ] Sampling continuity: no three consecutive tasks lack automated verification.
- [ ] Wave 0 creates every missing test/reference before its behavior is implemented.
- [ ] No watch-mode flags are used.
- [ ] Targeted feedback latency is measured and is under 60 seconds on warm caches, or the slow command is moved to the wave/full gate.
- [ ] Full-suite duration from the first green containerized baseline is recorded in the 01-02 summary.
- [ ] `nyquist_compliant: true` is set after all rows are green.

**Approval:** pending
