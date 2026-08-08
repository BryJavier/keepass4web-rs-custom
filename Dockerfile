FROM docker.io/rust:1.97.1-alpine AS build

WORKDIR /workspace

RUN apk add --no-cache build-base

COPY backend-rust/src backend-rust/src
COPY Cargo.* ./

ENV RUSTFLAGS="-Ctarget-cpu=sandybridge -Ctarget-feature=+aes,+sse2,+sse4.1,+ssse3"
RUN cargo build --locked --bins --release

FROM build AS test
RUN cargo test --locked

FROM scratch

COPY --from=build /workspace/target/release/keepass4web-rs /keepass4web
COPY backend-rust/config.yml /conf/config.yml

EXPOSE 8080
USER 1000:1000

HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD ["/keepass4web", "--help"]

CMD ["/keepass4web", "--config", "/conf/config.yml"]
