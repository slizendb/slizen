#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

: "${SLIZEN_IMAGE:?set SLIZEN_IMAGE to ghcr.io/slizendb/slizen@sha256:...}"
: "${SLIZEN_VERSION:?set SLIZEN_VERSION to the tag version without v}"
: "${SLIZEN_COMMIT:?set SLIZEN_COMMIT to the full release commit}"

case "${SLIZEN_IMAGE}" in
  *@sha256:*) ;;
  *)
    echo "SLIZEN_IMAGE must use an immutable sha256 digest: ${SLIZEN_IMAGE}" >&2
    exit 1
    ;;
esac

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

cleanup() {
  docker compose down --remove-orphans >/dev/null 2>&1 || true
}

require docker
require go
require jq

checksum_files() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$@"
    return
  fi
  echo "missing required command: sha256sum or shasum" >&2
  return 1
}

export SLIZEN_REQUIRE_IMMUTABLE_IMAGE=1
export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-slizen-release-evidence-$$}"
export SLIZEN_VALKEY_PORT="${SLIZEN_VALKEY_PORT:-16379}"
export SLIZEN_PROXY_PORT="${SLIZEN_PROXY_PORT:-16380}"
export SLIZEN_ADMIN_PORT="${SLIZEN_ADMIN_PORT:-19090}"
export ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:${SLIZEN_ADMIN_PORT}}"
export PROXY_ADDR="${PROXY_ADDR:-127.0.0.1:${SLIZEN_PROXY_PORT}}"
export ORIGIN_ADDR="${ORIGIN_ADDR:-127.0.0.1:${SLIZEN_VALKEY_PORT}}"
export TMP_DIR="${TMP_DIR:-./tmp}"
trap cleanup EXIT

./scripts/demo_report.sh

workload_result="${TMP_DIR}/slizen-workload-result.json"
workload_prefix="product:slizen:release:v${SLIZEN_VERSION}"
release_workload_requests=100000
release_workload_duration=30s
release_workload_flash_every=20000

go run -ldflags "-X github.com/slizendb/slizen/internal/buildinfo.Version=${SLIZEN_VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${SLIZEN_COMMIT}" ./cmd/slizenctl benchmark workload \
  --proxy "${PROXY_ADDR}" \
  --origin "${ORIGIN_ADDR}" \
  --admin "${ADMIN_URL}" \
  --scenario all \
  --key-prefix "${workload_prefix}" \
  --keys 1000 \
  --value-size 128 \
  --read-ratio 95 \
  --concurrency 32 \
  --duration "${release_workload_duration}" \
  --requests "${release_workload_requests}" \
  --seed 42 \
  --flash-every "${release_workload_flash_every}" \
  --output text \
  --json-file "${workload_result}"

jq -e \
  --arg prefix "${workload_prefix}" \
  --arg version "${SLIZEN_VERSION}" \
  --arg commit "${SLIZEN_COMMIT}" \
  --argjson request_limit "${release_workload_requests}" \
  --argjson flash_every "${release_workload_flash_every}" '
  .name == "Slizen Release Workload Benchmark"
  and .scenario_selection == "all"
  and .key_prefix == $prefix
  and (.isolated_key_prefix | startswith($prefix + ":run-"))
  and .runtime_versions.slizen == $version
  and .runtime_versions.slizen_commit == $commit
  and .runtime_versions.slizenctl == ($version + " (" + $commit + ")")
  and .max_requests_per_phase == $request_limit
  and .flash_key_moves_every_operations == $flash_every
  and (.scenarios | type == "array" and length == 4)
  and ((.scenarios | map(.name) | sort) == ["moving-flash", "skew-80-20", "skew-99-1", "uniform"])
  and all(.scenarios[];
    .evidence_valid == true
    and .origin.operation_attempts == $request_limit
    and .origin.termination_reason == "request_limit"
    and (.origin.read_latency.samples + .origin.write_latency.samples) == .origin.operation_attempts
    and .origin.read_ordering_wait_latency.samples == .origin.read_latency.samples
    and .origin.write_ordering_wait_latency.samples == .origin.write_latency.samples
    and .origin.final_validation_latency.samples == .origin.validation_reads
    and .origin.requests == (.origin.operation_attempts + .origin.validation_reads)
    and .slizen.operation_attempts == $request_limit
    and .slizen.termination_reason == "request_limit"
    and (.slizen.read_latency.samples + .slizen.write_latency.samples) == .slizen.operation_attempts
    and .slizen.read_ordering_wait_latency.samples == .slizen.read_latency.samples
    and .slizen.write_ordering_wait_latency.samples == .slizen.write_latency.samples
    and .slizen.final_validation_latency.samples == .slizen.validation_reads
    and .slizen.requests == (.slizen.operation_attempts + .slizen.validation_reads)
    and .origin.value_mismatches == 0
    and .slizen.value_mismatches == 0
    and .origin.validation_failures == 0
    and .slizen.validation_failures == 0
    and .origin.validation_mismatches == 0
    and .slizen.validation_mismatches == 0
    and (.slizen.cache_misses | type == "number")
    and (.slizen.cache_misses_policy_bypass | type == "number")
    and (.slizen.cache_misses_not_admitted | type == "number")
    and (.slizen.cache_misses_not_present | type == "number")
    and .slizen.cache_misses == (
      .slizen.cache_misses_policy_bypass
      + .slizen.cache_misses_not_admitted
      + .slizen.cache_misses_not_present
    )
  )
  and any(.scenarios[];
    .name == "skew-99-1"
    and .proved_origin_get_reduction == true
    and .slizen.cache_hits > 0
    and .origin_get_reduction_percent > 0
  )
' "${workload_result}" >/dev/null

image_digest="${SLIZEN_IMAGE##*@}"
generated_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n \
  --arg schema_version "slizen.release-evidence.v1" \
  --arg generated_at "${generated_at}" \
  --arg version "${SLIZEN_VERSION}" \
  --arg commit "${SLIZEN_COMMIT}" \
  --arg image "${SLIZEN_IMAGE}" \
  --arg image_digest "${image_digest}" \
  '{
    schema_version: $schema_version,
    generated_at: $generated_at,
    version: $version,
    commit: $commit,
    image: $image,
    image_digest: $image_digest,
    evidence: [
      "demo-report.md",
      "slizen-benchmark-result.json",
      "slizen-workload-result.json",
      "status-before.json",
      "status-after.json",
      "hotkeys.json",
      "audit.json"
    ]
  }' > "${TMP_DIR}/release-evidence-manifest.json"

(
  cd "${TMP_DIR}"
  checksum_files \
    audit.json \
    demo-report.md \
    hotkeys.json \
    release-evidence-manifest.json \
    slizen-benchmark-result.json \
    slizen-workload-result.json \
    status-after.json \
    status-before.json > SHA256SUMS
)

echo "release evidence verified for ${SLIZEN_IMAGE}"
echo "artifacts: ${TMP_DIR}"
