# 30-minute staging observe install

This is the shortest supported path to a ready, unrouted standalone Slizen Pod
in Kubernetes. The target is no more than 30 minutes for an operator unfamiliar
with Slizen. It proves installation and runtime identity; it is not permission
to route application traffic or enable caching.

Use the full [staging rollout](STAGING_ROLLOUT.md) before routing. It adds the
application compatibility/ACL checks, allowed/denied network proof,
observability, thresholds, failure drills, soak, and measured rollback required
for a staging Pass.

## Stop before starting if

- the origin requires TLS, Redis Cluster redirections, or Sentinel discovery;
- the application must send downstream `AUTH`, TLS, RESP3 `HELLO`, or another
  unsupported initialization command to the new endpoint;
- the cluster has no enforced NetworkPolicy or reviewed equivalent;
- the origin is not an isolated staging Redis/Valkey endpoint.

Slizen v0.2 supports one plaintext standalone origin. Redis or Valkey remains
the source of truth.

## 1. Record the timer and identities

Run the complete quickstart in one Bash session from a clean, pushed Slizen
checkout:

```sh
bash
set -Eeuo pipefail
export INSTALL_STARTED_AT="$(date +%s)"

export SLIZEN_NAMESPACE=slizen-staging
export SLIZEN_RELEASE=slizen
export SLIZEN_DEPLOYMENT=slizen
export ORIGIN_ADDRESS=redis.default.svc.cluster.local:6379
export STABLE_VERSION=0.2.2
export STABLE_COMMIT=74a12767deb72db9bc78bebd807cbe8717fa572c
export STABLE_DIGEST=sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627

command -v kubectl >/dev/null
command -v helm >/dev/null
command -v jq >/dev/null
test -z "$(git status --porcelain --untracked-files=all)"
export CHART_COMMIT="$(git rev-parse HEAD)"
git fetch --prune origin
test -n "$(git branch -r --contains "$CHART_COMMIT")"
kubectl get namespace default >/dev/null
export HELM_VERSION="$(helm version --template '{{.Version}}')"
printf 'helm_version=%s\n' "$HELM_VERSION"
test "$HELM_VERSION" = v3.18.4 ||
  test "$HELM_VERSION" = "${REVIEWED_HELM_VERSION:-}"
```

The runtime and deployment source are separate identities: the current chart
commit deliberately deploys the last verified public runtime digest until
v0.2.3 is published. Only set `REVIEWED_HELM_VERSION` before this session when
the platform owner has reviewed that exact version's `--atomic`, wait, and
timeout behavior. Otherwise the executable check stops the install.

## 2. Prepare one reviewed values file

Copy [`charts/slizen/examples/staging-values.yaml`](../charts/slizen/examples/staging-values.yaml)
into the team's configuration repository. Replace all example namespaces, Pod
labels, and the origin address. The application peer must identify only the
intended canary Pods.

The example describes the full staging path. This quickstart applies a
fail-closed command-line overlay: the admin API stays on Pod loopback, metrics
remain disabled, and only one temporary, exact network-test Pod identity may
reach RESP. The full runbook later replaces that identity with the exact canary
application and enables continuous metrics only together with its own
allowed/denied proof. Do not expose port 9090 merely to finish this quick path:
all admin routes share that listener.

If the origin authenticates, set `upstream.existingSecret.name` and its two key
names. Create that namespaced Secret through the platform secret manager; do
not put credentials in values or shell history. Use an empty username value for
Redis's default user. The rollout operator does not need Secret-read access.

Set the reviewed file path:

```sh
export REVIEWED_VALUES=/path/to/reviewed/slizen-staging-values.yaml
test -s "$REVIEWED_VALUES"

export QUICKSTART_REDIS_PEERS="$(
  jq -cn --arg namespace "$SLIZEN_NAMESPACE" '
    [{
      namespaceSelector: {
        matchLabels: {"kubernetes.io/metadata.name": $namespace}
      },
      podSelector: {
        matchLabels: {"slizen.dev/network-test": "allowed"}
      }
    }]
  '
)"
QUICKSTART_HELM_OVERRIDES=(
  --set-string "upstream.address=$ORIGIN_ADDRESS"
  --set-string mode=observe
  --set-string "image.digest=$STABLE_DIGEST"
  --set-string admin.listen=127.0.0.1:9090
  --set admin.allowNetworkAccess=false
  --set metrics.enabled=false
  --set metrics.serviceMonitor.enabled=false
  --set networkPolicy.enabled=true
  --set-json "networkPolicy.redisIngressPeers=$QUICKSTART_REDIS_PEERS"
  --set-json 'networkPolicy.metricsIngressPeers=[]'
)
```

## 3. Validate, render, and install

The commands below require the chart's tested Helm 3 behavior. If the platform
does not use Helm 3.18.4, record its reviewed `--atomic`, wait, and timeout
behavior before the change window.

```sh
helm lint ./charts/slizen \
  -f "$REVIEWED_VALUES" \
  "${QUICKSTART_HELM_OVERRIDES[@]}"

if ! kubectl get namespace "$SLIZEN_NAMESPACE" >/dev/null 2>&1; then
  kubectl create namespace "$SLIZEN_NAMESPACE"
fi

helm template "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" \
  -f "$REVIEWED_VALUES" \
  "${QUICKSTART_HELM_OVERRIDES[@]}" |
  kubectl apply --dry-run=server -n "$SLIZEN_NAMESPACE" -f -

helm upgrade --install "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" \
  --atomic --timeout 5m \
  -f "$REVIEWED_VALUES" \
  "${QUICKSTART_HELM_OVERRIDES[@]}"

kubectl rollout status deployment/"$SLIZEN_DEPLOYMENT" \
  -n "$SLIZEN_NAMESPACE" --timeout=2m
```

## 4. Prove the network boundary and live runtime

An applied NetworkPolicy is not proof that the CNI enforces it. Use one
temporary Pod for both halves of a controlled test: it reaches the exact
ClusterIP while carrying the allowed label, then must lose access after only
that label changes. The quick path stops if a namespaced egress policy could
confound the result; use the full runbook for platform-specific or cluster-wide
policy proof.

```sh
export SLIZEN_SERVICE_IP="$(
  kubectl get service "$SLIZEN_RELEASE" -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.spec.clusterIP}'
)"
test -n "$SLIZEN_SERVICE_IP"
test "$(
  kubectl get networkpolicy -n "$SLIZEN_NAMESPACE" -o json |
    jq '[.items[] | select((.spec.policyTypes // []) | index("Egress"))] | length'
)" -eq 0

export NETWORK_TEST_IMAGE=redis:7-alpine@sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99
export NETWORK_PROBE_OVERRIDES="$(
  jq -cn --arg image "$NETWORK_TEST_IMAGE" '
    {
      apiVersion: "v1",
      spec: {
        automountServiceAccountToken: false,
        securityContext: {
          runAsNonRoot: true,
          runAsUser: 999,
          runAsGroup: 999,
          seccompProfile: {type: "RuntimeDefault"}
        },
        containers: [{
          name: "slizen-network-probe",
          image: $image,
          command: ["sleep", "300"],
          securityContext: {
            allowPrivilegeEscalation: false,
            readOnlyRootFilesystem: true,
            capabilities: {drop: ["ALL"]}
          }
        }]
      }
    }
  '
)"
cleanup_quickstart_probe() {
  kubectl delete pod slizen-network-probe -n "$SLIZEN_NAMESPACE" \
    --ignore-not-found --wait=false >/dev/null
}
trap cleanup_quickstart_probe EXIT HUP INT TERM

kubectl run slizen-network-probe -n "$SLIZEN_NAMESPACE" \
  --restart=Never \
  --labels='slizen.dev/network-test=allowed' \
  --image="$NETWORK_TEST_IMAGE" \
  --overrides="$NETWORK_PROBE_OVERRIDES"
kubectl wait pod/slizen-network-probe -n "$SLIZEN_NAMESPACE" \
  --for=condition=Ready --timeout=60s
kubectl exec -n "$SLIZEN_NAMESPACE" slizen-network-probe -- \
  sh -c 'test "$(timeout 5 redis-cli -h "$1" -p "$2" PING)" = PONG' \
  probe "$SLIZEN_SERVICE_IP" 6380

kubectl label pod slizen-network-probe -n "$SLIZEN_NAMESPACE" \
  slizen.dev/network-test=denied --overwrite
export DENIAL_DEADLINE="$(( $(date +%s) + 30 ))"
export CONSECUTIVE_DENIALS=0
while test "$(date +%s)" -lt "$DENIAL_DEADLINE"; do
  export DENIAL_SAMPLE="$(
    kubectl exec -n "$SLIZEN_NAMESPACE" slizen-network-probe -- \
      sh -c '
        response="$(timeout 2 redis-cli -h "$1" -p "$2" PING 2>&1)"
        rc=$?
        printf "redis_exit=%s\n%s\n" "$rc" "$response"
        exit 0
      ' probe "$SLIZEN_SERVICE_IP" 6380
  )"
  export REDIS_EXIT="$(
    printf '%s\n' "$DENIAL_SAMPLE" |
      awk -F= '$1 == "redis_exit" { print $2; exit }'
  )"
  case "$REDIS_EXIT" in
    ''|*[!0-9]*)
      printf '%s\n' 'NO-GO: denied probe produced no trustworthy exit code' >&2
      exit 1
      ;;
  esac
  if test "$REDIS_EXIT" -eq 0; then
    export CONSECUTIVE_DENIALS=0
  else
    export CONSECUTIVE_DENIALS="$((CONSECUTIVE_DENIALS + 1))"
    if test "$CONSECUTIVE_DENIALS" -ge 3; then
      break
    fi
  fi
  sleep 1
done
if test "$CONSECUTIVE_DENIALS" -lt 3; then
  printf '%s\n' 'NO-GO: unlisted Pod still reaches the RESP Service' >&2
  exit 1
fi
test "$(
  kubectl get pod slizen-network-probe -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
)" = True

export LIVE_IMAGE="$(
  kubectl get deployment "$SLIZEN_DEPLOYMENT" -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)"
test "$LIVE_IMAGE" = "ghcr.io/slizendb/slizen@$STABLE_DIGEST"

export SLIZEN_POD="$(
  kubectl get pod -n "$SLIZEN_NAMESPACE" \
    -l app.kubernetes.io/instance="$SLIZEN_RELEASE" \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- \
  slizenctl readyz
export RUNTIME_STATUS="$(
  kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- \
    slizenctl status
)"
printf '%s\n' "$RUNTIME_STATUS" |
  jq -e --arg version "$STABLE_VERSION" --arg commit "$STABLE_COMMIT" '
    .version == $version
    and .commit == $commit
    and .mode == "observe"
    and .upstream_status == "up"
    and .cache_entries == 0
    and .cache_hits == 0
  '

kubectl delete pod slizen-network-probe -n "$SLIZEN_NAMESPACE" \
  --wait --timeout=60s
trap - EXIT HUP INT TERM

export INSTALL_SECONDS="$(( $(date +%s) - INSTALL_STARTED_AT ))"
printf 'observe install seconds=%s chart_commit=%s image=%s\n' \
  "$INSTALL_SECONDS" "$CHART_COMMIT" "$LIVE_IMAGE"
test "$INSTALL_SECONDS" -le 1800
```

At this point Slizen is ready but no application should be routed. A first-time
operator records Pass only when the command finishes within 30 minutes without
maintainer help. Otherwise record Partial or Fail with the blocking step.

## 5. Continue or remove

Before routing even one canary, continue at compatibility step 2 of the full
[staging rollout](STAGING_ROLLOUT.md). Replace the temporary probe peer with the
exact canary identity, re-run the allowed/denied proof using the real
application smoke, establish continuous metrics, and capture the application's
exact rollback source.

If no application was routed, removal is:

```sh
helm uninstall "$SLIZEN_RELEASE" \
  --namespace "$SLIZEN_NAMESPACE" --timeout 5m
```

If any application was routed, never uninstall first. Restore the complete
direct-origin application profile, prove direct health in less than five
minutes, and only then remove Slizen as specified by the full runbook.

For a sidecar evaluation, use the
[raw sidecar guide](../deploy/kubernetes/README.md); Helm cannot inject a
sidecar.
