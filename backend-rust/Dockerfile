FROM rust:1.97.1-slim-bookworm AS build

WORKDIR /workspace

RUN apt-get update && apt-get install -y --no-install-recommends pkg-config libssl-dev && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock ./
COPY backend-rust ./backend-rust

# `cargo test` deliberately does NOT run as part of this image build (unlike
# the Go Dockerfile, which does). Investigated and confirmed this isn't a
# fixable test-selection problem: most of keepass.rs's and private_service.rs's
# test suite exercises the real Linux kernel session keyring (KeyRingIdentifier
# ::Session — see backend-rust/src/keepass/key.rs and the seccomp profile at
# backend-rust/seccomp/keyring.json, which exists specifically because keyctl/
# add_key/request_key aren't in Docker's default syscall allowlist). Tried:
#   1. Running the full suite — 6 different tests failed with EPERM-class
#      errors (Unknown(1)), all keyring-related.
#   2. Skipping just the one obviously-keyring-named test (key_roundtrip) — a
#      DIFFERENT test failed on the next run with the identical error signature.
#      The kernel keyring is core to how this service works, not an isolated
#      test concern — dozens of tests touch it transitively through
#      `to_enc`/`from_enc`/`with_vault`, so skip-listing individual test names
#      doesn't scale and would gut most of the meaningful coverage anyway.
# An ephemeral `docker build` RUN step doesn't reliably provide a real session
# keyring context, regardless of thread count or skip lists. Testing this
# crate properly needs a CI environment with the right session/capability
# setup — a Phase 6 concern, not something to force into a generic image
# build. Run `cargo test` locally/in a real dev environment before releasing.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/workspace/target \
    cargo build --release --locked --bin keepass4web-rs && \
    cp /workspace/target/release/keepass4web-rs /out-binary

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Required at runtime, not just build time: main.rs's `Config::from_file`
# defaults to reading "config.yml" from the working directory and fails
# startup entirely (silently — StartupFailure is logged with no detail, exit
# code 0) if it's missing. RUST_PRIVATE_LISTEN and friends only ever *override*
# fields already loaded from this file — they don't substitute for it.
COPY --from=build /out-binary /app/private-service
COPY backend-rust/config.yml /app/config.yml

EXPOSE 9090
USER 65532:65532

CMD ["/app/private-service"]
