FROM node:20.20.2-alpine AS assets

WORKDIR /workspace

COPY package*.json ./
RUN npm ci

COPY js js
COPY public public

RUN cp node_modules/bootstrap/fonts/* public/fonts/ && npm run build

FROM docker.io/rust:1.97.1-alpine AS build

WORKDIR /workspace

COPY --from=assets /workspace/public public
COPY src src
COPY Cargo.* ./

RUN apk add --no-cache build-base
ENV RUSTFLAGS="-Ctarget-cpu=sandybridge -Ctarget-feature=+aes,+sse2,+sse4.1,+ssse3"
RUN cargo build --locked --bins --release

FROM scratch

COPY --from=build /workspace/public /public
COPY --from=build /workspace/target/release/keepass4web-rs /keepass4web
COPY config.yml /conf/config.yml

EXPOSE 8080
VOLUME /conf
USER 1000:1000

HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD ["/keepass4web", "--help"]

CMD ["/keepass4web", "--config", "/conf/config.yml"]
