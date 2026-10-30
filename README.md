# inlet

Minimal HTTP reverse tunnel in Go. One server binary (`inletd`), one client binary (`inlet`), token auth,
stable public URLs per token, and a local request inspector.

```
browser ──► https://tunnel.example.com/<tunnel-id>/...   (public listener :8080)
                              │
                        inletd server ◄── WebSocket control channel :8081
                              │
                        inlet client ──► http://127.0.0.1:3000
```

```
browser -> :8080 public -> hub -> (ws :8081) -> client -> localhost:port
```

Routing is path-based: the first path segment is the tunnel ID, the rest is
forwarded untouched (path, query, headers, body, status).

## Demo

![inlet CLI forwarding requests](docs/screenshots/cli.gif)

The terminal pane shows the forwarded URL, live 2xx–5xx counts, and the last six
requests. The local inspector (`http://127.0.0.1:4040`) keeps the last 200 with
headers, bodies, and timings:

![inlet inspector](docs/screenshots/inspector.png)

## Requirements

- Go 1.23+ (no CGO; `gorilla/websocket` + `modernc.org/sqlite`)
- Docker + Compose (optional, for deployment)

## Quick start (local)

```bash
make build        # produces ./inlet and ./inletd
```

All state lives in one local SQLite DB (`./data/inlet.db`, in the directory you start
`inletd` from). Admin commands (`invite`, `token`) write to that DB directly —
run them from that same directory, with the server stopped or running.

### 1. Create an invite (or a token)

```bash
./inletd invite create --ttl 24h --label me   # prints a one-time code
./inletd token create --label me              # alternative: token straight away
```

Invites are hashed, single-use, and expire. The code is only printed once.
`--ttl` defaults to `INLET_INVITE_TTL` (`24h`).

### 2. Start the server

```bash
INLET_PUBLIC_BASE=http://localhost:8080 ./inletd
# public :8080, control :8081
```

`INLET_PUBLIC_BASE` is what the client prints. Without it the server derives the
URL from the control request host, swapping in the public listener's port;
behind a proxy the `X-Forwarded-Proto` / `X-Forwarded-Host` headers win.

Invite-only is the default. For open signup:

```bash
INLET_OPEN_SIGNUP=true ./inletd
```

The server loads `./.env` from the working directory, so a leftover
`INLET_OPEN_SIGNUP=true` there also enables open signup — comment it out to
test the invite-only path.

### 3. Claim the invite on the client

```bash
./inlet signup --invite <code>     # saves the token to ~/.config/inlet/config.json
./inlet signup --server ws://localhost:8081 --invite <code>   # explicit server
./inlet signup                     # open signup only (no --invite)
```

`--server` defaults to `ws://localhost:8081`. Trying to reuse an invite fails
with `403 invalid or expired invite`.

### 4. Tunnel something

```bash
go run ./scripts/demo-server       # test target on :8000, in another shell
./inlet http 8000                  # prints the public URL
```

### 5. Hit it

```bash
curl http://localhost:8080/<id>/   # <id> from the printed URL
open http://127.0.0.1:4040         # local inspector, last 200 requests
```

Manage tokens/invites while it runs:

```bash
./inletd token list                # prefix, source, tunnel ID, last used, state
./inletd token revoke <prefix>
./inletd invite create --ttl 1h
```

`make dev` / `scripts/dev.sh start` does steps 1–2 for you (builds, creates a
`dev` token, starts `inletd` + the demo server); then run `./inlet http 8000`
and stop with `make dev-stop`.

The same tunnel ID is reused on every reconnect for that token. `--new-url` on
`inlet http` rotates it.

## Client

```bash
inlet signup --server ws://localhost:8081  # open signup, or --invite <code>
inlet auth <token>                         # save an existing token
inlet http <port>                          # tunnel a local HTTP server
inlet http <port> --new-url                # rotate the tunnel ID
inlet http <port> --inspect 4041           # inspector port (default 4040)
```

Flags work before or after the port. While running, the terminal shows a live
status pane (last 6 requests, 2xx–5xx counts). `Ctrl-C` exits and restores the
terminal. The inspector at `http://127.0.0.1:<inspectPort>` keeps the last 200
requests and is bound to loopback only.

The server URL is saved in the client config. `inlet signup --server …` sets it;
`inlet auth` and `inlet http` only read it:

```json
// ~/.config/inlet/config.json
{ "server": "wss://tunnel.example.com/_inlet/control", "token": "…", "inspectPort": 4040 }
```

If the URL has no path, `/_inlet/control` is appended.

## Server

```bash
inletd [-bind :8080] [-control :8081]
```

Configuration precedence: flag defaults → `~/.config/inlet/server.json` →
environment (env highest; config overrides explicit flags).

| Env | Meaning | Default |
| --- | --- | --- |
| `INLET_BIND` | public HTTP listener | `:8080` |
| `INLET_CONTROL_ADDR` | control WebSocket listener | `:8081` |
| `INLET_PUBLIC_BASE` | public base URL advertised to clients (e.g. `https://example.com`) | derived from control request |
| `INLET_OPEN_SIGNUP` | allow token creation without an invite | `false` |
| `INLET_DB` | SQLite database path | `./data/inlet.db` |
| `INLET_STALE_AFTER` | delete inactive invite/admin tokens after | `720h` |
| `INLET_OPEN_STALE_AFTER` | delete inactive open-signup tokens after | `168h` |
| `INLET_REVOKED_RETENTION` | keep revoked tokens before deleting | `168h` |
| `INLET_INVITE_TTL` | default invite lifetime | `24h` |

Environment values override `server.json`. The server also loads `./.env` at
startup without overwriting variables that are already exported, so
`make run-server` works after `cp .env.example .env`.

`server.json` mirrors these keys: `{"bind": ":8080", "controlAddr": ":8081", "publicBase": "…", "openSignup": false}`.

Tokens are stored as SHA-256 hashes in SQLite. Each token row owns its tunnel ID;
`assignments.json` is imported once on first boot and then no longer written.
Existing `INLET_TOKENS` values are imported as bootstrap tokens when the DB is
empty, then ignored.

Admin commands run against the local DB:

```bash
inletd token create --label laptop   # print a token once
inletd token list
inletd token revoke <prefix>
inletd invite create --ttl 24h
```

A janitor removes expired invites and stale tokens hourly. Tokens with an active
connection are skipped. Revocation applies to new connections immediately; an
already-connected client keeps its session until it reconnects.

## Docker

```bash
cp .env.example .env       # optionally set INLET_PUBLIC_BASE / INLET_OPEN_SIGNUP
docker compose up -d --build
docker exec -it inletd inletd token create --label admin
```

Exposes `8080` (public) and `8081` (control), and keeps the database in
`./data/inlet.db` (mounted as `/data`, `INLET_DB=/data/inlet.db`) so tokens and
tunnel IDs survive restarts. `.env` and `data/` are gitignored — don't commit
real tokens.

For production, terminate TLS in front of both ports:

- public traffic: `https://tunnel.example.com/<id>/...` → server `:8080`
- client control: `wss://tunnel.example.com/_inlet/control` → server `:8081`
- point clients at the full `wss://…/_inlet/control` URL in their config

## Development

```bash
go test ./...                              # all tests
go test ./cmd/client -run TestParseHTTPArgs
go vet ./...
make build-all                             # linux/amd64 + darwin/arm64 into dist/
```

Manual end-to-end check (after `inlet signup` or `inlet auth` from Quick start):

```bash
go run ./scripts/demo-server               # test target on :8000
./inlet http 8000                          # in another shell
scripts/demo-requests.sh                   # or: scripts/demo-requests.sh http://localhost:8080/<id>
```

`scripts/demo-server` fakes statuses, redirects, slow responses, sizes, and
echoes request details. `scripts/demo-requests.sh` drives the CLI pane and
inspector with varied methods, user agents, and payloads.

Regenerate the README screenshot (`docs/screenshots/cli.gif`) with:

```bash
source scripts/demo/setup.sh               # start servers + schedule requests
vhs scripts/demo/demo.tape                # records docs/screenshots/cli.gif
```

## Limitations

- HTTP only. There is no WebSocket upgrade support; the proxy forwards plain HTTP
  requests and strips `Connection`/`Upgrade`.
- No built-in TLS. Terminate TLS in front of both the public and control ports.
- The inspector listens on loopback only and keeps the last 200 requests in memory.
- HTTP request/response bodies are streamed in chunks over the control
  WebSocket, so large payloads no longer kill the connection. Individual
  control messages remain capped at 512 KiB.
- Set `INLET_PUBLIC_BASE` for the exact public URL. Otherwise the server
  derives one from the control request (honoring `X-Forwarded-Proto` /
  `X-Forwarded-Host`), using the control host with the public listener's port.
- The control WebSocket rejects cross-origin browser requests. Open signup is
  rate limited per IP (5 attempts/hour, 3 active open tokens/IP); control
  connections are not. Tokens are long-lived bearer credentials — terminate
  TLS and restrict the control port.

## License

[MIT](LICENSE)
