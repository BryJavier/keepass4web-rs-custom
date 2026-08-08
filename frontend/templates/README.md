# Server templates

The initial vertical slice keeps templates embedded in `internal/web` so the
binary has no runtime template-path dependency. This directory is reserved for
larger Go HTML template partials as the vault interface grows.
