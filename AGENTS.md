# AGENTS.md

Go 1.23 HTTP tunnel (`inlet`). Two binaries: `cmd/client` → `inlet`, `cmd/server` → `inletd`.
Server routes `/<tunnel-id>/...` on the public listener to a client connected over one
WebSocket control channel (`/_inlet/control`, JSON messages defined in `internal/proto`).
The client forwards each request to `http://127.0.0.1:<port>`.

## Commands

- `make build` → `./inlet`, `./inletd` (gitignored); `make build-all` → `dist/` (4 targets).
- `make run-server`, `make run-client` (tunnels `:3000`), `make run-demo-server`.
- `make clean` removes binaries + `dist/`.
- `go test ./...` or `make test`; single: `go test ./cmd/client -run TestParseHTTPArgs`.
- `go vet ./...` and `gofmt -l cmd internal` (no lint/CI config).
- E2E demo: `go run ./scripts/demo-server` + `./inlet http 8000` +
  `scripts/demo-requests.sh [base]` (no base → reads `~/.config/inlet/{config,assignments}.json`).

## Layout

- `cmd/client/main.go` — CLI + alternate-screen status pane. `parseHTTPArgs` accepts flags
  before or after the port; covered by `TestParseHTTPArgs`.
- `internal/client/` — control WS, per-request HTTP proxy, inspector (`inspector.html`
  is embedded via `go:embed`).
- `internal/server/` — `Hub` (control + response routing), public path proxy, ID assignment store.
- `internal/config/` — JSON persistence in `~/.config/inlet/`; server and client share the dir.
- `scripts/demo-server` — test target, not part of release builds.

## Gotchas

- Config precedence on server: CLI `-bind`/`-control` are seed defaults →
  `~/.config/inlet/server.json` → `INLET_*` env (env highest; config overrides explicit flags).
  `INLET_TOKENS` is required or the process exits.
- Client server URL has no CLI flag: edit `~/.config/inlet/config.json`. `Connect`
  appends `/_inlet/control` only when the URL path is empty.
- Tunnel IDs are 12 hex chars, persisted per token; `--new-url` rotates them. Token→ID
  assignments live in `~/.config/inlet/assignments.json`.
- `Registered.PublicURL` is set only when `INLET_PUBLIC_BASE` (or `server.json`'s
  `publicBase`) is configured. Otherwise the client fabricates
  `scheme://control-host:8080/<id>` (no trailing slash), which is wrong behind a proxy.
  `/<id>` and `/<id>/` route identically.
- Bodies are base64 in JSON; both sides `SetReadLimit(512 * 1024)`, so payloads above
  ~380 KiB kill the connection — no chunking. Keep demo payloads small.
- HTTP only: no WebSocket upgrade support; proxy strips `Connection`/`Upgrade`.
- Inspector defaults to `127.0.0.1:4040` (override with `--inspect`); embedded HTML polls
  `/api/state` — change the JSON shape in both `internal/client/inspector.go` and
  `inspector.html`.
- Runtime state (`config/inlet/`, `.env`) is gitignored and mounted by compose; never
  commit tokens.
- Docker compose mounts `./config/inlet` (not `~/.config/inlet`) into the container.

## Conventions

- Changing the wire protocol requires updating `internal/proto`, the client read/write
  paths, and `internal/server/control.go` together.
- Tests are plain stdlib (`testing`, no frameworks/mocks). Add one small test for
  non-trivial parsing/rendering logic; terminal output is asserted by substring.
- Keep the inspector UI self-contained in the embedded HTML — no build step exists.
" path="/Users/yuval/Coding/tunneler/AGENTS.md