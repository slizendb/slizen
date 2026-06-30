#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

docker compose up --build -d

echo "waiting for Slizen readiness..."
for _ in $(seq 1 60); do
  if curl -fsS http://127.0.0.1:9090/readyz >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

curl -fsS http://127.0.0.1:9090/readyz >/dev/null

redis-cli -p 6380 SET product:iphone_17 '{"name":"iPhone 17","price":999}' >/dev/null

go run ./cmd/slizenctl demo black-friday \
  --redis 127.0.0.1:6380 \
  --admin http://127.0.0.1:9090 \
  --key product:iphone_17 \
  --workers 100 \
  --duration 20s

go run ./cmd/slizenctl status --admin http://127.0.0.1:9090

echo
echo "stack is still running for inspection"
echo "shut it down with: docker compose down"
