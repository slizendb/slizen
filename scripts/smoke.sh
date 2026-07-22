#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-slizen-smoke}"

ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:9090}"
KEY="${KEY:-product:iphone_17}"
VALUE="${VALUE:-{\"name\":\"iPhone 17\",\"price\":999}}"
UPDATED_VALUE="${UPDATED_VALUE:-{\"name\":\"iPhone 17\",\"price\":1000}}"
ADMISSION_KEY="${ADMISSION_KEY:-product:slizen:two-hit-smoke}"
ADMISSION_VALUE="${ADMISSION_VALUE:-two-hit-value}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

cleanup() {
  docker compose down --remove-orphans >/dev/null 2>&1 || true
}

wait_ready() {
  for _ in $(seq 1 60); do
    if curl -fsS "${ADMIN_URL}/readyz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Slizen did not become ready" >&2
  docker compose ps >&2 || true
  docker compose logs >&2 || true
  return 1
}

valkey_cli() {
  docker compose exec -T valkey valkey-cli -h slizend -p 6380 "$@"
}

upstream_gets() {
  curl -fsS "${ADMIN_URL}/metrics" |
    awk '/slizen_upstream_requests_total\{command="GET"\}/ { print $2; found = 1 } END { if (!found) print 0 }'
}

require docker
require curl
require awk

trap cleanup EXIT
cleanup
docker compose up --build -d
wait_ready

curl -fsS "${ADMIN_URL}/healthz" >/dev/null
curl -fsS "${ADMIN_URL}/readyz" >/dev/null
status="$(curl -fsS "${ADMIN_URL}/v1/status")"
metrics="$(curl -fsS "${ADMIN_URL}/metrics")"

if grep -q 'demo-secret' <<<"${status}"; then
  echo "status leaked key hash secret" >&2
  exit 1
fi

valkey_cli SET "${KEY}" "${VALUE}" >/dev/null
got="$(valkey_cli GET "${KEY}")"
if [[ "${got}" != "${VALUE}" ]]; then
  echo "unexpected GET value after SET: ${got}" >&2
  exit 1
fi

valkey_cli SET "${ADMISSION_KEY}" "${ADMISSION_VALUE}" >/dev/null
first_admission_get="$(valkey_cli GET "${ADMISSION_KEY}")"
upstream_after_first_admission_get="$(upstream_gets)"
second_admission_get="$(valkey_cli GET "${ADMISSION_KEY}")"
upstream_after_second_admission_get="$(upstream_gets)"
if [[ "${first_admission_get}" != "${ADMISSION_VALUE}" || "${second_admission_get}" != "${ADMISSION_VALUE}" ]]; then
  echo "two-hit admission returned an unexpected value" >&2
  exit 1
fi
if [[ "${upstream_after_first_admission_get}" != "${upstream_after_second_admission_get}" ]]; then
  echo "second admission GET reached the upstream" >&2
  exit 1
fi

docker compose exec -T slizend /usr/local/bin/slizenctl status --admin http://127.0.0.1:9090 >/dev/null
docker compose exec -T slizend /usr/local/bin/slizenctl healthz --admin http://127.0.0.1:9090 >/dev/null
docker compose exec -T slizend /usr/local/bin/slizenctl readyz --admin http://127.0.0.1:9090 >/dev/null

docker compose exec -T slizend /usr/local/bin/slizenctl demo black-friday \
  --redis 127.0.0.1:6380 \
  --admin http://127.0.0.1:9090 \
  --key "${KEY}" \
  --workers 8 \
  --duration 5s

docker compose exec -T slizend /usr/local/bin/slizenctl hotkeys --admin http://127.0.0.1:9090 >/tmp/slizen-hotkeys.json
hotkeys="$(cat /tmp/slizen-hotkeys.json)"
if grep -Eq 'iphone_17|product:iphone' <<<"${hotkeys}"; then
  echo "hotkeys leaked raw key" >&2
  exit 1
fi
if ! grep -q 'hmac-sha256:' <<<"${hotkeys}"; then
  echo "hotkeys did not expose HMAC key identifier" >&2
  exit 1
fi

docker compose exec -T slizend /usr/local/bin/slizenctl audit --admin http://127.0.0.1:9090 >/tmp/slizen-audit.json
audit="$(cat /tmp/slizen-audit.json)"
if grep -Eq 'iphone_17|product:iphone|demo-secret' <<<"${audit}"; then
  echo "audit leaked a raw key or secret" >&2
  exit 1
fi
if ! grep -q 'slizen.audit.v1' <<<"${audit}" || ! grep -q 'reason_codes' <<<"${audit}" || ! grep -q 'hmac-sha256:' <<<"${audit}" || ! grep -q '"capacity_observations_dropped": 0' <<<"${audit}" || ! grep -q '"telemetry_complete": true' <<<"${audit}"; then
  echo "audit did not contain the expected bounded report fields" >&2
  exit 1
fi

metrics="$(curl -fsS "${ADMIN_URL}/metrics")"
if grep -Eq 'iphone_17|product:iphone' <<<"${metrics}"; then
  echo "metrics leaked raw key" >&2
  exit 1
fi
awk '/slizen_cache_hits_total\{command="GET"\}/ { if ($2 + 0 > 0) found = 1 } END { exit found ? 0 : 1 }' <<<"${metrics}" || {
  echo "expected cache hits after demo" >&2
  exit 1
}
for reason in policy_bypass not_admitted not_present unclassified; do
  if ! grep -q "slizen_cache_miss_reasons_total{reason=\"${reason}\"}" <<<"${metrics}"; then
    echo "missing bounded cache miss reason metric: ${reason}" >&2
    exit 1
  fi
done
if ! grep -q '^slizen_hotness_capacity_observations_dropped_total ' <<<"${metrics}"; then
  echo "missing hotness capacity-drop metric" >&2
  exit 1
fi

upstream_before_set="$(upstream_gets)"
valkey_cli SET "${KEY}" "${UPDATED_VALUE}" >/dev/null
got="$(valkey_cli GET "${KEY}")"
upstream_after_set_get="$(upstream_gets)"
if [[ "${got}" != "${UPDATED_VALUE}" ]]; then
  echo "SET through Slizen did not refresh the admitted cached value: ${got}" >&2
  exit 1
fi
if [[ "${upstream_before_set}" != "${upstream_after_set_get}" ]]; then
  echo "GET after exact SET did not use the write-through value" >&2
  exit 1
fi

unsupported="$(valkey_cli MULTI 2>&1 || true)"
if ! grep -qi 'stateful or unsafe' <<<"${unsupported}"; then
  echo "unsupported command did not return expected RESP error: ${unsupported}" >&2
  exit 1
fi

docker compose stop -t 15 slizend >/dev/null
echo "smoke ok"
