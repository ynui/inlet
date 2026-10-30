#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

PIDFILE=.demo.pids

start() {
  make build >/dev/null
  rm -f "$PIDFILE" inletd.log demo-server.log
  token=$(./inletd token create --label dev | awk '/^token:/{print $2}')
  admin_token=$(./inletd token create --label dev-admin | awk '/^token:/{print $2}')
  INLET_ADMIN_TOKEN="$admin_token" nohup ./inletd > inletd.log 2>&1 & echo $! >> "$PIDFILE"
  nohup go run ./scripts/demo-server > demo-server.log 2>&1 & echo $! >> "$PIDFILE"
  ready=0
  for _ in {1..50}; do
    if curl -fsS --max-time 1 http://127.0.0.1:8080/_inlet/healthz >/dev/null; then
      ready=1
      break
    fi
    sleep 0.1
  done
  if [[ "$ready" != 1 ]]; then
    echo "inletd did not start; see ./inletd.log" >&2
    cat inletd.log >&2
    exit 1
  fi
  ./inlet config set-server ws://127.0.0.1:8080 >/dev/null
  ./inlet auth "$token" >/dev/null
  ./inlet-admin auth --server ws://127.0.0.1:8080 "$admin_token" >/dev/null
  ./inlet-admin config show >/dev/null
  echo "inletd public:  http://127.0.0.1:8080"
  echo "inletd control: ws://127.0.0.1:8080/_inlet/control"
  echo "admin:     ./inlet-admin invite create"
  echo "demo app:  http://127.0.0.1:8000"
  echo "logs:      ./inletd.log  ./demo-server.log"
  echo "client:    ./inlet http 8000"
  echo "teardown:  make dev-stop  (or scripts/dev.sh stop)"
}

stop() {
  if [ -f "$PIDFILE" ]; then
    xargs kill < "$PIDFILE" 2>/dev/null || true
    rm -f "$PIDFILE"
  fi
  pkill -f './inletd' 2>/dev/null || true
  pkill -f 'demo-server' 2>/dev/null || true
  echo "stopped"
}

case "${1:-start}" in
  start) start ;;
  stop) stop ;;
  *) echo "usage: $0 [start|stop]"; exit 1 ;;
esac
