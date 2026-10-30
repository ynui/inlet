#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

PIDFILE=.demo.pids

start() {
  make build >/dev/null
  rm -f "$PIDFILE" inletd.log demo-server.log
  token=$(./inletd token create --label dev | awk '/^token:/{print $2}')
  nohup ./inletd > inletd.log 2>&1 & echo $! >> "$PIDFILE"
  nohup go run ./scripts/demo-server > demo-server.log 2>&1 & echo $! >> "$PIDFILE"
  sleep 1
  rm -f ~/.config/inlet/config.json
  ./inlet auth "$token" >/dev/null
  echo "inletd public:  http://localhost:8080"
  echo "inletd control: ws://localhost:8081/_inlet/control"
  echo "demo app:  http://localhost:8000"
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