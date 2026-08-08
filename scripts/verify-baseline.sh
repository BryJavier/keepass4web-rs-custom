#!/bin/sh
# shellcheck shell=sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

# shellcheck source=../tools/versions.env
. "$repository_root/tools/versions.env"

versions_only=false
containerized=false
ci=false

usage() {
  printf '%s\n' 'Usage: scripts/verify-baseline.sh [--versions-only] [--containerized] [--ci]' >&2
}

fail_version() {
  tool_name=$1
  expected=$2
  actual=$3
  printf '%s version mismatch: expected %s, got %s\n' "$tool_name" "$expected" "$actual" >&2
  exit 1
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '%s is required but was not found on PATH\n' "$1" >&2
    exit 1
  fi
}

check_exact() {
  tool_name=$1
  expected=$2
  actual=$3
  if [ "$actual" != "$expected" ]; then
    fail_version "$tool_name" "$expected" "$actual"
  fi
}

check_host_cli_versions() {
  require_command supabase
  require_command docker

  supabase_actual=$(SUPABASE_TELEMETRY_DISABLED=1 supabase --version)
  compose_actual=$(docker compose version --short)
  check_exact 'Supabase CLI' "$SUPABASE_CLI_VERSION" "$supabase_actual"
  check_exact 'Docker Compose' "$DOCKER_COMPOSE_VERSION" "$compose_actual"
}

check_host_language_versions() {
  require_command go
  require_command rustc
  require_command node

  go_actual=$(go version | awk '{print substr($3, 3)}')
  rust_actual=$(rustc --version | awk '{print $2}')
  node_actual=$(node --version | sed 's/^v//')
  check_exact 'Go' "$GO_VERSION" "$go_actual"
  check_exact 'Rust' "$RUST_VERSION" "$rust_actual"
  check_exact 'Node.js' "$NODE_VERSION" "$node_actual"
}

check_container_versions() {
  require_command docker

  go_actual=$(docker run --rm "golang:$GO_VERSION-alpine" go version | awk '{print substr($3, 3)}')
  rust_actual=$(docker run --rm "rust:$RUST_VERSION" rustc --version | awk '{print $2}')
  node_actual=$(docker run --rm "node:$NODE_VERSION" node --version | sed 's/^v//')
  check_exact 'Go container' "$GO_VERSION" "$go_actual"
  check_exact 'Rust container' "$RUST_VERSION" "$rust_actual"
  check_exact 'Node.js container' "$NODE_VERSION" "$node_actual"
}

run_containerized_baseline() {
  check_container_versions
  check_host_cli_versions

  docker run --rm -v "$repository_root:/workspace" -w /workspace "golang:$GO_VERSION-alpine" sh -ceu '
    apk add --no-cache git >/dev/null
    go test ./...
  '
  docker run --rm --security-opt "seccomp=$repository_root/backend-rust/seccomp/keyring.json" \
    -v "$repository_root:/workspace:ro" -w /workspace \
    -e CARGO_HOME=/tmp/cargo -e CARGO_TARGET_DIR=/tmp/target "rust:$RUST_VERSION" \
    cargo test --locked
  docker run --rm -v "$repository_root:/workspace" -w /workspace "node:$NODE_VERSION" sh -ceu '
    npm ci
    npm run build:css
  '
}

run_ci_baseline() {
  check_host_language_versions
  check_host_cli_versions

  cargo test --locked
  go test ./...
  npm ci
  npm run build:css
  npm test
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --versions-only) versions_only=true ;;
    --containerized) containerized=true ;;
    --ci) ci=true ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
  shift
done

if [ "$ci" = true ] && [ "$containerized" = true ]; then
  printf '%s\n' '--ci and --containerized cannot be combined' >&2
  exit 2
fi

if [ "$containerized" = true ]; then
  if [ "$versions_only" = true ]; then
    check_container_versions
    check_host_cli_versions
  else
    run_containerized_baseline
  fi
elif [ "$ci" = true ]; then
  run_ci_baseline
else
  check_host_language_versions
  check_host_cli_versions
  if [ "$versions_only" != true ]; then
    run_ci_baseline
  fi
fi

printf '%s\n' 'Baseline verification passed.'
