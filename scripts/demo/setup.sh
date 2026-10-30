#!/usr/bin/env bash
# Prepares a local demo environment (servers + token + client config) and
# schedules the request burst + cleanup in the background. Source it:
#   source scripts/demo-setup.sh
set +u
set +m

if [[ -f scripts/demo-requests.sh ]]; then ROOT=.; 
elif [[ -f ../demo-requests.sh ]]; then ROOT=..; 
else ROOT=.; fi
cd "$ROOT" || exit 1

W=$(mktemp -d "${TMPDIR:-/tmp}/inlet-demo.XXXXXX")
export HOME="$W/home"
mkdir -p "$HOME/.config/inlet" "$W/data"
export INLET_DB="$W/data/inlet.db"

# demo server
go run ./scripts/demo-server -addr :8000 >"$W/demo-server.log" 2>&1 &
DEMOSRV=$!
sleep 1

# inletd (default ports)
./inletd >"$W/server.log" 2>&1 &
SERVER=$!
sleep 1

TOKEN=$(./inletd token create --label demo | awk '/^token:/{print $2}')
id=$(./inletd token list | awk '$NF == "active" {print $3; exit}')

cat > "$HOME/.config/inlet/config.json" <<EOF
{"server":"ws://127.0.0.1:8081","token":"$TOKEN","inspectPort":4040}
EOF

# schedule request burst + teardown in the background
(
  sleep 6
  ../demo-requests.sh --demo "http://localhost:8080/$id" >/dev/null 2>&1
  sleep 4
  pkill -f 'inlet http 8000' 2>/dev/null
  sleep 1
  kill "$SERVER" "$DEMOSRV" 2>/dev/null
) &

echo "ready (tunnel $id)"
