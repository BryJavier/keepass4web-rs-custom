# Runtime configuration

The committed files in `deploy/config/*.env.example` are safe templates, not
runtime configuration. For each environment, copy every service template into
its ignored sibling before setting real values:

```sh
cp deploy/config/go.env.example deploy/config/go.development.env
cp deploy/config/rust.env.example deploy/config/rust.development.env
cp deploy/config/supabase.env.example deploy/config/supabase.development.env
```

Repeat that copy operation with `staging` and `production`. Do not commit any
of the resulting files. The Go template configures the browser-facing service;
the Rust template configures the private service; and the Supabase template is
for backend tooling only. Never render a service-role value into a browser
asset, a Compose diagnostic, or a log.

Each Compose invocation uses one ignored environment file, for example
`deploy/config/development.compose.env`. It supplies the values used for
variable substitution and must contain the values from the relevant service
templates. Staging and production additionally require immutable image digests:

```dotenv
WEB_IMAGE=registry.example/keepass4web-web@sha256:replace-with-recorded-digest
RUST_IMAGE=registry.example/keepass4web-rust@sha256:replace-with-recorded-digest
```

Resolve configuration without printing values before starting a service:

```sh
docker compose -f docker-compose.yml -f deploy/compose/development.yml \
  --env-file deploy/config/development.compose.env config --quiet
docker compose -f docker-compose.yml -f deploy/compose/staging.yml \
  --env-file deploy/config/staging.compose.env config --quiet
docker compose -f docker-compose.yml -f deploy/compose/production.yml \
  --env-file deploy/config/production.compose.env config --quiet
```

Production secrets are injected from a managed secret store. Ignored files are
only a local and development convenience; they are not a production secret
system. Provider selection is deferred: the eventual platform must prove the
managed-secret, TLS, and private-network controls below before deployment.

`RUST_SERVICE_TOKEN` must have the identical value in the Go and Rust runtime
configuration. Compose sets `RUST_PRIVATE_LISTEN=0.0.0.0:8080` and
`PRIVATE_SERVICE_ALLOW_CONTAINER_BIND=true` only because the Rust container is
attached solely to the internal `private` network; do not use either setting on
a publicly routed host.
