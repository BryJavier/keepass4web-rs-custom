# Go and Supabase Backend Standards

## Contents

- Go service design
- Authentication and sessions
- PostgreSQL, migrations, and RLS
- Supabase Storage
- Application encryption
- Jobs, failures, observability, and verification
- Official references

## Go service design

- Keep handlers limited to request parsing, authentication, validation, service invocation, and response rendering.
- Put authorization and business rules in application services; put SQL and Storage mechanics behind narrow repositories.
- Accept `context.Context` as the first parameter for blocking work. Propagate request/job cancellation and configure explicit timeouts.
- Use bounded request bodies, streaming uploads, generated temporary paths, and guaranteed deferred cleanup.
- Prefer concrete types internally and interfaces at the consuming boundary. Avoid global clients and hidden mutable state.
- Wrap errors with operation context while preserving typed/sentinel causes used for HTTP mapping.
- Map expected failures to stable categories; return correlation IDs for unexpected failures without leaking internals.
- Close rows, bodies, files, pipes, and transactions on every path. Do not start goroutines without ownership, cancellation, and a wait strategy.
- Make retryable operations idempotent with stable keys and uniqueness constraints. Retry only operations known to be safe.

## Authentication and sessions

- Use Supabase email/password authentication for the administratively provisioned allow-listed account. Keep public signup disabled.
- Verify issuer, audience, signature, expiry, and subject for tokens according to the installed Supabase integration.
- Derive identity from verified authentication state, never request fields.
- Issue a random opaque application session cookie. Store only its hash plus encrypted refresh material in `app_sessions`.
- Apply `HttpOnly`, `Secure`, suitable `SameSite`, expiry, rotation, revocation, CSRF, and login rate limiting.
- Never send the service-role key, database password, refresh token, or application encryption key to the browser.

## PostgreSQL and migrations

- Create every schema, constraint, index, function, grant, policy, and seed change in versioned `supabase/migrations/`.
- Generate a new migration for a new change. Do not edit a migration already applied to a shared environment.
- Recreate locally with `supabase db reset`; review generated diffs before committing. Never use `db reset --linked` outside an explicitly disposable environment.
- Use parameterized SQL and explicit column lists. Apply query/transaction timeouts and inspect affected row counts.
- Model invariants in PostgreSQL: primary/foreign keys, `not null`, uniqueness, checks, deliberate delete behavior, and indexes supporting ownership and lookup paths.
- Keep multi-record changes in one transaction. Use revision predicates or row locks where concurrent updates matter.
- Insert `change_events` in the same transaction as entry changes.
- Keep import/job status transitions valid and monotonic. Startup reconciliation must find interrupted work.

### RLS defense in depth

- Enable RLS on exposed tables and scope policies to `authenticated`.
- Use both `using` and `with check` for ownership-changing operations.
- Index columns referenced by policies, especially `owner_id`.
- Treat service-role and bypass-RLS database access as privileged. RLS does not replace Go authorization on those paths.
- Avoid security-definer functions and views unless their grants, search path, caller, and RLS behavior are explicitly tested.

Representative ownership policy:

```sql
alter table public.vaults enable row level security;

create policy "owners manage vaults"
on public.vaults
for all
to authenticated
using ((select auth.uid()) = owner_id)
with check ((select auth.uid()) = owner_id);

create index vaults_owner_id_idx on public.vaults (owner_id);
```

Adapt names to the actual migration; do not paste a policy without tests.

## Supabase Storage

- Keep KDBX and attachment buckets private. Use separate buckets when retention or policy differs.
- Generate opaque object keys from trusted IDs; never use user filenames as paths.
- Store original KDBX bytes unchanged. Encrypt attachment bytes in Go before upload and encrypt filename metadata in PostgreSQL.
- Proxy downloads through authorized Go handlers unless `ARCHITECTURE.md` is changed to permit signed URLs.
- Remember that service keys bypass Storage RLS and may create objects without normal user ownership metadata.
- Restrict bucket MIME types and sizes, but repeat validation in Go.

PostgreSQL and Storage cannot commit atomically. Use:

1. A durable `pending`/`importing` database state.
2. Idempotent object keys and checksums.
3. Final database activation only after required objects exist.
4. Compensating object deletion on failure.
5. Persisted cleanup jobs and startup reconciliation for interrupted work.

## Application encryption

- Use versioned AES-256-GCM envelopes with a unique random nonce per encryption.
- Bind owner, vault, record type, and record ID as associated authenticated data.
- Keep write/read key versions in deployment secrets, not Supabase.
- Add a new write version before re-encryption; retain old read keys until migration completes.
- Use chunked authenticated encryption for attachments so size is bounded in memory. Give every chunk a unique nonce and authenticate its sequence and envelope metadata.
- Zero or shorten the lifetime of plaintext buffers where practical. Never log ciphertext envelopes as a debugging substitute.
- Keep version-one search server-side: load the selected vault, decrypt authorized payloads, apply input/result bounds, and filter in Go.

## Jobs, failures, and observability

- Persist a job before execution. Lease/claim it atomically and make retry transitions explicit.
- Use structured logs with correlation IDs and stable redacted categories.
- Do not log request bodies, auth headers, cookies, decrypted fields, keyfiles, worker stdout, or attachment names.
- Test cancellation, timeout, panic recovery, duplicate delivery, partial Storage writes, database rollback, cleanup retry, and restart reconciliation.

## Verification

- `gofmt` all changed Go files.
- Run focused tests first, then `go test -race ./...`, `go vet ./...`, and configured linters/build commands.
- Recreate Supabase locally from migrations and run repository/RLS integration tests.
- Inspect database rows, Storage objects, logs, and temp directories for secret leakage.
- Benchmark or profile only observed performance risks; do not weaken encryption for speculative speed.

## Official references

- [Supabase password authentication](https://supabase.com/docs/guides/auth/passwords)
- [Supabase Row Level Security](https://supabase.com/docs/guides/database/postgres/row-level-security)
- [Supabase Storage access control](https://supabase.com/docs/guides/storage/security/access-control)
- [Supabase private buckets](https://supabase.com/docs/guides/storage/buckets/fundamentals)
- [Supabase database migrations](https://supabase.com/docs/guides/local-development/database-migrations)
