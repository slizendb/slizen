#!/usr/bin/env bash
set -euo pipefail

ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:9090}"
DIRECT_REDIS="${DIRECT_REDIS:-127.0.0.1}"
DIRECT_PORT="${DIRECT_PORT:-6379}"
SLIZEN_REDIS="${SLIZEN_REDIS:-127.0.0.1}"
SLIZEN_PORT="${SLIZEN_PORT:-6380}"
KEY="${KEY:-product:iphone_17}"
REQUESTS="${REQUESTS:-10000}"
CLIENTS="${CLIENTS:-50}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

run_case() {
  local name="$1"
  local host="$2"
  local port="$3"
  echo
  echo "== ${name} =="
  redis-benchmark -h "${host}" -p "${port}" -n "${REQUESTS}" -c "${CLIENTS}" -q GET "${KEY}"
}

require redis-cli
require redis-benchmark
require curl

redis-cli -p "${SLIZEN_PORT}" SET "${KEY}" '{"name":"iPhone 17","price":999}' >/dev/null

start_status="$(curl -fsS "${ADMIN_URL}/v1/status")"
run_case "direct Valkey" "${DIRECT_REDIS}" "${DIRECT_PORT}"
run_case "Slizen before or during promotion" "${SLIZEN_REDIS}" "${SLIZEN_PORT}"
sleep 2
run_case "Slizen after promotion window" "${SLIZEN_REDIS}" "${SLIZEN_PORT}"
end_status="$(curl -fsS "${ADMIN_URL}/v1/status")"

echo
echo "start status:"
echo "${start_status}"
echo
echo "end status:"
echo "${end_status}"
