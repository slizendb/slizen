#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

step() {
  printf '\n==> %s\n' "$*"
}

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

check_make_targets_in_docs() {
  local targets
  targets="$(awk -F: '/^[A-Za-z0-9_.-]+:/ { print $1 }' Makefile | sort -u)"
  local refs
  refs="$(grep -RhoE '(^[[:space:]]*|`)make [A-Za-z0-9_.-]+' README.md README.ru.md START_HERE_RU.md AGENT_PROMPT.md docs 2>/dev/null | awk '{ print $2 }' | sort -u || true)"
  local missing=0
  while IFS= read -r ref; do
    [[ -z "${ref}" ]] && continue
    if ! grep -qx "${ref}" <<<"${targets}"; then
      echo "docs reference missing make target: make ${ref}" >&2
      missing=1
    fi
  done <<<"${refs}"
  if [[ "${missing}" -ne 0 ]]; then
    exit 1
  fi
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

wait_ready() {
  local admin_url="$1"
  for _ in $(seq 1 60); do
    if curl -fsS "${admin_url}/readyz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Slizen did not become ready at ${admin_url}" >&2
  docker compose ps >&2 || true
  docker compose logs >&2 || true
  return 1
}

validate_workload_evidence() {
  local result_file="$1"
  local key_prefix="$2"
  local expected_version="$3"
  local expected_commit="$4"
  local expected_request_limit="$5"
  local expected_flash_every="$6"
  jq -e \
    --arg key_prefix "${key_prefix}" \
    --arg expected_version "${expected_version}" \
    --arg expected_commit "${expected_commit}" \
    --argjson expected_request_limit "${expected_request_limit}" \
    --argjson expected_flash_every "${expected_flash_every}" '
    def known_version:
      type == "string" and length > 0 and ascii_downcase != "unknown";
    .name == "Slizen Release Workload Benchmark"
    and .scenario_selection == "all"
    and .key_prefix == $key_prefix
    and (.isolated_key_prefix | type == "string" and startswith($key_prefix + ":run-") and length > ($key_prefix | length))
    and .runtime_versions.slizen == $expected_version
    and .runtime_versions.slizen_commit == $expected_commit
    and .runtime_versions.slizenctl == (
      if $expected_commit == "unknown" then $expected_version
      else $expected_version + " (" + $expected_commit + ")"
      end
    )
    and (.runtime_versions.origin | known_version)
    and .max_requests_per_phase == $expected_request_limit
    and .flash_key_moves_every_operations == $expected_flash_every
    and (.scenarios | type == "array" and length == 4)
    and ((.scenarios | map(.name) | sort) == ["moving-flash", "skew-80-20", "skew-99-1", "uniform"])
    and all(.scenarios[];
      .evidence_valid == true
      and .origin.operation_attempts == $expected_request_limit
      and .origin.termination_reason == "request_limit"
      and (.origin.read_latency.samples + .origin.write_latency.samples) == .origin.operation_attempts
      and .origin.read_ordering_wait_latency.samples == .origin.read_latency.samples
      and .origin.write_ordering_wait_latency.samples == .origin.write_latency.samples
      and .origin.final_validation_latency.samples == .origin.validation_reads
      and .origin.requests == (.origin.operation_attempts + .origin.validation_reads)
      and .slizen.operation_attempts == $expected_request_limit
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
      and (.slizen.cache_hits | type == "number")
      and (.cache_hit_ratio_percent | type == "number")
      and (.origin_get_reduction_percent | type == "number")
    )
    and any(.scenarios[];
      .name == "skew-99-1"
      and .proved_origin_get_reduction == true
      and .slizen.cache_hits > 0
      and .cache_hit_ratio_percent > 0
      and .origin_get_reduction_percent > 0
    )
  ' "${result_file}" >/dev/null || {
    echo "release workload evidence did not satisfy the v0.2 gate: ${result_file}" >&2
    return 1
  }
}

cleanup_release_stack_best_effort() {
  docker compose down --remove-orphans >/dev/null 2>&1 || true
}

cleanup_release_stack() {
  docker compose down --remove-orphans >/dev/null
}

require bash
require go
require awk
require curl
require docker
require git
require grep
require jq
require sed
require "${HELM_BIN:-helm}"

docker compose version >/dev/null 2>&1 || {
  echo "Docker Compose is required for release-check" >&2
  exit 1
}
docker info >/dev/null 2>&1 || {
  echo "the Docker daemon is unavailable" >&2
  exit 1
}

export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-slizen-release-check-$$}"
export SLIZEN_VERSION="${SLIZEN_VERSION:-0.2.2}"
export SLIZEN_COMMIT="${SLIZEN_COMMIT:-$(release_commit)}"
export SLIZEN_VALKEY_PORT="${SLIZEN_VALKEY_PORT:-16379}"
export SLIZEN_PROXY_PORT="${SLIZEN_PROXY_PORT:-16380}"
export SLIZEN_ADMIN_PORT="${SLIZEN_ADMIN_PORT:-19090}"
export ADMIN_URL="${ADMIN_URL:-http://127.0.0.1:${SLIZEN_ADMIN_PORT}}"
PROXY_ADDR="${PROXY_ADDR:-127.0.0.1:${SLIZEN_PROXY_PORT}}"
ORIGIN_ADDR="${ORIGIN_ADDR:-127.0.0.1:${SLIZEN_VALKEY_PORT}}"
WORKLOAD_KEY_PREFIX="product:slizen:release:v${SLIZEN_VERSION}"
WORKLOAD_RESULT="./tmp/slizen-workload-result.json"
WORKLOAD_REQUESTS=100000
WORKLOAD_DURATION=30s
WORKLOAD_FLASH_EVERY=20000
trap cleanup_release_stack_best_effort EXIT

step "go checks"
make check

step "shell syntax"
bash -n scripts/demo.sh
bash -n scripts/smoke.sh
bash -n scripts/release_check.sh
bash -n scripts/validate_k8s.sh
if [[ -f scripts/demo_report.sh ]]; then
  bash -n scripts/demo_report.sh
fi
if [[ -f scripts/release_evidence.sh ]]; then
  bash -n scripts/release_evidence.sh
fi

step "documentation make targets"
check_make_targets_in_docs

step "release documentation tests"
go test ./internal/release/...

step "Kubernetes packaging"
make validate-k8s

step "Docker smoke"
make smoke

step "release workload evidence"
make demo-up
wait_ready "${ADMIN_URL}"
mkdir -p ./tmp
go run -ldflags "-X github.com/slizendb/slizen/internal/buildinfo.Version=${SLIZEN_VERSION} -X github.com/slizendb/slizen/internal/buildinfo.Commit=${SLIZEN_COMMIT}" ./cmd/slizenctl benchmark workload \
  --proxy "${PROXY_ADDR}" \
  --origin "${ORIGIN_ADDR}" \
  --admin "${ADMIN_URL}" \
  --scenario all \
  --key-prefix "${WORKLOAD_KEY_PREFIX}" \
  --keys 1000 \
  --value-size 128 \
  --read-ratio 95 \
  --concurrency 32 \
  --duration "${WORKLOAD_DURATION}" \
  --requests "${WORKLOAD_REQUESTS}" \
  --seed 42 \
  --flash-every "${WORKLOAD_FLASH_EVERY}" \
  --output text \
  --json-file "${WORKLOAD_RESULT}"
validate_workload_evidence "${WORKLOAD_RESULT}" "${WORKLOAD_KEY_PREFIX}" "${SLIZEN_VERSION}" "${SLIZEN_COMMIT}" "${WORKLOAD_REQUESTS}" "${WORKLOAD_FLASH_EVERY}"
cleanup_release_stack
trap - EXIT

step "release-check ok"
