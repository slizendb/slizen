#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
chart_dir="$repo_dir/charts/slizen"
raw_manifest="$repo_dir/deploy/kubernetes/observe-sidecar.yaml"
helm_bin=${HELM_BIN:-helm}

if ! command -v "$helm_bin" >/dev/null 2>&1; then
  echo "helm is required to validate Kubernetes packaging" >&2
  exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

"$helm_bin" lint "$chart_dir"
"$helm_bin" template slizen "$chart_dir" \
  --namespace slizen-staging >"$tmp_dir/default.yaml"
"$helm_bin" lint "$chart_dir" -f "$chart_dir/ci/cache-values.yaml"
"$helm_bin" template slizen "$chart_dir" \
  --namespace slizen-staging \
  -f "$chart_dir/ci/cache-values.yaml" >"$tmp_dir/cache.yaml"

if "$helm_bin" template slizen "$chart_dir" --set replicaCount=2 >/dev/null 2>&1; then
  echo "chart accepted an unsafe multi-replica v0.2 deployment" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" --set metrics.enabled=true >/dev/null 2>&1; then
  echo "chart exposed admin without explicit acknowledgement" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" --set-string admin.listen=0.0.0.0:9090 >/dev/null 2>&1; then
  echo "chart accepted a non-loopback admin listener without acknowledgement" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" --set hotness.demotionThreshold=0 >/dev/null 2>&1; then
  echo "chart accepted a zero demotion threshold" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" --set hotness.promotionThreshold=20 --set hotness.demotionThreshold=20 >/dev/null 2>&1; then
  echo "chart accepted promotion threshold at or below demotion threshold" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" --set-string cache.maxLocalTTL=9223372037s >/dev/null 2>&1; then
  echo "chart accepted a cache TTL outside the Go duration range" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" \
  --set cache.maxBytes=1024 \
  --set-string 'cache.policies[0].prefix=catalog:' \
  --set-string 'cache.policies[0].mode=cache' \
  --set 'cache.policies[0].maxItemBytes=2048' \
  --set-string 'cache.policies[0].maxLocalTTL=1s' >/dev/null 2>&1; then
  echo "chart accepted a per-policy item limit above cache.maxBytes" >&2
  exit 1
fi
"$helm_bin" template slizen "$chart_dir" \
  --set cache.maxBytes=1024 \
  --set-string cache.maxLocalTTL=1m \
  --set-string 'cache.policies[0].prefix=catalog:' \
  --set-string 'cache.policies[0].mode=cache' \
  --set 'cache.policies[0].maxItemBytes=1024' \
  --set-string 'cache.policies[0].maxLocalTTL=60s' >"$tmp_dir/policy-limit-boundary.yaml"
if "$helm_bin" template slizen "$chart_dir" \
  --set-string cache.maxLocalTTL=1m \
  --set-string 'cache.policies[0].prefix=catalog:' \
  --set-string 'cache.policies[0].mode=cache' \
  --set 'cache.policies[0].maxItemBytes=1024' \
  --set-string 'cache.policies[0].maxLocalTTL=61s' >/dev/null 2>&1; then
  echo "chart accepted a per-policy TTL above cache.maxLocalTTL" >&2
  exit 1
fi
if "$helm_bin" template slizen "$chart_dir" \
  --set-string 'cache.policies[0].prefix=catalog:' \
  --set-string 'cache.policies[0].mode=deny' \
  --set-string 'cache.policies[1].prefix=catalog:' \
  --set-string 'cache.policies[1].mode=observe' >/dev/null 2>&1; then
  echo "chart accepted duplicate policy prefixes" >&2
  exit 1
fi

prefix_tail=$(awk 'BEGIN { for (i = 0; i < 1015; i++) printf "x" }')
total_prefix_values="$tmp_dir/total-prefix-values.yaml"
{
  printf 'cache:\n  policies:\n'
  policy_index=0
  while [ "$policy_index" -lt 257 ]; do
    printf '    - prefix: "%08d:%s"\n      mode: deny\n' "$policy_index" "$prefix_tail"
    policy_index=$((policy_index + 1))
  done
} >"$total_prefix_values"
if "$helm_bin" template slizen "$chart_dir" -f "$total_prefix_values" >/dev/null 2>&1; then
  echo "chart accepted policy prefixes above the total 262144-byte limit" >&2
  exit 1
fi

grep -q 'mode = "observe"' "$tmp_dir/default.yaml"
grep -q 'listen = "127.0.0.1:9090"' "$tmp_dir/default.yaml"
grep -q 'kind: ServiceMonitor' "$tmp_dir/cache.yaml"
grep -q 'kind: Deployment' "$raw_manifest"

if grep -q 'name: .*metrics' "$tmp_dir/default.yaml"; then
  echo "default render unexpectedly exposes metrics/admin" >&2
  exit 1
fi
if grep -Eq 'password[[:space:]]*=|username[[:space:]]*=' "$tmp_dir/default.yaml"; then
  echo "default render contains inline upstream credentials" >&2
  exit 1
fi
if grep -Eq '(max_bytes|max_entries|max_item_bytes|max_tracked_keys) = .*e[+-]' "$tmp_dir/cache.yaml"; then
  echo "rendered TOML contains a floating-point value for an integer limit" >&2
  exit 1
fi

if command -v ruby >/dev/null 2>&1; then
  ruby -e 'require "yaml"; abort "empty Kubernetes manifest" if YAML.load_stream(File.read(ARGV.fetch(0))).compact.empty?' "$raw_manifest"
fi

echo "Kubernetes manifests and Helm chart validated"
