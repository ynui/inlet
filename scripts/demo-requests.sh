#!/usr/bin/env bash
# Fires a varied batch of requests at a running inlet tunnel so the CLI pane and
# inspector can be checked against different methods, user agents and payloads.
#
#   scripts/demo-requests.sh                          # derive tunnel from ~/.config/inlet
#   INLETD="docker exec inletd inletd" scripts/demo-requests.sh
#   scripts/demo-requests.sh http://localhost:8080/id # explicit tunnel base
#   scripts/demo-requests.sh --slow                   # pause between requests
#
# Pairs with scripts/demo-server:
#   go run ./scripts/demo-server -addr :8000 &
#   ./inlet http 8000
#   scripts/demo-requests.sh
set -uo pipefail

base=
slow=0
demo=0
while (($#)); do
  case $1 in
    --slow) slow=1 ;;
    --demo) demo=1; slow=1 ;;
    *)
      if [[ -n $base ]]; then
        echo "usage: $0 [--slow] [tunnel-base]" >&2
        exit 2
      fi
      base=$1
      ;;
  esac
  shift
done

INLETD=${INLETD:-}

if [[ -z $base ]]; then
  id=
  if [[ -n $INLETD ]]; then
    id=$($INLETD token list 2>/dev/null | awk '$NF == "active" {print $3; exit}')
  else
    for cmd in "./inletd" "docker exec inletd inletd"; do
      id=$($cmd token list 2>/dev/null | awk '$NF == "active" {print $3; exit}')
      if [[ -n $id ]]; then
        INLETD=$cmd
        break
      fi
    done
  fi
  if [[ -z $id ]]; then
    echo "no active tunnel found (tried ${INLETD:-./inletd and docker})" >&2
    exit 1
  fi
  base="http://localhost:8080/$id"
fi
base=${base%/}

send() {
  local method=$1 path=$2 code size secs req
  shift 2
  if (( slow && requests > 0 )); then
    sleep "0.$((3 + RANDOM % 5))"
  fi
  requests=$((requests + 1))
  # curl -X HEAD waits for a response body that never arrives, stalling until
  # --max-time. -I is the actual HEAD request and returns immediately.
  req=(-X "$method")
  [[ $method == HEAD ]] && req=(-I)
  read -r code size secs < <(curl -s --max-time 10 -o /dev/null -w '%{http_code} %{size_download} %{time_total}' "${req[@]}" "$base$path" "$@")
  printf '  %-7s %-46s %s %8sB %ss\n' "$method" "$path" "$code" "$size" "$secs"
}

requests=0

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
(( demo )) || send GET "/very/long/path/$(printf 'segment%.0s' {1..12})"

for u in \
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/129.0' \
  'curl/8.7.1' \
  'python-requests/2.32.3' \
  'okhttp/4.12.0' \
  'PostmanRuntime/7.39.0'; do
  ua GET / "$u"
done

(( demo )) || send POST /upload -A 'Mozilla/5.0' -H 'Content-Type: application/octet-stream' --data-binary @<(head -c 2048 /dev/urandom)

# burst: more than the 6 visible rows, oldest should fall off the pane
burst=10
(( demo )) && burst=4
for ((i=1;i<=burst;i++)); do
  send GET "/burst/$i"
done

# finish on a varied mix so the 6-row pane shows more than the burst
send GET /status/404
send POST /api/users -H 'Content-Type: application/json' -d '{"name":"demo","role":"admin"}'
send GET "/slow?ms=250"
send GET /status/500
send GET "/api/users?page=2&limit=50"
send DELETE /api/users/42
