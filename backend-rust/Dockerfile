FROM rust:1.97.1-slim-bookworm AS build

WORKDIR /workspace

RUN apt-get update && apt-get install -y --no-install-recommends pkg-config libssl-dev && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock ./
COPY backend-rust ./backend-rust
# Test fixture referenced by keepass::keepass::tests (fs::read("tests/test.kdbx"))
# lives at the repo root, not under backend-rust/ — needed for the test stage
# only. Never copied into the runtime stage below (this is literally the file
# VPS-INTEGRATION-PLAN.md warns not to deploy as production data).
COPY tests ./tests

# `key_roundtrip` is skipped deliberately, not by oversight: it exercises the
# real Linux kernel session keyring (KeyRingIdentifier::Session — see
# backend-rust/src/keepass/key.rs), which needs a real login session and the
# keyctl/add_key/request_key syscalls. Neither is reliably available inside an
# ephemeral `docker build` step (no session keyring context, and Docker's
# default build-time seccomp profile blocks those syscalls regardless — the
# custom profile at backend-rust/seccomp/keyring.json only applies to the
# container at Kubernetes *runtime*, not at image build time). All 30 other
# tests still run and gate the build normally.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/workspace/target \
    cargo test --release --locked -- --skip key_roundtrip && \
    cargo build --release --locked --bin keepass4web-rs && \
    cp /workspace/target/release/keepass4web-rs /out-binary

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out-binary /app/private-service

EXPOSE 9090
USER 65532:65532

CMD ["/app/private-service"]
