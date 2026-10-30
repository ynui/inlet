#!/usr/bin/env bash
# Prepares a local demo environment (servers + token + client config) and
# schedules the request burst + cleanup in the background. Source it:
#   source scripts/demo/setup.sh
set +u
set +m

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
if [[ -z "$ROOT" ]]; then
  echo "run this setup from inside the inlet repository" >&2
  return 1
fi
cd "$ROOT" || return 1

W=$(mktemp -d "${TMPDIR:-/tmp}/inlet-demo.XXXXXX")
export HOME="$W/home"
mkdir -p "$HOME/.config/inlet" "$W/data"
DB_PATH="$W/data/inlet.db"

# demo server
go run ./scripts/demo-server -addr :8000 >"$W/demo-server.log" 2>&1 &
DEMOSRV=$!

# Use a temporary working directory so a repository .env cannot affect the demo.
(cd "$W" && INLET_OPEN_SIGNUP=true INLET_DB="$DB_PATH" "$ROOT/inletd" -bind 127.0.0.1:8080 -control 127.0.0.1:8081) >"$W/server.log" 2>&1 &
SERVER=$!

for _ in {1..40}; do
  if curl -fsS http://127.0.0.1:8080/_inlet/healthz >/dev/null 2>&1 && curl -fsS http://127.0.0.1:8000/ >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

cat > "$HOME/.config/inlet/config.json" <<EOF
{"server":"ws://127.0.0.1:8081","inspectPort":4040}
EOF

# Wait for the signup in the recording to create a token, then wait until its
# client is connected before sending requests through that tunnel.
(
  id=
  for _ in {1..120}; do
    id=$(cd "$W" && INLET_DB="$DB_PATH" "$ROOT/inletd" token list | awk '$NF == "active" {print $3; exit}')
    if [[ -n "$id" ]] && curl -fsS "http://127.0.0.1:8080/$id/" >/dev/null 2>&1; then
      break
    fi
    sleep 0.25
  done
  if [[ -n "$id" ]]; then
    "$ROOT/scripts/demo-requests.sh" --demo "http://127.0.0.1:8080/$id" >/dev/null 2>&1
  else
    echo "no active demo tunnel found; signup may have failed" >>"$W/server.log"
  fi
  sleep 12
  pkill -f 'inlet http 8000' 2>/dev/null
  sleep 1
  kill "$SERVER" "$DEMOSRV" 2>/dev/null
) &

echo "ready (signup enabled; tunnel requests will start after the client connects)"
