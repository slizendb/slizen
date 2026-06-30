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

docker_available() {
  command -v docker >/dev/null 2>&1 &&
    docker compose version >/dev/null 2>&1 &&
    docker info >/dev/null 2>&1
}

check_make_targets_in_docs() {
  local targets
  targets="$(awk -F: '/^[A-Za-z0-9_.-]+:/ { print $1 }' Makefile | sort -u)"
  local refs
  refs="$(grep -RhoE 'make [A-Za-z0-9_.-]+' README.md README.ru.md START_HERE_RU.md AGENT_PROMPT.md docs 2>/dev/null | awk '{ print $2 }' | sort -u || true)"
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

require bash
require go
require awk
require grep
require sed

step "go checks"
make check

step "shell syntax"
bash -n scripts/demo.sh
bash -n scripts/smoke.sh
bash -n scripts/release_check.sh
if [[ -f scripts/demo_report.sh ]]; then
  bash -n scripts/demo_report.sh
fi

step "documentation make targets"
check_make_targets_in_docs

step "release documentation tests"
go test ./internal/release/...

step "Docker smoke"
if docker_available; then
  make smoke
else
  echo "warning: Docker Compose is unavailable; skipping make smoke in release-check" >&2
fi

step "release-check ok"
