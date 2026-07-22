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

release_version() {
  local tag
  tag="$(git describe --tags --exact-match --match 'v[0-9]*' HEAD 2>/dev/null || true)"
  if [[ -n "${tag}" ]]; then
    printf '%s' "${tag#v}"
    return
  fi
  printf 'dev'
}

release_commit() {
  local commit
  commit="$(git rev-parse HEAD 2>/dev/null || true)"
  [[ -n "${commit}" ]] || commit="unknown"
  if [[ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null || true)" ]]; then
    commit="${commit}-dirty"
  fi
  printf '%s' "${commit}"
}

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

compose_up() {
  if [[ -n "${SLIZEN_IMAGE:-}" ]]; then
    if [[ "${SLIZEN_REQUIRE_IMMUTABLE_IMAGE:-0}" == "1" && "${SLIZEN_IMAGE}" != *@sha256:* ]]; then
      echo "SLIZEN_IMAGE must use an immutable sha256 digest for release evidence: ${SLIZEN_IMAGE}" >&2
      return 1
    fi
    docker compose pull slizend
    docker compose up --no-build -d
    return
  fi
  docker compose up --build -d
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

export SLIZEN_VERSION="${SLIZEN_VERSION:-$(release_version)}"
export SLIZEN_COMMIT="${SLIZEN_COMMIT:-$(release_commit)}"

compose_up
wait_ready

curl -fsS "${ADMIN_URL}/v1/status" > "${TMP_DIR}/status-before.json"

go run -ldflags "-X github.com/slizendb/slizen/internal/buildinfo.Version=${SLIZEN_VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${SLIZEN_COMMIT}" ./cmd/slizenctl benchmark hotkey \
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
from datetime import datetime, timezone
from pathlib import Path

tmp = Path(os.environ.get("TMP_DIR", "./tmp"))
benchmark = json.loads((tmp / "slizen-benchmark-result.json").read_text())
before = json.loads((tmp / "status-before.json").read_text())
after = json.loads((tmp / "status-after.json").read_text())
audit = json.loads((tmp / "audit.json").read_text())
commit = os.environ["SLIZEN_COMMIT"]
expected_version = os.environ["SLIZEN_VERSION"]
image = os.environ.get("SLIZEN_IMAGE", "local source build")
version = after.get("version", "unknown")
runtime = benchmark.get("runtime_versions", {})
if version != expected_version or after.get("commit") != commit:
    raise SystemExit(
        f"daemon build metadata mismatch: got {version}/{after.get('commit')}, "
        f"expected {expected_version}/{commit}"
    )
if runtime.get("slizen") != expected_version or runtime.get("slizen_commit") != commit:
    raise SystemExit(
        f"benchmark build metadata mismatch: got {runtime.get('slizen')}/"
        f"{runtime.get('slizen_commit')}, expected {expected_version}/{commit}"
    )
mode = after.get("mode", "unknown")
visibility = after.get("key_visibility", "unknown")
hot = benchmark["phases"][-1]

report = f"""# Slizen {version} Demo Report

Date: {datetime.now(timezone.utc).isoformat()}
Git commit: `{commit}`
Slizen version: `{version}`
Image: `{image}`
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
- origin GET reduction: {benchmark["upstream_get_reduction_percent"]:.1f}%
- proved reduction: {str(benchmark["proved_reduction"]).lower()}
- hot phase ops/sec: {hot["ops_per_second"]:.0f}
- hot phase upstream GETs: {hot["upstream_gets"]}

## Status Delta

- requests total: {before.get("requests_total", 0)} -> {after.get("requests_total", 0)}
- cache hits total: {before.get("cache_hits_total", 0)} -> {after.get("cache_hits_total", 0)}
- upstream GETs total: {before.get("upstream_gets_total", 0)} -> {after.get("upstream_gets_total", 0)}
- retained cache entries: {before.get("cache_entries", 0)} -> {after.get("cache_entries", 0)}
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

- This is one synthetic Docker Compose run, not a universal latency or capacity claim.
- Origin GET reduction does not mean that Slizen was faster than direct Redis or Valkey.
- Redis or Valkey remains the source of truth.
- Slizen is single-node developer-preview software and not production-ready.
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
