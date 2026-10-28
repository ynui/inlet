#!/usr/bin/env bash
# Fires a varied batch of requests at a running inlet tunnel so the CLI pane and
# inspector can be checked against different methods, user agents and payloads.
#
#   scripts/demo-requests.sh                          # derive tunnel from ~/.config/inlet
#   scripts/demo-requests.sh http://localhost:8080/id # explicit tunnel base
#
# Pairs with scripts/demo-server:
#   go run ./scripts/demo-server -addr :8000 &
#   ./inlet http 8000
#   scripts/demo-requests.sh
set -uo pipefail

base=${1:-}
if [[ -z $base ]]; then
  cfg=${HOME}/.config/inlet
  token=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$cfg/config.json")
  id=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]]["id"])' "$cfg/assignments.json" "$token")
  base="http://localhost:8080/$id"
fi
base=${base%/}

send() {
  local method=$1 path=$2 code size secs req
  shift 2
  # curl -X HEAD waits for a response body that never arrives, stalling until
  # --max-time. -I is the actual HEAD request and returns immediately.
  req=(-X "$method")
  [[ $method == HEAD ]] && req=(-I)
  read -r code size secs < <(curl -s --max-time 10 -o /dev/null -w '%{http_code} %{size_download} %{time_total}' "${req[@]}" "$base$path" "$@")
  printf '  %-7s %-46s %s %8sB %ss\n' "$method" "$path" "$code" "$size" "$secs"
}

ua() { send "$1" "$2" -A "$3" "${@:4}"; }

echo "target: $base"

send GET /
send HEAD /
send OPTIONS /
send GET /health
send GET "/api/users?page=2&limit=50"
send GET /static/app.js
send POST /api/users -H 'Content-Type: application/json' -d '{"name":"demo","role":"admin"}'
send PUT /api/users/42 -H 'Content-Type: application/json' -d '{"name":"demo","role":"owner"}'
send PATCH /api/users/42 -H 'Content-Type: application/json' -d '{"role":"admin"}'
send POST /login -H 'Content-Type: application/x-www-form-urlencoded' -d 'user=demo&pass=secret'
send DELETE /api/users/42
send GET /missing-page
send GET /html
send GET /status/201
send GET /status/302
send GET /status/400
send GET /status/401
send GET /status/500
send GET /redirect
send GET "/slow?ms=250"
send GET /bytes/4096
send GET "/very/long/path/$(printf 'segment%.0s' {1..12})"

for u in \
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/129.0' \
  'curl/8.7.1' \
  'python-requests/2.32.3' \
  'okhttp/4.12.0' \
  'PostmanRuntime/7.39.0'; do
  ua GET / "$u"
done

send POST /upload -A 'Mozilla/5.0' -H 'Content-Type: application/octet-stream' --data-binary @<(head -c 2048 /dev/urandom)

# burst: more than the 6 visible rows, oldest should fall off the pane
for i in {1..10}; do
  send GET "/burst/$i"
done

# finish on a varied mix so the 6-row pane shows more than the burst
send GET /status/404
send POST /api/users -H 'Content-Type: application/json' -d '{"name":"demo","role":"admin"}'
send GET "/slow?ms=250"
send GET /status/500
send GET "/api/users?page=2&limit=50"
send DELETE /api/users/42
