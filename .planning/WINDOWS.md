---
schema_version: 1
open_count: 1
waived_count: 0
fixed_count: 4
total_count: 5
last_updated: 2026-08-02T22:06:41.540Z
---

# Broken Windows Ledger

> Cross-phase defect register. `/gsd-ship` blocks while `open_count > 0`.
> Waive with `gsd-tools windows waive <id> "<reason>"` (reason required).
> Mark fixed with `gsd-tools windows fixed <id>`.

| id | phase | kind | file | line | description | status | reason | recorded_at | resolved_at |
|----|-------|------|------|------|-------------|--------|--------|-------------|-------------|
| 1 | 01 | deviation | cmd/web/Dockerfile |  | Replaced unpublished golang:stable tag with pinned official Go 1.26.5 image. | fixed |  | 2026-08-02T14:46:58.557Z | 2026-08-02T14:48:58.591Z |
| 2 | 01 | deviation | docker-compose.yml |  | Added WEB_HOST_PORT override to complete tracer verification around unrelated local port conflict. | fixed |  | 2026-08-02T14:46:58.669Z | 2026-08-02T14:48:58.693Z |
| 3 | 01 | deviation | cmd/web/Dockerfile |  | Added Git in test stage so required git check-ignore contract runs in container. | fixed |  | 2026-08-02T14:46:58.763Z | 2026-08-02T14:48:58.783Z |
| 4 | 01 | deviation | tests/operations/config_templates_test.go |  | Narrowed secret-shaped heuristic so safe issuer URLs are not classified as tokens. | fixed |  | 2026-08-02T14:46:58.853Z | 2026-08-02T14:48:58.870Z |
| 5 | 01 | deviation | cmd/web/main.go |  | Mirrored already-sanitized JSON events to stdout and stderr so the output audit fails closed for both process sinks. | open |  | 2026-08-02T22:06:41.540Z |  |

````json
[
  {
    "id": 1,
    "kind": "deviation",
    "phase": "01",
    "file": "cmd/web/Dockerfile",
    "line": null,
    "description": "Replaced unpublished golang:stable tag with pinned official Go 1.26.5 image.",
    "status": "fixed",
    "reason": "",
    "recorded_at": "2026-08-02T14:46:58.557Z",
    "resolved_at": "2026-08-02T14:48:58.591Z"
  },
  {
    "id": 2,
    "kind": "deviation",
    "phase": "01",
    "file": "docker-compose.yml",
    "line": null,
    "description": "Added WEB_HOST_PORT override to complete tracer verification around unrelated local port conflict.",
    "status": "fixed",
    "reason": "",
    "recorded_at": "2026-08-02T14:46:58.669Z",
    "resolved_at": "2026-08-02T14:48:58.693Z"
  },
  {
    "id": 3,
    "kind": "deviation",
    "phase": "01",
    "file": "cmd/web/Dockerfile",
    "line": null,
    "description": "Added Git in test stage so required git check-ignore contract runs in container.",
    "status": "fixed",
    "reason": "",
    "recorded_at": "2026-08-02T14:46:58.763Z",
    "resolved_at": "2026-08-02T14:48:58.783Z"
  },
  {
    "id": 4,
    "kind": "deviation",
    "phase": "01",
    "file": "tests/operations/config_templates_test.go",
    "line": null,
    "description": "Narrowed secret-shaped heuristic so safe issuer URLs are not classified as tokens.",
    "status": "fixed",
    "reason": "",
    "recorded_at": "2026-08-02T14:46:58.853Z",
    "resolved_at": "2026-08-02T14:48:58.870Z"
  },
  {
    "id": 5,
    "kind": "deviation",
    "phase": "01",
    "file": "cmd/web/main.go",
    "line": null,
    "description": "Mirrored already-sanitized JSON events to stdout and stderr so the output audit fails closed for both process sinks.",
    "status": "open",
    "reason": "",
    "recorded_at": "2026-08-02T22:06:41.540Z",
    "resolved_at": null
  }
]
````
