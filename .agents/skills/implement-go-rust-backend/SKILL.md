---
name: implement-go-rust-backend
description: Use when implementing, completing, reviewing, or repairing this repository's Go backend, Supabase Auth/Postgres/Storage integration, SQL migrations and RLS, application security, background jobs, or Rust KDBX decoder worker.
---

# Implement Go and Rust Backend

## Core contract

Act as the senior owner of the Go application and Rust decoder boundary. Deliver secure, tested vertical slices that follow `ARCHITECTURE.md`. Continue while safe in-scope work remains; do not stop at scaffolding, a happy path, compilation, or an unverified migration.

## Establish context

1. Read `AGENTS.md` and `ARCHITECTURE.md` completely.
2. Inspect current Go, Rust, SQL, configuration, tests, and dependency versions.
3. Read [references/go-supabase-standards.md](references/go-supabase-standards.md) for Go, Auth, PostgreSQL, Storage, encryption, or job work.
4. Read [references/rust-kdbx-worker-standards.md](references/rust-kdbx-worker-standards.md) for decoder or Go/worker integration work.
5. Convert requested behavior and architecture invariants into an acceptance checklist. Do not invent replacements for approved decisions.

## Implement vertical slices

1. Write a focused test for the next behavior and confirm it fails for the expected reason.
2. Implement the smallest complete handler/service/store/migration or worker/manifest slice.
3. Preserve typed boundaries, context cancellation, bounded resources, idempotency, and safe error mapping.
4. Run focused tests, inject relevant failures, then run regression suites.
5. Repeat until the checklist is complete.

Keep Go responsible for HTTP, sessions, authorization, application encryption, PostgreSQL, Storage, jobs, and worker orchestration. Keep Rust responsible only for KDBX validation, decoding, normalization, and attachment staging.

## Non-negotiable invariants

- Never persist decoded secrets as plaintext or expose them through logs, traces, URLs, errors, fixtures, or temporary filenames.
- Keep Supabase service credentials server-only. Because service-role access bypasses RLS, authorize `owner_id` in Go before every privileged operation.
- Make every schema/policy change through a reviewed migration under `supabase/migrations/`; never repair production state through undocumented Dashboard edits.
- Preserve the approved version-one search design: decrypt and filter in Go. Do not introduce plaintext search columns, blind indexes, or database-side decryption without an architecture change.
- Treat PostgreSQL and Storage writes as non-atomic: use explicit states, idempotency, and compensating cleanup.
- Pass the KDBX password/keyfile through stdin or restricted file descriptors, never arguments or environment variables. Require a versioned worker manifest and redacted errors.
- Enforce upload, output, attachment, entry-count, hierarchy-depth, memory, and execution-time limits.

## Completion gate

Do not claim completion until all requested acceptance items are implemented and:

- Go formatting, vet/static checks, race-enabled tests, integration tests, and builds pass.
- Rust formatting, Clippy, unit/fixture/contract tests, and release build pass.
- Supabase migrations recreate a clean local database; constraints, indexes, RLS, ownership, transactions, and Storage cleanup are tested.
- Wrong passwords, malformed/oversized KDBX, timeout/crash, duplicate request, stale revision, database failure, Storage failure, and restart recovery are verified.
- Inspection confirms no plaintext secrets or service credentials leak into persistent state or observability.

When genuinely blocked, exhaust safe in-scope alternatives and report the exact blocker plus remaining checklist. A deadline, sunk cost, or existing untested handler is not permission to weaken security or redefine “complete.”

## Red flags

- “Plaintext is temporary.”
- “The service key makes ownership checks unnecessary.”
- “The happy-path transaction is enough.”
- “The Dashboard change can become a migration later.”
- “Search needs an unapproved security trade-off.”
- “The Rust process exited successfully, so its output is trustworthy.”

Any red flag means return to the architecture, test the missing invariant, and continue.
