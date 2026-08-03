# Toolchain baseline

`tools/versions.env` is the single machine-readable contract for the supported
toolchain. Do not replace its exact numeric versions with major-version labels.

| Tool | Supported version | Official source |
| --- | --- | --- |
| Go | 1.26.5 | [go.dev/dl](https://go.dev/dl/) or `golang:1.26.5-alpine` |
| Rust | 1.97.1 | [rustup.rs](https://rustup.rs/) or `rust:1.97.1` |
| Node.js | 20.20.2 | [nodejs.org downloads](https://nodejs.org/en/download) or `node:20.20.2` |
| Supabase CLI | 2.109.1 | [Supabase CLI releases](https://github.com/supabase/cli/releases) |
| Docker Compose | 5.1.2 | [Docker Compose releases](https://github.com/docker/compose/releases) |

## Run the baseline

Prerequisites are Docker Desktop (including Compose) and the Supabase CLI. To
verify all versions without installing Go, Rust, or Node on the host, run:

```sh
./scripts/verify-baseline.sh --versions-only --containerized
```

For the complete local baseline with host tools already installed at the exact
versions, run:

```sh
./scripts/verify-baseline.sh --ci
```

The CI command performs, in order, `cargo test --locked`, `go test ./...`,
`npm ci`, `npm run build`, and `npm test`. `cargo test --locked` and `npm ci`
make lockfile drift a failure; neither command updates a lockfile.

## Supabase CLI probe

Use a task-specific writable project directory and disable optional telemetry
when probing a sandboxed or automated machine:

```sh
supabase_probe_dir=$(mktemp -d)
SUPABASE_WORKDIR="$supabase_probe_dir" SUPABASE_TELEMETRY_DISABLED=1 supabase --version
rm -rf "$supabase_probe_dir"
```

The command prints only the CLI version. Do not print environment variables or
configuration values while diagnosing version drift.

## Refresh a released CLI checksum

Only refresh a checksum when intentionally upgrading the matching version in
`tools/versions.env`.

1. Download the matching Linux AMD64 artifact from the official release page.
2. Calculate its SHA-256 locally with `shasum -a 256 <artifact>`.
3. Replace the corresponding value in `tools/checksums.env`.
4. Run `node --test tests/operations/ci_toolchain.test.js` and the containerized
   version gate before opening a review.

GitHub Actions downloads the exact Supabase archive and Docker Compose plugin,
verifies these values before installation, gates both reported versions, then
calls the shared verifier. Never bypass the checksum or version gate.

## Troubleshooting

If the verifier reports a version mismatch, install the exact version shown in
`tools/versions.env` (or use `--containerized` for Go, Rust, and Node) and rerun
the version-only command. A version failure happens before tests and means the
result is not a valid baseline.

If all version checks pass but a test fails, keep the versions unchanged and
debug the failing test. Confirm that `Cargo.lock` and `package-lock.json` remain
unchanged before retrying; a lockfile change is dependency drift, not a baseline
fix.
