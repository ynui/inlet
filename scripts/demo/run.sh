#!/usr/bin/env bash
# End-to-end demo of the inlet client TUI.
#
#   scripts/demo.sh                 # record with:  script -q demo.typescript scripts/demo.sh
#
# Uses non-default ports so it does not clash with a running docker inletd.
set -u
set +m

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

W=$(mktemp -d "${TMPDIR:-/tmp}/inlet-demo.XXXXXX")
export HOME="$W/home"
mkdir -p "$HOME/.config/inlet" "$W/data"
export INLET_DB="$W/data/inlet.db"

cleanup() {
  [[ -n ${CLIENT:-} ]] && kill "$CLIENT" 2>/dev/null
  [[ -n ${SERVER:-} ]] && kill "$SERVER" 2>/dev/null
  [[ -n ${DEMOSRV:-} ]] && kill "$DEMOSRV" 2>/dev/null
  wait 2>/dev/null
  rm -rf "$W"
}
trap cleanup EXIT

go run ./scripts/demo-server -addr :8000 >"$W/demo-server.log" 2>&1 &
DEMOSRV=$!
sleep 1

./inletd -bind 127.0.0.1:18080 -control 127.0.0.1:18081 >"$W/server.log" 2>&1 &
SERVER=$!
sleep 1

# issue a token from the running server
TOKEN=$(./inletd token create --label demo | awk '/^token:/{print $2}')

# client config
cat > "$HOME/.config/inlet/config.json" <<EOF
{"server":"ws://127.0.0.1:18081","token":"$TOKEN","inspectPort":14040}
EOF

id=$(./inletd token list | awk '$NF == "active" {print $3; exit}')
echo "$ ./inlet http 8000"
sleep 1
./inlet http 8000 --inspect 14040 &
CLIENT=$!
sleep 2

echo "" >/dev/null
../demo-requests.sh --slow "http://localhost:8080/$id" >/dev/null 2>&1

# let uptime tick a few seconds so the pane shows up Ns
sleep 4

kill "$CLIENT" 2>/dev/null
wait "$CLIENT" 2>/dev/null
CLIENT=""
echo
echo "demo finished"
