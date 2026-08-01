# Repository Guidelines

## Project Structure & Module Organization

Rust application code lives in `src/`. `server/` owns the Actix HTTP server and API routes; `keepass/` handles KDBX decoding, encrypted cache data, and entry mapping; `auth_backend/`, `db_backend/`, and `config/` contain their respective pluggable implementations. Frontend source is in `js/scripts/` and `js/style/`; Browserify produces `public/scripts/bundle.js`. Static assets and the HTML shell are under `public/`. Rust unit tests sit beside their modules, while Playwright end-to-end tests live in `tests/e2e/`.

## Build, Test, and Development Commands

- `cargo build` — compile the Rust backend.
- `cargo test` — run Rust unit and integration tests.
- `cargo run -- --config config.yml` — run locally with the selected configuration.
- `npm ci` — install locked frontend/test dependencies.
- `npm run dev` — create an unminified frontend bundle.
- `npm run build` — generate the production bundle used by the server.
- `npm test` — run Playwright end-to-end tests; install Chromium first with `npx playwright install --with-deps chromium`.

CI uses Node 20 and runs both `cargo test` and the Playwright suite. Build the frontend before browser tests.

## Coding Style & Naming Conventions

Follow idiomatic Rust: four-space indentation, `snake_case` for functions/modules, `CamelCase` for types, and `SCREAMING_SNAKE_CASE` for constants. Keep route handlers thin and put domain logic in the corresponding module. Run `cargo fmt` before submitting Rust changes; use `cargo clippy --all-targets --all-features` when practical. JavaScript uses existing React component naming (`LoginForm.js`, `TreeViewer.js`) and camelCase variables; avoid unrelated formatting churn.

## Testing Guidelines

Add focused Rust tests in the module being changed, naming cases after behavior such as `read_ok` or `write_fail`. Extend `tests/e2e/main-flow.spec.js` for user-visible flows. Tests must not require real credentials or production KeePass data; use the test configuration and fixtures.

## Commit & Pull Request Guidelines

Recent history follows concise imperative subjects and Conventional Commit-style prefixes, e.g. `fix: handle expired session` or `build(deps): bump serde`. Keep commits single-purpose. Pull requests should describe behavior and security implications, link relevant issues, list verification commands, and include screenshots for frontend changes.

## Security & Configuration

Do not commit `.kdbx` files, keyfiles, passwords, tokens, or production `config.yml` values. Treat protected entry data and session keys as secrets. Prefer environment- or deployment-provided configuration, and review authentication, cookie, and cache-timeout changes carefully.
