---
phase: 01-baseline-operations
verified: 2026-08-03T04:00:52Z
status: human_needed
score: 9/10 must-haves verified
behavior_unverified: 1
overrides_applied: 0
re_verification:
  previous_status: gaps_found
  previous_score: 6/10
  gaps_closed:
    - "An operator can configure development, staging, and production from ignored templates without a real secret entering source control."
    - "A developer can verify that sensitive request and vault data are absent from logs, traces, errors, and sessions."
  gaps_remaining: []
  regressions: []
behavior_unverified_items:
  - truth: "Existing KeePass decoding, encrypted cache, key lifecycle, and browser behavior continue to pass their captured tests."
    test: "Run the complete supported baseline: ./scripts/verify-baseline.sh --ci on a host with the exact pinned Go, Rust, Node, Supabase CLI, and Compose tools."
    expected: "Locked Cargo, Go, frontend build, and Playwright tests all pass without changing Cargo.lock or package-lock.json."
    why_human: "This verifier executed the version gate and targeted Go/Rust contracts, but the host lacks the exact Go/Rust toolchains and the full browser baseline was not run."
human_verification:
  - test: "Run the complete supported baseline: ./scripts/verify-baseline.sh --ci on a host with the exact pinned Go, Rust, Node, Supabase CLI, and Compose tools."
    expected: "Locked Cargo, Go, frontend build, and Playwright tests all pass without changing Cargo.lock or package-lock.json."
    why_human: "The full browser-enabled baseline is runtime behavior that this verifier could not exercise in the available toolchain environment."
---

# Phase 1: Baseline & Operations Verification Report

**Phase Goal:** Developers and operators can build and run KeePass4Web safely without exposing secrets or public Rust access.
**Verified:** 2026-08-03T04:00:52Z
**Status:** human_needed
**Re-verification:** Yes — after closure plans 01-06 and 01-07

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
| --- | --- | --- | --- |
| 1 | A developer can run the captured baseline tests using supported tool versions. | ✓ VERIFIED | `tools/versions.env` pins Go 1.26.5, Rust 1.97.1, Node 20.20.2, Supabase CLI 2.109.1, and Compose 5.1.2. `./scripts/verify-baseline.sh --versions-only --containerized` exited 0 and the script invokes locked Rust, Go, frontend, and browser baselines on its supported `--ci` path. |
| 2 | CI uses the same exact toolchain and Cargo resolution cannot mutate the lockfile. | ✓ VERIFIED | The workflow sources `tools/versions.env`, provisions/checks the pinned tools before calling `scripts/verify-baseline.sh --ci`, and every Cargo baseline path uses `cargo test --locked`. This re-verification found no tracked lockfile changes. |
| 3 | Operators can configure development, staging, and production from ignored templates without a real secret entering source control. | ✓ VERIFIED | `.gitignore` explicitly ignores all three documented `deploy/config/{development,staging,production}.compose.env` files. The focused pinned-Go contract passed and derives every `--env-file` occurrence from both operations runbooks, rejects unsafe path drift, checks each path with `git check-ignore --no-index`, and preserves trackable `*.env.example` templates. |
| 4 | Invalid Go configuration stops before binding and reports only a missing key. | ✓ VERIFIED | Quick regression inspection confirms `internal/config.Load` validates the required key contract before `runWithLogger` calls the supplied server function; diagnostics pass only `config.MissingKey(err)` through the centralized safe logger. This unchanged path was covered by the prior focused Go contract. |
| 5 | Go is the only public service and Rust accepts traffic only on its private network. | ✓ VERIFIED | Fresh resolution of development, staging, and production Compose JSON models passed: exactly `web` has one published port, `rust-service` has none and belongs only to `private`, and `private.internal` is true. |
| 6 | Supabase config is commit-safe and production remains provider-neutral with managed secrets, TLS ingress, and Go-only exposure. | ✓ VERIFIED | `supabase/config.toml` has no runtime credentials. `docs/operations/configuration.md` and `topology.md`, enforced by `TestProductionControlContract`, require managed-secret injection, TLS-terminating Go-only ingress, private Rust, provider deferral, validation, and rollback. |
| 7 | Sensitive request and vault data are absent from logs, traces, errors, and sessions. | ✓ VERIFIED | The Go running-process output contract passed. The complete pinned Rust `redaction_contract` test binary exited 0, covering hostile request, auth/session, KeePass/cache, and dynamic/default-route output captures. |
| 8 | Go output uses a centralized allow-list and generic correlated errors. | ✓ VERIFIED | `internal/observability/safe.go` drops unapproved fields for every output sink and validates JSON logger attributes. The pinned `TestGoOutputContract` exited 0 after injecting hostile environment, header, query, cookie, body, trace, and session sentinels. |
| 9 | Rust redaction tests fail closed for every captured sensitive-output case. | ✓ VERIFIED | `tests/redaction_contract.rs` routes every scenario through `assert_redaction_scenario`, which first requires non-empty HTTP response, stdout, and stderr. The complete pinned Rust contract exited 0. |
| 10 | Existing KeePass decoding, encrypted cache, key lifecycle, and browser behavior remain covered by the supported full baseline. | ⚠️ PRESENT_BEHAVIOR_UNVERIFIED | The exact version gate passed and targeted Rust/Go contracts pass, but this verifier did not execute the supported `--ci` baseline including Playwright. |

**Score:** 9/10 truths verified (1 present, behavior-unverified)

### Required Artifacts

| Artifact | Expected | Status | Details |
| --- | --- | --- | --- |
| `tools/versions.env`, `scripts/verify-baseline.sh`, CI workflow | Exact reproducible baseline | ✓ VERIFIED | Numeric single-source version contract is sourced by both local verifier and CI; containerized version gate passed. |
| `.gitignore`, `tests/operations/config_templates_test.go`, operations runbooks | Safe ignored runtime configuration | ✓ VERIFIED | Exact root-anchored Compose paths are ignored; tests parse both runbooks rather than a hard-coded subset; examples remain trackable. |
| `docker-compose.yml`, overlays, `tests/operations/topology_test.go` | Go-public/Rust-private topology | ✓ VERIFIED | Three fresh resolved Compose models meet the semantic published-port/network policy. |
| `internal/observability/safe.go`, Go output contract | Go sensitive-output boundary | ✓ VERIFIED | Deny-by-default sink filtering, generic correlated error boundary, running-process captures, and hostile-sentinel test are wired. |
| `src/observability.rs`, `src/server/server.rs`, `tests/redaction_contract.rs` | Rust safe request/output boundary | ✓ VERIFIED | Middleware calls `emit_request(response.request(), ...)` after routing; emitter derives `match_pattern()` or fixed `unmatched`; all real-process redaction scenarios passed. |

### Key Link Verification

| From | To | Via | Status | Details |
| --- | --- | --- | --- | --- |
| `scripts/verify-baseline.sh` | `tools/versions.env` | sources exact versions before execution | ✓ WIRED | Direct source link; pinned containerized version gate passed. |
| `.github/workflows/test.yml` | `scripts/verify-baseline.sh` | shared CI baseline | ✓ WIRED | Workflow sources the version contract, checks tools, and invokes `--ci`. |
| `tests/operations/topology_test.go` | Compose base and overlays | resolves JSON policy for all environments | ✓ WIRED | Test constructs all three overlays; independent Compose/Node resolution checks passed. |
| `.gitignore` | documented `*.compose.env` paths | runtime-file protection | ✓ WIRED | All three exact runbook paths matched explicit ignore rules, and the runbook-derived focused test passed. |
| `src/server/server.rs` | `src/observability.rs` | post-routing request completion telemetry | ✓ WIRED | `response.request()` is passed after `service.call`; `emit_request` derives only Actix matched routing metadata or fixed `unmatched`. |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
| --- | --- | --- | --- | --- |
| Compose topology policy | resolved service/network model | `docker compose ... config --format json` for each overlay | Yes | ✓ FLOWING |
| Go output audit | child-process stdout/stderr, HTTP, trace/session captures | real Go process plus sink sanitizer | Yes | ✓ FLOWING |
| Rust request telemetry | `route` | post-routing `response.request()` → `HttpRequest::match_pattern()` → stdout/stderr | Yes; code-defined pattern or fixed fallback only | ✓ FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
| --- | --- | --- | --- |
| Pinned baseline version gate | `./scripts/verify-baseline.sh --versions-only --containerized` | exit 0: `Baseline verification passed.` | ✓ PASS |
| Documented Compose env-file safety | pinned Go: `go test ./tests/operations -run "TestConfigurationTemplates\|TestDocumentedComposeEnvFilesAreIgnored" -count=1` | exit 0 | ✓ PASS |
| Go hostile-output contract | pinned Go: `go test ./tests/operations -run TestGoOutputContract -count=1` | exit 0 | ✓ PASS |
| Resolved deployment topology | three `docker compose ... config --format json` policy checks | development, staging, production each passed | ✓ PASS |
| Rust dynamic/default route redaction | pinned Rust: `cargo test --locked --test redaction_contract sensitive_sentinels_never_reach_outputs -- --exact` | exit 0 | ✓ PASS |
| Complete Rust redaction contract | pinned Rust: `cargo test --locked --test redaction_contract` | exit 0 | ✓ PASS |

### Probe Execution

Step 7c: SKIPPED — Phase 1 declares no conventional `scripts/*/tests/probe-*.sh` probe.

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
| --- | --- | --- | --- | --- |
| FOUND-01 | 01-02 | Captured baseline tests use supported Go, Rust, and Supabase tool versions. | ✓ SATISFIED | Numeric tool contract and shared baseline/CI wiring exist; exact containerized version gate passed. |
| FOUND-02 | 01-01, 01-03, 01-04, 01-06 | Ignored templates configure all environments without committing real secrets. | ✓ SATISFIED | All service and Compose runtime conventions are ignored; every documented Compose `--env-file` is dynamically tested. |
| FOUND-03 | 01-01, 01-03 | Go is the only public service and Rust is private-only. | ✓ SATISFIED | All three resolved overlays passed Go-only/public and Rust-private policy checks. |
| FOUND-04 | 01-04, 01-05, 01-07 | Sensitive request/vault data are absent from logs, traces, errors, and sessions. | ✓ SATISFIED | Go output audit and complete pinned Rust redaction contract passed; Rust raw-path logging is eliminated by the post-routing pattern/fallback boundary. |

No orphaned Phase 1 requirements were found. No deferred items apply: the two original Phase 1 gaps are implemented in closure plans 01-06 and 01-07.

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
| --- | --- | --- | --- | --- |
| `src/auth.rs` | 112 | Existing TODO on session-backend error classification | ⚠️ Warning | Not an output disclosure path; no `TBD`, `FIXME`, or `XXX` marker was found in Phase 1 implementation files. |
| `src/server/route/auth.rs` | 63, 332 | Existing TODOs in authentication route | ⚠️ Warning | Existing cleanup/error distinctions; redaction tests prohibit legacy formatted diagnostics. |
| `src/server/route/util.rs`, `src/server/route/keepass.rs`, `src/keepass/db_cache.rs` | 151; 191, 205; 54 | Existing TODOs | ⚠️ Warning | Outside the closed raw-path/configuration gaps; no evidence they emit sensitive values. |
| `01-VALIDATION.md` | frontmatter and task map | Still marked `draft`/`nyquist_compliant: false` with pending rows | ⚠️ Warning | Administrative validation sign-off is stale despite the closure plans; it does not contradict the code/test evidence above. |

### Human Verification Required

### 1. Complete supported baseline

**Test:** Run `./scripts/verify-baseline.sh --ci` on a host with Go 1.26.5, Rust 1.97.1, Node 20.20.2, Supabase CLI 2.109.1, and Docker Compose 5.1.2.

**Expected:** Locked Cargo, Go, frontend build, and Playwright tests pass without modifying either lockfile.

**Why human:** The local environment lacks the exact Go/Rust toolchains; the full browser-enabled `--ci` path was not exercised.

### Gaps Summary

Both prior blockers are closed. The exact three Compose runtime paths used by both runbooks are now explicitly ignored and mechanically checked, while the Rust request boundary now emits only post-routing resource patterns or the fixed `unmatched` route. Pinned Go and complete Rust output-redaction contracts passed.

The phase cannot receive an automated `passed` verdict yet because the pre-existing full browser baseline claim remains unexercised in this verifier environment. This is an escalation gate for the developer to run the one supported full baseline command; it is not an implementation gap.

---

_Verified: 2026-08-03T04:00:52Z_
_Verifier: the agent (gsd-verifier)_
