#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:9090}"
PROXY_ADDR="${PROXY_ADDR:-127.0.0.1:6380}"
ORIGIN_ADDR="${ORIGIN_ADDR:-127.0.0.1:6379}"
KEY="${KEY:-product:iphone_17}"
VALUE="${VALUE:-{\"name\":\"iPhone 17\",\"price\":999}}"
BENCHMARK_WARMUP="${BENCHMARK_WARMUP:-3s}"
BENCHMARK_DURATION="${BENCHMARK_DURATION:-5s}"
BENCHMARK_CONCURRENCY="${BENCHMARK_CONCURRENCY:-16}"
BENCHMARK_REQUESTS="${BENCHMARK_REQUESTS:-20000}"
TMP_DIR="${TMP_DIR:-./tmp}"

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

wait_ready() {
  for _ in $(seq 1 60); do
    if curl -fsS "${ADMIN_URL}/readyz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Slizen did not become ready" >&2
  docker compose logs >&2 || true
  return 1
}

require docker
require curl
require git
require go
require python3

if ! docker compose version >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "Docker Compose is required for demo report" >&2
  exit 1
fi

mkdir -p "${TMP_DIR}"
export TMP_DIR

export SLIZEN_COMMIT
SLIZEN_COMMIT="$(git rev-parse --short HEAD)"

docker compose up --build -d
wait_ready

curl -fsS "${ADMIN_URL}/v1/status" > "${TMP_DIR}/status-before.json"

go run -ldflags "-X github.com/slizendb/slizen/internal/buildinfo.Version=dev -X github.com/slizendb/slizen/internal/buildinfo.Commit=${SLIZEN_COMMIT}" ./cmd/slizenctl benchmark hotkey \
  --proxy "${PROXY_ADDR}" \
  --origin "${ORIGIN_ADDR}" \
  --admin "${ADMIN_URL}" \
  --key "${KEY}" \
  --value "${VALUE}" \
  --warmup "${BENCHMARK_WARMUP}" \
  --duration "${BENCHMARK_DURATION}" \
  --concurrency "${BENCHMARK_CONCURRENCY}" \
  --requests "${BENCHMARK_REQUESTS}" \
  --output text \
  --json-file "${TMP_DIR}/slizen-benchmark-result.json"

curl -fsS "${ADMIN_URL}/v1/status" > "${TMP_DIR}/status-after.json"
curl -fsS "${ADMIN_URL}/v1/hotkeys" > "${TMP_DIR}/hotkeys.json"
curl -fsS "${ADMIN_URL}/v1/audit" > "${TMP_DIR}/audit.json"

python3 - <<'PY'
import json
import os
import subprocess
from datetime import datetime, timezone
from pathlib import Path

tmp = Path(os.environ.get("TMP_DIR", "./tmp"))
benchmark = json.loads((tmp / "slizen-benchmark-result.json").read_text())
before = json.loads((tmp / "status-before.json").read_text())
after = json.loads((tmp / "status-after.json").read_text())
audit = json.loads((tmp / "audit.json").read_text())
commit = subprocess.check_output(["git", "rev-parse", "--short", "HEAD"], text=True).strip()
version = after.get("version", "unknown")
mode = after.get("mode", "unknown")
visibility = after.get("key_visibility", "unknown")
hot = benchmark["phases"][-1]

report = f"""# Slizen v0.2 Demo Report

Date: {datetime.now(timezone.utc).isoformat()}
Git commit: `{commit}`
Slizen version: `{version}`
Config mode: `{mode}`
Key visibility: `{visibility}`

## Benchmark Parameters

- key: `{benchmark["key"]}`
- concurrency: {benchmark["concurrency"]}
- duration: {benchmark["duration_seconds"]}s
- warmup: {benchmark["warmup_seconds"]}s
- max requests: {benchmark["max_requests"]}

## Benchmark Result

- cache hit ratio: {benchmark["cache_hit_ratio_percent"]:.1f}%
- upstream GET reduction: {benchmark["upstream_get_reduction_percent"]:.1f}%
- proved reduction: {str(benchmark["proved_reduction"]).lower()}
- hot phase ops/sec: {hot["ops_per_second"]:.0f}
- hot phase upstream GETs: {hot["upstream_gets"]}

## Status Delta

- requests total: {before.get("requests_total", 0)} -> {after.get("requests_total", 0)}
- cache hits total: {before.get("cache_hits_total", 0)} -> {after.get("cache_hits_total", 0)}
- upstream GETs total: {before.get("upstream_gets_total", 0)} -> {after.get("upstream_gets_total", 0)}
- cache entries: {before.get("cache_entries", 0)} -> {after.get("cache_entries", 0)}
- hot keys: {before.get("hot_keys", 0)} -> {after.get("hot_keys", 0)}

## Observe/Audit Evidence

- schema: `{audit.get("schema_version", "unknown")}`
- tracked keys: {audit.get("tracked_keys", 0)}
- returned entries: {audit.get("returned_entries", 0)}
- truncated: {str(audit.get("truncated", False)).lower()}
- telemetry complete: {str(audit.get("telemetry_complete", False)).lower()}
- tracking evictions: {audit.get("tracking_evictions", 0)}
- oversized observations dropped: {audit.get("oversized_observations_dropped", 0)}

## Limitations

- This is a local Docker Compose demonstration, not a scientific benchmark.
- Redis or Valkey remains the source of truth.
- Slizen v0.2 is single-node and not production-ready.
- Results depend on local machine, Docker, Redis/Valkey, and workload shape.

## Repeat

```sh
make demo-report
cat ./tmp/demo-report.md
```

Artifacts:

- `./tmp/slizen-benchmark-result.json`
- `./tmp/status-before.json`
- `./tmp/status-after.json`
- `./tmp/hotkeys.json`
- `./tmp/audit.json`
"""

(tmp / "demo-report.md").write_text(report)
print(tmp / "demo-report.md")
PY
