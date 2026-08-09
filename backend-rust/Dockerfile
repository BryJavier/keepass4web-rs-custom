FROM rust:1.97.1-slim-bookworm AS build

WORKDIR /workspace

RUN apt-get update && apt-get install -y --no-install-recommends pkg-config libssl-dev && rm -rf /var/lib/apt/lists/*

COPY Cargo.toml Cargo.lock ./
COPY backend-rust ./backend-rust

RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/workspace/target \
    cargo test --release --locked && \
    cargo build --release --locked --bin keepass4web-rs && \
    cp /workspace/target/release/keepass4web-rs /out-binary

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out-binary /app/private-service

EXPOSE 9090
USER 65532:65532

CMD ["/app/private-service"]
