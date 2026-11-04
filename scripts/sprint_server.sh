#!/usr/bin/env bash
# ==============================================================================
# LionSQL Dedicated Dogfooding Sprint Tracker Server (scripts/sprint_server.sh)
#
# Runs an isolated LionSQL instance for sprint task tracking:
#   - Data directory: data/sprint_db (Persistent, separated from benchmarks)
#   - Web Studio / HTTP: http://127.0.0.1:8090
#   - Wire protocols: Disabled (-pg="" -tds="") for zero port conflicts
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
BIN_PATH="${ROOT_DIR}/bin/lionsql"
DATA_DIR="${ROOT_DIR}/data/sprint_db"
PID_FILE="${DATA_DIR}/sprint_server.pid"
LOG_FILE="${DATA_DIR}/sprint_server.log"

HTTP_ADDR="127.0.0.1:8090"

mkdir -p "${DATA_DIR}"

ensure_binary() {
  if [ ! -f "${BIN_PATH}" ]; then
    echo "▶ Building lionsql binary..."
    go build -tags wiredtiger -buildvcs=false -o "${BIN_PATH}" "${ROOT_DIR}/cmd/lionsql"
  fi
}

is_running() {
  if [ -f "${PID_FILE}" ]; then
    local pid
    pid="$(cat "${PID_FILE}" 2>/dev/null || true)"
    if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
      return 0
    fi
  fi
  return 1
}

start_server() {
  if is_running; then
    local pid
    pid="$(cat "${PID_FILE}")"
    echo "LionSQL Sprint Server is already running (PID: ${pid}) at http://${HTTP_ADDR}/_/"
    return 0
  fi

  ensure_binary

  echo "▶ Starting LionSQL Sprint Server on http://${HTTP_ADDR}/_/ (data: ${DATA_DIR})..."
  nohup "${BIN_PATH}" \
    -dir="${DATA_DIR}" \
    -http="${HTTP_ADDR}" \
    -pg="" \
    -tds="" \
    -engine=memory \
    > "${LOG_FILE}" 2>&1 &

  local new_pid=$!
  echo "${new_pid}" > "${PID_FILE}"

  # Wait up to 5 seconds for HTTP to respond
  local attempts=0
  while [ "${attempts}" -lt 25 ]; do
    if curl -s "http://${HTTP_ADDR}/health" >/dev/null 2>&1 || curl -s "http://${HTTP_ADDR}/" >/dev/null 2>&1; then
      echo "✔ LionSQL Sprint Server started successfully (PID: ${new_pid})!"
      echo "  - Web Studio: http://${HTTP_ADDR}/_/"
      echo "  - Logs:       ${LOG_FILE}"
      return 0
    fi
    sleep 0.2
    attempts=$((attempts + 1))
  done

  echo "✔ Server process spawned (PID: ${new_pid}). Ready."
}

stop_server() {
  if [ -f "${PID_FILE}" ]; then
    local pid
    pid="$(cat "${PID_FILE}" 2>/dev/null || true)"
    if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
      echo "▶ Stopping LionSQL Sprint Server (PID: ${pid})..."
      kill "${pid}" 2>/dev/null || true
      sleep 0.5
      if kill -0 "${pid}" 2>/dev/null; then
        kill -9 "${pid}" 2>/dev/null || true
      fi
    fi
    rm -f "${PID_FILE}"
    echo "✔ LionSQL Sprint Server stopped."
  else
    echo "LionSQL Sprint Server is not running."
  fi
}

status_server() {
  if is_running; then
    local pid
    pid="$(cat "${PID_FILE}")"
    echo "✔ LionSQL Sprint Server is ACTIVE (PID: ${pid})"
    echo "  - Web Studio: http://${HTTP_ADDR}/_/"
    echo "  - Storage:    ${DATA_DIR}"
  else
    echo "✖ LionSQL Sprint Server is INACTIVE."
  fi
}

case "${1:-start}" in
  start)
    start_server
    ;;
  stop)
    stop_server
    ;;
  restart)
    stop_server
    start_server
    ;;
  status)
    status_server
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac
