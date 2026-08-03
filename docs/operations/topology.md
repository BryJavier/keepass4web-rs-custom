# Deployment topology and rollback

All environments resolve the same boundary: Go is the only publicly exposed application service.
`web` is the only service with a host port. Rust has no public listener, host
port, route, or ingress; it joins only the internal `private` network and is
reachable solely from Go.

Production is provider-neutral. Provider selection is deferred, but a
production platform must inject real values from a managed secret store and
terminate external TLS at a TLS-terminating ingress attached only to Go. Rust
must remain off every public route and ingress, even while images are replaced
or rolled back.

## Start and validate

Use the matching ignored environment file and Compose overlay. Development can
build locally; staging and production require the immutable digests recorded
in their environment files.

```sh
docker compose -f docker-compose.yml -f deploy/compose/development.yml \
  --env-file deploy/config/development.compose.env up --build -d
docker compose -f docker-compose.yml -f deploy/compose/staging.yml \
  --env-file deploy/config/staging.compose.env up -d
docker compose -f docker-compose.yml -f deploy/compose/production.yml \
  --env-file deploy/config/production.compose.env up -d

curl --fail --silent --show-error http://127.0.0.1:8080/healthz
! curl --connect-timeout 2 --fail http://127.0.0.1:8080/rust-service
docker run --rm --network keepass4web-rs-custom_private curlimages/curl:8.12.1 \
  --fail --silent --show-error http://rust-service:8080/healthz
```

The last command is an operator reachability probe from the private network;
it is not a browser route. Use the project-specific Compose network name shown
by `docker network ls` if the deployment name differs.

Before production rollout, run the matching `config --quiet` command from the
configuration guide, confirm the TLS-terminating ingress routes only to Go,
and confirm the managed secret store injects the values without rendering them
to logs or diagnostics.

## Shutdown and rollback

Stop an environment cleanly with its same overlay and ignored environment file:

```sh
docker compose -f docker-compose.yml -f deploy/compose/development.yml \
  --env-file deploy/config/development.compose.env down
docker compose -f docker-compose.yml -f deploy/compose/staging.yml \
  --env-file deploy/config/staging.compose.env down
docker compose -f docker-compose.yml -f deploy/compose/production.yml \
  --env-file deploy/config/production.compose.env down
```

To roll back staging or production, replace `WEB_IMAGE` and `RUST_IMAGE` with
the previously recorded immutable digests, resolve with `config --quiet`, then
run `up -d` again. The rollback must never publish Rust or attach ingress to it.
