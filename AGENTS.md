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
  `scripts/demo-requests.sh [base]` (no base → takes the first active tunnel from `inletd token list`).

## Layout

- `cmd/client/main.go` — CLI + alternate-screen status pane. `parseHTTPArgs` accepts flags
  before or after the port; covered by `TestParseHTTPArgs`.
- `internal/client/` — control WS, per-request HTTP proxy, inspector (`inspector.html`
  is embedded via `go:embed`).
- `internal/server/` — `Hub` (control + response routing), public path proxy, signup endpoint.
- `internal/config/` — JSON persistence in `~/.config/inlet/`; server and client share the dir.
- `internal/store/` — SQLite token/invite store, hashed secrets, stale cleanup.
- `scripts/demo-server` — test target, not part of release builds.

## Gotchas

- Config precedence on server: CLI `-bind`/`-control` are seed defaults →
  `~/.config/inlet/server.json` → `INLET_*` env (env highest; config overrides explicit flags).
  Tokens live in SQLite (`./data/inlet.db`, or `/data/inlet.db` in Docker; override with
  `INLET_DB`), not in env. `INLET_TOKENS` is imported once as bootstrap tokens when
  the DB is empty.
- Client server URL has no CLI flag: edit `~/.config/inlet/config.json`. `Connect`
  appends `/_inlet/control` only when the URL path is empty.
- Tunnel IDs are 12 hex chars, stored on the token row; `--new-url` rotates them.
  `assignments.json` is imported once on first boot, then unused.
- `Registered.PublicURL` uses `INLET_PUBLIC_BASE` (or `server.json`'s `publicBase`)
  when set, otherwise it is derived from the control request
  (`X-Forwarded-Proto`/`Host`), swapping in the public listener's port.
  `/<id>` and `/<id>/` route identically.
- Control messages are fragmented transparently at 64 KiB; body data streams in
  32 KiB chunks. Both sides still `SetReadLimit(512 * 1024)` per WebSocket message.
- HTTP only: no WebSocket upgrade support; proxy strips `Connection`/`Upgrade`.
- Inspector defaults to `127.0.0.1:4040` (override with `--inspect`); embedded HTML polls
  `/api/state` — change the JSON shape in both `internal/client/inspector.go` and
  `inspector.html`.
- Runtime state (`data/`, `.env`) is gitignored; never commit tokens. Compose mounts
  `./data` to `/data` (`INLET_DB=/data/inlet.db`).
- Admin commands (`token`, `invite`) operate on the local DB; run them in the same
  working directory as `inletd` (or with `INLET_DB` pointing at the right file).

## Conventions

- Changing the wire protocol requires updating `internal/proto`, the client read/write
  paths, and `internal/server/control.go` together.
- Tests are plain stdlib (`testing`, no frameworks/mocks). Add one small test for
  non-trivial parsing/rendering logic; terminal output is asserted by substring.
- Keep the inspector UI self-contained in the embedded HTML — no build step exists.
" path="/Users/yuval/Coding/tunneler/AGENTS.md