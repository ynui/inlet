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

Routing is path-based: the first path segment is the tunnel ID, the rest is
forwarded untouched (path, query, headers, body, status).

## Requirements

- Go 1.23+ (no CGO, single dependency: `gorilla/websocket`)
- Docker + Compose (optional, for deployment)

## Quick start (local)

```bash
export INLET_TOKENS=dev-secret     # or put it in .env (loaded from the working dir)
make build                         # produces ./inlet and ./inletd

./inletd                           # public :8080, control :8081
./inlet auth dev-secret            # saves client config
./inlet http 3000                  # serve a local app on :3000
```

The client prints the public URL, e.g. `http://localhost:8080/0c5f8bdf6709`.
The same ID is reused on every reconnect for that token. Set `INLET_PUBLIC_BASE`
on the server to advertise the real public URL; otherwise the client guesses
`<scheme>://<control-host>:8080/<id>`.

## Demo

![inlet CLI forwarding requests](docs/screenshots/cli.gif)

The terminal pane shows the forwarded URL, live 2xx–5xx counts, and the last six
requests. The local inspector (`http://127.0.0.1:4040`) keeps the last 200 with
headers, bodies, and timings:

![inlet inspector](docs/screenshots/inspector.png)

## Client

```bash
inlet auth <token>                         # save token (server defaults to ws://localhost:8081)
inlet http <port>                          # tunnel a local HTTP server
inlet http <port> --new-url                # rotate the tunnel ID
inlet http <port> --inspect 4041           # inspector port (default 4040)
```

Flags work before or after the port. While running, the terminal shows a live
status pane (last 6 requests, 2xx–5xx counts). `Ctrl-C` exits and restores the
terminal. The inspector at `http://127.0.0.1:<inspectPort>` keeps the last 200
requests and is bound to loopback only.

The server URL is only settable in the client config file — there is no flag:

```json
// ~/.config/inlet/config.json
{ "server": "wss://tunnel.example.com/_inlet/control", "token": "…", "inspectPort": 4040 }
```

If the URL has no path, `/_inlet/control` is appended.

## Server

```bash
inletd [-bind :8080] [-control :8081]
```

Configuration precedence: defaults → `~/.config/inlet/server.json` → environment.
`INLET_TOKENS` is mandatory (else the server exits at startup).

| Env | Meaning | Default |
| --- | --- | --- |
| `INLET_TOKENS` | comma-separated allowed tokens | none (required) |
| `INLET_BIND` | public HTTP listener | `:8080` |
| `INLET_CONTROL_ADDR` | control WebSocket listener | `:8081` |
| `INLET_PUBLIC_BASE` | public base URL advertised to clients (e.g. `https://example.com`) | derived from control host |

Environment values override `server.json`. The server also loads `./.env` at
startup without overwriting variables that are already exported, so
`make run-server` works after `cp .env.example .env`.

`server.json` mirrors these keys: `{"bind": ":8080", "controlAddr": ":8081", "tokens": ["…"], "publicBase": "…"}`.

The server persists token → tunnel ID assignments in
`~/.config/inlet/assignments.json` (shared config dir with the client).

## Docker

```bash
cp .env.example .env       # then set INLET_TOKENS (and optionally INLET_PUBLIC_BASE)
docker compose up -d --build
```

Exposes `8080` (public) and `8081` (control), and mounts `./config/inlet` into the
container as the config directory so tunnel IDs survive restarts. `.env` and
`config/` are gitignored — don't commit real tokens.

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

Manual end-to-end check:

```bash
go run ./scripts/demo-server               # test target on :8000
./inlet http 8000                          # in another shell
scripts/demo-requests.sh                   # or: scripts/demo-requests.sh http://localhost:8080/<id>
```

`scripts/demo-server` fakes statuses, redirects, slow responses, sizes, and
echoes request details. `scripts/demo-requests.sh` drives the CLI pane and
inspector with varied methods, user agents, and payloads.

## Limitations

- HTTP only. There is no WebSocket upgrade support; the proxy forwards plain HTTP
  requests and strips `Connection`/`Upgrade`.
- Bodies are sent inline as base64 inside WebSocket text messages, and both
  ends cap messages at 512 KiB. Request/response bodies larger than roughly
  380 KiB close the control connection — there is no streaming or chunking.
- `INLET_PUBLIC_BASE` is only a display hint. Without it the client prints
  `scheme://control-host:8080/<id>`, which is wrong behind a proxy on another
  port (e.g. HTTPS on 443). Set it, or build the public URL from the tunnel ID.
- No origin check on the control WebSocket, no rate limiting, and tokens are
  long-lived bearer credentials. Terminate TLS and restrict the control port.
- The inspector buffers in memory only; nothing is persisted.

## License

[MIT](LICENSE)
