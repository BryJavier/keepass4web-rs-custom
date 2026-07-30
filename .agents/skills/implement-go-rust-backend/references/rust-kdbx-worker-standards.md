# Rust KDBX Worker Standards

## Boundary contract

Keep the decoder as a local executable in the same container as Go. It must have no HTTP server, Supabase client, database access, session management, or application authorization logic.

The worker accepts one bounded import job and produces one normalized result:

- Input: encrypted KDBX path or restricted file descriptor plus password/keyfile through stdin or inherited descriptors
- Output: a schema-versioned manifest on stdout and attachment references under a private staging directory
- Diagnostics: redacted structured messages on stderr
- Status: documented exit codes/error categories that Go maps to safe application errors

Do not pass passwords, keyfile bytes, tokens, or decoded fields through command arguments, environment variables, filenames, or stderr.

## Manifest design

- Include `schema_version` and reject unsupported versions on both sides.
- Preserve stable KeePass UUIDs separately from application UUIDs.
- Represent group hierarchy, order, entries, fields, protection metadata, tags, icons, and attachments deterministically.
- Distinguish absent, empty, protected, and unsupported values.
- Use generated attachment reference names; keep original filenames inside the manifest only.
- Validate unique IDs, parent references, counts, sizes, nesting depth, UTF-8 handling, and total output before Go trusts the result.
- Add a Go/Rust golden contract test for every manifest version.

Stdout is protocol data. Never mix log text into it.

## Secure decoding

- Reuse only the proven KDBX password/keyfile construction, `Database::open`, traversal, and extraction behavior from the existing Rust implementation.
- Remove Actix, old auth/database backends, kernel keyring, session cache, HTTP fetching, and static-file concerns.
- Keep credentials and decoded values in the smallest possible scope. Use secrecy/zeroization types where supported and avoid derived `Debug` on secret-bearing structures.
- Zero input buffers and temporary plaintext buffers where practical, recognizing compiler/runtime limitations.
- Create staging directories/files with restrictive permissions and generated names. Reject path traversal and never materialize an attachment using its original filename.
- Enforce independent limits for input bytes, manifest bytes, attachment count/bytes, entries, groups, fields, hierarchy depth, and decode duration.
- Return stable redacted categories for bad credentials, malformed/unsupported KDBX, limit violation, I/O failure, and internal failure.
- Avoid `unsafe`; if unavoidable, isolate it, document the invariant, and test it directly.

## Rust implementation quality

- Split protocol, decoder, normalization, attachment staging, limits, and error mapping into focused modules.
- Use typed errors internally. Add context at boundaries without erasing actionable causes.
- Keep serialization explicit with `serde`; deny unknown fields when reading versioned control messages where forward compatibility does not require them.
- Avoid panics for user-controlled input. Propagate errors and make cleanup deterministic.
- Pin decoder dependencies and review feature flags, advisories, and license obligations.
- Prefer deterministic output order so fixtures and Go contract tests remain stable.

## Go process orchestration

Go must:

1. Create one private staging directory per job.
2. Start the worker with `exec.CommandContext` or an equivalent cancellable boundary.
3. Send secrets through the agreed private channel and close it promptly.
4. Drain bounded stdout and stderr without pipe deadlock.
5. Enforce deadline and output limits; terminate and reap the child on failure.
6. Decode and validate the complete manifest before persistence.
7. Encrypt/upload staged attachments, then remove staging data on every exit path.

Do not trust a zero exit code without validating the manifest. Do not expose raw worker errors to HTTP clients.

## Test matrix

- Password-only, keyfile-only if supported, and combined credentials
- Wrong password, wrong keyfile, malformed header, truncated/corrupted data, and unsupported version
- Empty vault, nested groups, duplicate-looking names, protected/custom fields, tags, Unicode, custom icons, and attachments
- Oversized input/output, excessive hierarchy, excessive fields/entries, and attachment limits
- Broken stdout, invalid manifest version, partial staging output, worker timeout, crash, and cancellation
- Deterministic repeated decode and Go/Rust manifest contract compatibility

Use generated or sanitized fixtures only. Never commit personal KDBX files, real passwords, or production keyfiles.

Before completion, run formatting, Clippy with warnings denied for project code, unit/fixture/contract tests, and the release build. Confirm logs, stderr, temporary directories, and failure artifacts contain no decoded secret.
