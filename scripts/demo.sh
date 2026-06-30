#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:9090}"
PROXY_ADDR="${PROXY_ADDR:-127.0.0.1:6380}"
KEY="${KEY:-product:iphone_17}"
VALUE="${VALUE:-{\"name\":\"iPhone 17\",\"price\":999}}"
WORKERS="${WORKERS:-16}"
DURATION="${DURATION:-5s}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

wait_ready() {
  echo "waiting for Slizen readiness..."
  for _ in $(seq 1 60); do
    if curl -fsS "${ADMIN_URL}/readyz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Slizen did not become ready" >&2
  docker compose logs slizend >&2 || true
  return 1
}

require docker
require curl

docker compose up --build -d
wait_ready

curl -fsS "${ADMIN_URL}/healthz"
curl -fsS "${ADMIN_URL}/readyz"
curl -fsS "${ADMIN_URL}/v1/status" >/dev/null
curl -fsS "${ADMIN_URL}/metrics" >/dev/null

docker compose exec -T valkey valkey-cli -h slizend -p 6380 SET "${KEY}" "${VALUE}" >/dev/null
got="$(docker compose exec -T valkey valkey-cli -h slizend -p 6380 GET "${KEY}")"
if [[ "${got}" != "${VALUE}" ]]; then
  echo "unexpected GET value: ${got}" >&2
  exit 1
fi

docker compose exec -T slizend /usr/local/bin/slizenctl status --admin http://127.0.0.1:9090 >/dev/null

docker compose exec -T slizend /usr/local/bin/slizenctl demo black-friday \
  --redis 127.0.0.1:6380 \
  --admin http://127.0.0.1:9090 \
  --key "${KEY}" \
  --workers "${WORKERS}" \
  --duration "${DURATION}"

docker compose exec -T slizend /usr/local/bin/slizenctl hotkeys --admin http://127.0.0.1:9090
docker compose exec -T slizend /usr/local/bin/slizenctl status --admin http://127.0.0.1:9090

echo
echo "stack is still running for inspection"
echo "shut it down with: docker compose down --remove-orphans"
