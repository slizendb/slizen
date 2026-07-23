# Staging rollout and rollback

This runbook is the executable path from direct Redis/Valkey to Slizen
`observe`, one cached prefix, a canary, and gradual expansion. Redis or Valkey
remains the source of truth throughout the trial. Slizen keeps no durable
state, so removal requires no data migration.

The stable public image is currently v0.2.2:

```text
tag:    v0.2.2
commit: 74a12767deb72db9bc78bebd807cbe8717fa572c
image:
ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627
```

The v0.2.3-rc.1 staging prerelease is published at commit
`7662a1fb5974a6fc369ca486d2a59c85f68cd3b7` and image index
`sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a`.
Use its release-bound chart or raw sidecar from the
[GitHub prerelease](https://github.com/slizendb/slizen/releases/tag/v0.2.3-rc.1)
for an RC trial. The source chart and stable aliases intentionally remain on
v0.2.2; do not use a floating image reference or treat RC behavior as evidence
for a v0.2.2 deployment.

Record two identities separately: the executable runtime
(release tag + stamped full commit + image digest) and the deployment source
(chart/manifest/config pushed commit + version + exact values/render hash). A
v0.2.3-rc.1 source chart commit can intentionally deploy the stable v0.2.2
image; that is a valid two-artifact mapping, not one shared commit. The
release-bound RC chart instead pins the exact RC digest above.

Read [FAILURE_MODES.md](FAILURE_MODES.md) before the change window and record the
result with [STAGING_RELEASE_GATE.md](STAGING_RELEASE_GATE.md).

## Choose one deployment pattern

- **Sidecar:** put Slizen in the application Pod and point that application at
  `127.0.0.1:6380`. Copy `deploy/kubernetes/observe-sidecar.yaml` as a pattern;
  it is not an injector. Every application Pod gets an independent disposable
  cache and invalidation state.
- **Standalone proxy:** install `charts/slizen` and point a canary workload at
  its ClusterIP Service. This is simpler when several clients share an origin.

Helm cannot inject a sidecar. The chart is not an Operator or admission webhook.
Slizen v0.2 is single-node, and the chart enforces one replica. Never scale it
behind one Service: replicas would have independent cache and invalidation
state.

The standalone chart deliberately uses a `Recreate` Deployment strategy. Every
upgrade or Helm rollback terminates the old Pod before the new Pod is ready, so
there is an expected interval with no ready Service endpoint. Existing RESP
connections close, in-flight requests can fail, and writes without a received
reply are ambiguous. `--atomic` protects Helm state if an upgrade fails; it does
not provide zero downtime or preserve client connections.

Updating a sidecar changes the parent Pod template. Kubernetes replaces the
whole Pod, so the application restarts and its loopback RESP connection closes.
Plan both patterns as connection-disrupting changes and require bounded client
reconnect behavior.

Multiple sidecars are safe for `observe`, but cache mode needs an explicit
consistency decision. A write routed through one sidecar invalidates only that
Pod's cache; sibling sidecars do not learn about it. Before enabling cache mode
for a sidecar Deployment, require at least one of:

- exactly one routed application replica for the cache canary;
- a selected prefix that is operationally read-only/immutable for the whole
  soak; or
- written service-owner acceptance that any sibling or direct-origin write can
  remain invisible through another sidecar for up to the configured local TTL.

Do not treat routing all writes through sidecars as cross-Pod invalidation. The
raw observe example uses `RollingUpdate` with `maxSurge: 1`, which can
temporarily run old and new caches together. A “single replica” cache canary
must also use a reviewed no-overlap strategy such as `Recreate`, or explicitly
accept the same TTL-bounded overlap risk.

## Prerequisites

Run the complete change from one dedicated Bash session. The fail-fast options
below are part of the safety contract: without them, a failed `test`, `jq -e`,
hash, or identity check can fall through to a later mutation. Both paths
require `kubectl`, `jq`, `git`, `awk`, and `mktemp`; the standalone path also
requires Helm and was verified with Helm 3.18.4. A different Helm version is
allowed only after the platform owner records that its lint/template,
`--atomic`, `--wait`, and timeout behavior passed the same failure tests.

```sh
bash
set -Eeuo pipefail
trap 'printf "NO-GO: runbook command failed at shell line %s\n" "$LINENO" >&2' ERR

export APP_NAMESPACE=app-staging
export SLIZEN_NAMESPACE=slizen-staging
export DEPLOYMENT_PATTERN=standalone
export APP_DEPLOYMENT_CONTROL=REPLACE_WITH_gitops_OR_direct
export RUN_DESTRUCTIVE_DRILLS=no

case "$APP_DEPLOYMENT_CONTROL" in
  gitops|direct) ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac

for required_tool in kubectl jq git awk mktemp grep date sleep; do
  command -v "$required_tool" >/dev/null
done

if command -v sha256sum >/dev/null 2>&1; then
  checksum256() { sha256sum "$@"; }
elif command -v shasum >/dev/null 2>&1; then
  checksum256() { shasum -a 256 "$@"; }
else
  printf '%s\n' 'NO-GO: sha256sum or shasum is required' >&2
  exit 1
fi

export HELM_VERSION=
if test "$DEPLOYMENT_PATTERN" = standalone; then
  command -v helm >/dev/null
  export HELM_VERSION="$(helm version --template '{{.Version}}')"
  printf 'helm_version=%s\n' "$HELM_VERSION"
  test "$HELM_VERSION" = v3.18.4 ||
    test "$HELM_VERSION" = "${REVIEWED_HELM_VERSION:-}"
fi
```

Before the window, verify the operator identity has only the
platform-approved permissions needed by the chosen deployment pattern and
drills. The checks below are non-exhaustive early diagnostics, not an RBAC
proof: `kubectl run -i`, Helm storage, waits/watches, Service inspection, policy
cleanup, and chart resources can require additional narrow verbs. Rehearse the
exact chosen command path with the same identity and use server-side dry-runs
before the change window. A pre-created Slizen namespace is acceptable when
namespace creation is not delegated; the application namespace must already
exist:

```sh
kubectl get namespace "$APP_NAMESPACE" >/dev/null
kubectl auth can-i get deployments -n "$APP_NAMESPACE" | grep -Fx yes
kubectl auth can-i get pods -n "$APP_NAMESPACE" | grep -Fx yes
kubectl auth can-i create pods/exec -n "$APP_NAMESPACE" | grep -Fx yes
if test "$APP_DEPLOYMENT_CONTROL" = direct; then
  kubectl auth can-i patch deployments -n "$APP_NAMESPACE" | grep -Fx yes
fi

case "$DEPLOYMENT_PATTERN" in
  standalone)
    if ! kubectl get namespace "$SLIZEN_NAMESPACE" >/dev/null 2>&1; then
      kubectl auth can-i create namespaces | grep -Fx yes
    fi
    kubectl auth can-i get deployments -n "$SLIZEN_NAMESPACE" | grep -Fx yes
    kubectl auth can-i get pods -n "$SLIZEN_NAMESPACE" | grep -Fx yes
    kubectl auth can-i create pods/exec -n "$SLIZEN_NAMESPACE" | grep -Fx yes
    kubectl auth can-i create pods -n "$APP_NAMESPACE" | grep -Fx yes
    kubectl auth can-i create networkpolicies.networking.k8s.io \
      -n "$SLIZEN_NAMESPACE" | grep -Fx yes
    ;;
  sidecar)
    # The GitOps/deployment-controller identity, not necessarily this rollout
    # operator, must be authorized to update the parent Deployment/ConfigMap.
    if test "$APP_DEPLOYMENT_CONTROL" = direct; then
      kubectl auth can-i get configmaps -n "$APP_NAMESPACE" | grep -Fx yes
      kubectl auth can-i create configmaps -n "$APP_NAMESPACE" | grep -Fx yes
      kubectl auth can-i patch configmaps -n "$APP_NAMESPACE" | grep -Fx yes
    fi
    ;;
  *)
    printf '%s\n' 'NO-GO: DEPLOYMENT_PATTERN must be standalone or sidecar' >&2
    exit 1
    ;;
esac

if test "$RUN_DESTRUCTIVE_DRILLS" = yes; then
  if test "$DEPLOYMENT_PATTERN" = standalone; then
    kubectl auth can-i delete pods -n "$SLIZEN_NAMESPACE" | grep -Fx yes
  else
    kubectl auth can-i delete pods -n "$APP_NAMESPACE" | grep -Fx yes
  fi
fi
```

Stop and obtain the normal change-role/RBAC approval when a required answer is
`no`; skip permissions for drills that are explicitly recorded as Partial.
Do not widen credentials ad hoc during the rollout. In particular, the rollout
operator does not need Secret-read access merely to install Slizen. Credential
key existence must be attested by the Secret owner/manager, or checked without
output by an operator who already has that access. The Helm server-side dry-run
and the deployment controller's own preflight remain the authority for their
resource-write permissions. For `APP_DEPLOYMENT_CONTROL=gitops`, record and
rehearse the exact source revision, reconciliation command, controller identity,
and bounded wait; live `patch deployments` permission is neither required nor
requested.

## 0. Agree on the decision before deploying

Assign a service owner, rollout operator, rollback operator, and incident
channel. Fill every blank below before routing traffic. A missing threshold is
a no-go; do not choose a budget after seeing the result.

| Measurement | Baseline and query/source | Go threshold chosen by the service owner |
| --- | --- | --- |
| Application error rate | direct-origin representative window | no more than baseline + `___` percentage points |
| Application p95/p99 | direct-origin representative window | proxy tax no more than `___ ms` or `___%` |
| Origin errors | Redis/Valkey or exporter | no more than baseline + `___` |
| Physical origin GET rate | Redis/Valkey `INFO commandstats` `cmdstat_get:calls` or an equivalent origin-side exporter, direct/observe baseline | cache stage reduces it by at least `___%` for the selected prefix; missing this target is a safe no-benefit result, not permission to widen scope |
| Slizen upstream errors | `slizen_upstream_errors_total` | no more than `___` over baseline after startup |
| Active connections | v0.2.3 `slizen_active_connections`; otherwise platform/application socket telemetry | no more than `___` and no unbounded growth after reconnect/rollback drills |
| Pod memory | platform working-set metric and Pod limit | no more than `___ MiB` and `___%` of the limit |
| Pipeline response memory | exact client maximum pipeline depth × representative worst-case GET/MGET value/response sizes, measured with platform working-set telemetry | remains below `___ MiB` and `___%` of the Pod limit with no OOM/restart; an OOM limit is containment, not a graceful pass |
| Cache budget | `slizen_cache_bytes`, entries, evictions | no more than `___%` of configured cache limits; eviction growth no more than `___/min` |
| Correctness | application checks plus deterministic values | exactly 0 mismatches and 0 validation mismatches |
| Compatibility | command inventory and integration suite | 0 unsupported, rejected, or over-limit commands in routed traffic |
| Availability | readiness/restarts/OOM events | 0 unplanned readiness flaps, restarts, or OOM kills after rollout settles |
| Rollback | timed endpoint restoration drill | direct-origin health restored in less than 5 minutes |

Choose the soak windows too:

```text
observe soak:       ____________________
one-prefix soak:    ____________________
canary-slice soak:  ____________________
each expansion:     ____________________
```

Each window must include one representative traffic cycle and the service's
normal peak. If the team has no better workload-specific evidence, use 24 hours
per stage; a quiet 30-minute window proves only that the process starts.

Do not substitute `slizen_upstream_requests_total` for physical origin traffic.
It counts one logical Slizen data-path call after client retry handling; a
logical `GET` can make multiple wire attempts, and readiness/status `PING`s are
not included. Capture a monotonic origin-side `cmdstat_get:calls`/exporter
delta over the same attributable window, with no `CONFIG RESETSTAT`. Use an
operator or benchmark identity for `INFO`; the Slizen runtime ACL does not need
that permission.

Keep `cache.allow_stale_on_upstream_error=false` during the first trial. Record
the selected prefix's maximum acceptable staleness, whether any writer bypasses
Slizen, and why that risk is acceptable. If direct writes cannot tolerate the
configured local TTL, keep the prefix in `observe`.

Changing only the host/port is valid only for a plaintext downstream client
profile that sends no `AUTH` and whose connection initialization uses only
Slizen-compatible commands. Slizen v0.2 does not authenticate downstream
clients, terminate TLS, or transparently implement arbitrary `HELLO`, `CLIENT`,
or connection-state setup. A client that authenticates directly to Redis needs
a separate reviewed Slizen endpoint profile with downstream auth/TLS disabled;
Slizen itself receives the upstream credentials through its Secret.

## 1. Capture the exact rollback target

The examples below assume an environment variable holds the application
endpoint. Adjust the read/write command when the endpoint comes from a
ConfigMap, Secret reference, flags, or an external deployment system. Capture
that source exactly; do not copy credentials into the trial record. If routing
through Slizen also changes the client TLS/auth/initialization profile, capture
and roll back the complete application configuration/revision, not only the
endpoint variable.

Set these values:

```sh
export APP_DEPLOYMENT=catalog-api
export APP_CONTAINER=app
export APP_LABEL_SELECTOR=app.kubernetes.io/name=catalog-api
export ENDPOINT_ENV=REDIS_ADDRESS
export ORIGINAL_ENDPOINT_CREDENTIAL_FREE=REPLACE_WITH_yes_ONLY_FOR_SECRET_FREE_HOST_PORT
export ROLLBACK_SOURCE_REFERENCE=
export SIDECAR_CONFIGMAP=slizen-config
export SLIZEN_RELEASE=slizen
export SLIZEN_DEPLOYMENT=slizen
export SLIZEN_HOST=slizen.slizen-staging.svc
export SLIZEN_PORT=6380
export SLIZEN_ENDPOINT="$SLIZEN_HOST:$SLIZEN_PORT"
export ORIGIN_HOST=redis.default.svc.cluster.local
export ORIGIN_PORT=6379
export STABLE_VERSION=0.2.2
export STABLE_COMMIT=74a12767deb72db9bc78bebd807cbe8717fa572c
export STABLE_DIGEST=sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627
```

Capture cluster, application revision, endpoint, application image, and any
existing Slizen/Helm revision:

```sh
umask 077
export SLIZEN_PRIVATE_EVIDENCE_DIR="$(
  mktemp -d "${TMPDIR:-/tmp}/slizen-staging-evidence.XXXXXX"
)"
export PRE_SLIZEN_MANIFEST="$SLIZEN_PRIVATE_EVIDENCE_DIR/pre-slizen-application.yaml"
kubectl config current-context
kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o yaml > "$PRE_SLIZEN_MANIFEST"

export ORIGINAL_APP_REVISION="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}'
)"
export ORIGINAL_APP_IMAGE="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o json |
    jq -r --arg container "$APP_CONTAINER" \
      '.spec.template.spec.containers[] | select(.name == $container) | .image'
)"
export ORIGINAL_APP_REPLICAS="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.replicas}'
)"
export ORIGINAL_APP_STRATEGY_JSON="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o json |
    jq -c '.spec.strategy'
)"
export ORIGINAL_ENDPOINT=
case "$ORIGINAL_ENDPOINT_CREDENTIAL_FREE" in
  yes)
    export ORIGINAL_ENDPOINT="$(
      kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o json |
        jq -r --arg container "$APP_CONTAINER" --arg variable "$ENDPOINT_ENV" '
          .spec.template.spec.containers[]
          | select(.name == $container)
          | (.env // [])[]
          | select(.name == $variable)
          | .value // empty
        '
    )"
    test -n "$ORIGINAL_ENDPOINT"
    test "$ORIGINAL_ENDPOINT" != "$SLIZEN_ENDPOINT"
    export ORIGINAL_ENDPOINT_HANDLING=credential-free-shell-value
    ;;
  no)
    test -n "$ROLLBACK_SOURCE_REFERENCE"
    export ORIGINAL_ENDPOINT_HANDLING=owned-configuration-reference
    ;;
  *)
    printf '%s\n' \
      'NO-GO: ORIGINAL_ENDPOINT_CREDENTIAL_FREE must be truthful yes or no' >&2
    exit 1
    ;;
esac
case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    test -n "$ROLLBACK_SOURCE_REFERENCE"
    ;;
  direct)
    test "$ORIGINAL_ENDPOINT_CREDENTIAL_FREE" = yes
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac

export PRE_SLIZEN_MANIFEST_SHA256="$(
  checksum256 "$PRE_SLIZEN_MANIFEST" | awk '{print $1}'
)"
printf 'application_revision=%s\napplication_image=%s\napplication_replicas=%s\napplication_strategy=%s\norigin_endpoint_handling=%s\nrollback_manifest_sha256=%s\n' \
  "$ORIGINAL_APP_REVISION" "$ORIGINAL_APP_IMAGE" "$ORIGINAL_APP_REPLICAS" \
  "$ORIGINAL_APP_STRATEGY_JSON" "$ORIGINAL_ENDPOINT_HANDLING" \
  "$PRE_SLIZEN_MANIFEST_SHA256"
```

The endpoint-variable branch is allowed only for a credential-free host/port
value. Stop if that branch produces an empty or already-Slizen address. If the
endpoint can contain a username, password, token, or URI userinfo, choose `no`;
the runbook will not capture or expand it in the shell. Use the recorded
Secret/ConfigMap reference or service-owned GitOps/full-manifest restoration
for rollback.
Resolve the real origin host, port, database, TLS/auth settings, and
configuration owner before continuing. Keep the saved manifest private because
it may contain Secret references or other sensitive configuration. The default
`mktemp` location stays outside the repository so the rollback capture does not
cause the clean-source gate to fail; record its SHA-256 in shared evidence, not its
private local path or contents.

For an existing standalone Helm release:

```sh
export EXISTING_SLIZEN_RELEASE="$(
  helm list --all --namespace "$SLIZEN_NAMESPACE" \
    --filter "^${SLIZEN_RELEASE}$" --short
)"
if test -n "$EXISTING_SLIZEN_RELEASE"; then
  export PREVIOUS_HELM_REVISION="$(
    helm history "$SLIZEN_RELEASE" -n "$SLIZEN_NAMESPACE" -o json |
      jq -r '([.[] | select(.status == "deployed")][-1].revision // empty)'
  )"
  case "$PREVIOUS_HELM_REVISION" in
    ''|*[!0-9]*)
      printf '%s\n' 'NO-GO: existing Helm release has no numeric deployed revision' >&2
      exit 1
      ;;
  esac
  export PREVIOUS_SLIZEN_IMAGE="$(
    kubectl get deployment "$SLIZEN_DEPLOYMENT" -n "$SLIZEN_NAMESPACE" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
  )"
  test -n "$PREVIOUS_SLIZEN_IMAGE"
  printf 'helm_revision=%s\nslizen_image=%s\n' \
    "$PREVIOUS_HELM_REVISION" "$PREVIOUS_SLIZEN_IMAGE"
else
  export PREVIOUS_HELM_REVISION=
  export PREVIOUS_SLIZEN_IMAGE=
fi
```

For a sidecar, the `ORIGINAL_APP_REVISION` must be the last known-good parent
Deployment revision with the direct Redis endpoint. Verify it before using
`kubectl rollout undo`; a revision number without inspected content is not a
rollback plan.

## 2. Check compatibility and upstream permissions before routing traffic

Build the top-level Redis command inventory from application code, client
configuration, and existing telemetry. Compare it with
[REDIS_COMPATIBILITY.md](REDIS_COMPATIBILITY.md). v0.2 requires database 0 and
does not support transactions, Lua, Pub/Sub, blocking commands, arbitrary data
structures, connection-state handshakes, or transparent TLS termination.

Run the exact application client library's connection-initialization path
against Slizen, including pool creation and reconnect. Do not infer
compatibility from value commands alone. Downstream `AUTH`, TLS, and required
unsupported initialization such as `HELLO`/`CLIENT` are no-go signals. Optional
client metadata commands are acceptable only when the actual library proves it
can tolerate Slizen's explicit error and still reconnect and execute the full
integration suite deterministically.

`proxy.read_timeout` is an idle connection deadline, not only a partial-command
timeout. Keep the v0.2.3 `5m` default or tune it above the application's
expected pool-idle/reuse interval, then verify that the active-connection and
reconnect rates remain inside the pre-agreed budgets. A shorter value that
causes routine pool churn is a no-go configuration even when requests
eventually succeed.

The published v0.2.3-rc.1 image includes the offline command-catalog gate.
Review the exact argument shapes for commands reported with limitations before
acknowledging them. Pin its verified immutable digest and override the image's
normal `slizend` entrypoint:

```sh
export SLIZEN_IMAGE=ghcr.io/slizendb/slizen@sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a
docker run --rm --entrypoint /usr/local/bin/slizenctl \
  "$SLIZEN_IMAGE" version
docker run --rm --entrypoint /usr/local/bin/slizenctl \
  "$SLIZEN_IMAGE" compatibility report \
  --accept-limitations --output json GET MGET SET TTL
```

The stable v0.2.2 binary predates that command, so its operator must perform the
same comparison from `REDIS_COMPATIBILITY.md`. The report never discovers a
workload by itself and does not validate arguments; the explicit inventory and
application integration suite remain required. Capture the CLI version/full
commit with the JSON report and require both to match the image release
evidence.

Preflight the exact upstream identity Slizen will use. An application ACL that
works for the direct client is not automatically sufficient. Slizen's
`go-redis/v9` upstream client must complete its connection/authentication
handshake and readiness `PING`. A proxy `GET` sends `GET` plus `PTTL`; `MGET`
sends `MGET` plus one `PTTL` per key. The identity also needs every supported
write or pass-through command present in the routed application inventory.

Run this preflight through the platform's approved secret-aware client from an
in-cluster context with the same network path, username, password, and database
that Slizen will use. Test a disposable key and all required command/argument
variants. Do not paste credentials into a command or trial record. If the exact
`go-redis/v9` handshake cannot be reproduced safely before installation,
install Slizen with no application traffic, then require stable readiness and
the post-route runtime smoke in step 4 before beginning the soak.

Slizen v0.2 accepts a plain upstream address and has no upstream or downstream
TLS configuration. A TLS-required Redis/Valkey origin or TLS-required
application connection is a no-go unless the platform provides a separately
reviewed private tunnel/termination component and the team tests that complete
path. Do not put `rediss://` in `upstream.address` or claim built-in TLS.

The upstream client is also one direct `redis.NewClient` connection target.
Slizen v0.2 does not discover Redis Cluster or Sentinel topology, follow
`MOVED`/`ASK`, split cross-slot `MGET`, or perform Sentinel-driven master
failover. A Cluster/Sentinel endpoint is a no-go unless a separately operated
and tested proxy/service presents Slizen with one stable standalone-compatible
endpoint and owns topology/failover behavior.

Replay the exact application's maximum pipeline depth with representative
worst-case `GET`/`MGET` value sizes while watching Pod working-set memory.
redcon can retain replies for several already-read commands until the pipeline
flushes, and Slizen has no aggregate pipeline response-byte limit. A test that
exceeds the pre-agreed memory headroom, restarts, or reaches OOM is a no-go;
raising the Pod limit without a workload-specific bound is not a pass.

Do not proceed if routed traffic needs an unsupported command, a non-zero
database, unsupported connection state, a request above a configured bound, or
an upstream ACL that rejects Slizen's handshake, `PING`, `PTTL`, or any routed
application command.

## 3A. Install or upgrade the standalone Helm proxy in `observe`

Run from the repository checkout whose chart you reviewed. Render and perform a
server-side dry run first:

Slizen v0.2 has no downstream RESP authentication. The chart's default
NetworkPolicy therefore denies all ingress. Create a reviewed values file that
allows only the exact canary Pods; replace both example labels with labels
actually enforced by the cluster:

```yaml
# slizen-staging-network-policy.yaml
admin:
  listen: 0.0.0.0:9090
  allowNetworkAccess: true
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
networkPolicy:
  enabled: true
  redisIngressPeers:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: app-staging
      podSelector:
        matchLabels:
          app.kubernetes.io/name: catalog-api
  metricsIngressPeers:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
      podSelector:
        matchLabels:
          app.kubernetes.io/name: prometheus
```

Replace both application and monitoring labels with identities enforced in the
target cluster. Do not use a namespace-only allow rule when an exact Pod
selector is available. If the cluster has no ServiceMonitor CRD, keep
`metrics.enabled=true`, set `serviceMonitor.enabled=false`, and configure the
team's other reviewed continuous scraper against the restricted metrics
Service before install. If the CNI does not implement Kubernetes NetworkPolicy,
configure and test the platform's equivalent isolation before routing. Store
the environment values in the team's reviewed configuration system, outside
the Slizen chart checkout, and set its path:

```sh
export STAGING_VALUES_FILE=/path/to/reviewed/slizen-staging-network-policy.yaml
```

The chart checkout must be clean and its commit must exist in the reviewed
remote. An uncommitted local chart can support development, not a staging pass:

```sh
test -z "$(git status --porcelain --untracked-files=all)"
export CHART_COMMIT="$(git rev-parse HEAD)"
git fetch --prune origin
export CHART_REMOTE_REFS="$(git branch -r --contains "$CHART_COMMIT")"
test -n "$CHART_REMOTE_REFS"
printf '%s\n' "$CHART_REMOTE_REFS"
helm show chart ./charts/slizen

export STAGING_VALUES_SHA256="$(
  checksum256 "$STAGING_VALUES_FILE" | awk '{print $1}'
)"
printf 'chart_commit=%s\nstaging_values_sha256=%s\n' \
  "$CHART_COMMIT" "$STAGING_VALUES_SHA256"
```

Stop if the remote-containment output does not identify the reviewed repository
branch/tag. The environment values file must contain Secret references, never
Secret contents.

```sh
helm lint ./charts/slizen \
  -f "$STAGING_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string mode=observe \
  --set-string image.digest="$STABLE_DIGEST"

if ! kubectl get namespace "$SLIZEN_NAMESPACE" >/dev/null 2>&1; then
  kubectl create namespace "$SLIZEN_NAMESPACE"
fi

# Declare the reviewed auth posture. This must not be inferred from whether a
# manually typed Secret name happens to be empty.
export UPSTREAM_AUTH_EXPECTED=REPLACE_WITH_disabled_OR_secret
export SECRET_KEY_ATTESTATION_REFERENCE=

export RENDERED_SLIZEN_DEPLOYMENT_JSON="$(
  helm template "$SLIZEN_RELEASE" ./charts/slizen \
    --namespace "$SLIZEN_NAMESPACE" \
    -f "$STAGING_VALUES_FILE" \
    --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
    --set-string mode=observe \
    --set-string image.digest="$STABLE_DIGEST" \
    --show-only templates/deployment.yaml |
    kubectl create --dry-run=client -f - -o json
)"
export UPSTREAM_SECRET_NAME="$(
  printf '%s\n' "$RENDERED_SLIZEN_DEPLOYMENT_JSON" |
    jq -r '
      .spec.template.spec.containers[]
      | select(.name == "slizen")
      | (.env // [])[]
      | select(.name == "SLIZEN_UPSTREAM_USERNAME")
      | .valueFrom.secretKeyRef.name
    '
)"
export UPSTREAM_PASSWORD_SECRET_NAME="$(
  printf '%s\n' "$RENDERED_SLIZEN_DEPLOYMENT_JSON" |
    jq -r '
      .spec.template.spec.containers[]
      | select(.name == "slizen")
      | (.env // [])[]
      | select(.name == "SLIZEN_UPSTREAM_PASSWORD")
      | .valueFrom.secretKeyRef.name
    '
)"
export UPSTREAM_USERNAME_KEY="$(
  printf '%s\n' "$RENDERED_SLIZEN_DEPLOYMENT_JSON" |
    jq -r '
      .spec.template.spec.containers[]
      | select(.name == "slizen")
      | (.env // [])[]
      | select(.name == "SLIZEN_UPSTREAM_USERNAME")
      | .valueFrom.secretKeyRef.key
    '
)"
export UPSTREAM_PASSWORD_KEY="$(
  printf '%s\n' "$RENDERED_SLIZEN_DEPLOYMENT_JSON" |
    jq -r '
      .spec.template.spec.containers[]
      | select(.name == "slizen")
      | (.env // [])[]
      | select(.name == "SLIZEN_UPSTREAM_PASSWORD")
      | .valueFrom.secretKeyRef.key
    '
)"
case "$UPSTREAM_AUTH_EXPECTED" in
  disabled)
    test -z "$UPSTREAM_SECRET_NAME"
    test -z "$UPSTREAM_PASSWORD_SECRET_NAME"
    ;;
  secret)
    test -n "$UPSTREAM_SECRET_NAME"
    test "$UPSTREAM_PASSWORD_SECRET_NAME" = "$UPSTREAM_SECRET_NAME"
    test -n "$UPSTREAM_USERNAME_KEY"
    test -n "$UPSTREAM_PASSWORD_KEY"
    ;;
  *)
    printf '%s\n' 'NO-GO: auth posture must be disabled or secret' >&2
    exit 1
    ;;
esac

export PRIVACY_SECRET_NAME="$(
  printf '%s\n' "$RENDERED_SLIZEN_DEPLOYMENT_JSON" |
    jq -r '
      .spec.template.spec.containers[]
      | select(.name == "slizen")
      | (.env // [])[]
      | select(.name == "SLIZEN_KEY_HASH_SECRET")
      | .valueFrom.secretKeyRef.name
    '
)"
export PRIVACY_SECRET_KEY="$(
  printf '%s\n' "$RENDERED_SLIZEN_DEPLOYMENT_JSON" |
    jq -r '
      .spec.template.spec.containers[]
      | select(.name == "slizen")
      | (.env // [])[]
      | select(.name == "SLIZEN_KEY_HASH_SECRET")
      | .valueFrom.secretKeyRef.key
    '
)"

if test -n "$UPSTREAM_SECRET_NAME"; then
  if kubectl auth can-i get "secret/$UPSTREAM_SECRET_NAME" \
    -n "$SLIZEN_NAMESPACE" | grep -Fx yes >/dev/null; then
    kubectl get secret "$UPSTREAM_SECRET_NAME" -n "$SLIZEN_NAMESPACE" -o json |
      jq -e --arg username "$UPSTREAM_USERNAME_KEY" \
        --arg password "$UPSTREAM_PASSWORD_KEY" \
        '.data[$username] != null and .data[$password] != null' >/dev/null
  else
    test -n "$SECRET_KEY_ATTESTATION_REFERENCE"
  fi
fi
if test -n "$PRIVACY_SECRET_NAME"; then
  if kubectl auth can-i get "secret/$PRIVACY_SECRET_NAME" \
    -n "$SLIZEN_NAMESPACE" | grep -Fx yes >/dev/null; then
    kubectl get secret "$PRIVACY_SECRET_NAME" -n "$SLIZEN_NAMESPACE" -o json |
      jq -e --arg key "$PRIVACY_SECRET_KEY" \
        '.data[$key] != null' >/dev/null
  else
    test -n "$SECRET_KEY_ATTESTATION_REFERENCE"
  fi
fi

helm template "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" \
  -f "$STAGING_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string mode=observe \
  --set-string image.digest="$STABLE_DIGEST" |
  kubectl apply --dry-run=server -n "$SLIZEN_NAMESPACE" -f -

helm template "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" \
  -f "$STAGING_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string mode=observe \
  --set-string image.digest="$STABLE_DIGEST" |
  checksum256
```

Record the rendered hash with the chart commit and values hash. Re-render after
any input change; do not reuse the earlier hash.

These commands are verified with Helm 3.18.4 and deliberately use
`--atomic --timeout`. If the platform supplies another Helm version, verify its
failure and wait semantics with that platform before the change window rather
than changing flags during the rollout.

Install with a bounded atomic rollout:

```sh
helm upgrade --install "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" --create-namespace \
  --atomic --timeout 5m \
  -f "$STAGING_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string mode=observe \
  --set-string image.digest="$STABLE_DIGEST"

kubectl rollout status deployment/"$SLIZEN_DEPLOYMENT" \
  -n "$SLIZEN_NAMESPACE" --timeout=2m
kubectl get deployment "$SLIZEN_DEPLOYMENT" -n "$SLIZEN_NAMESPACE" \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}{"\n"}'

export SLIZEN_POD="$(
  kubectl get pod -n "$SLIZEN_NAMESPACE" \
    -l app.kubernetes.io/instance="$SLIZEN_RELEASE" \
    -o jsonpath='{.items[0].metadata.name}'
)"
export RUNTIME_STATUS="$(
  kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- slizenctl status
)"
printf '%s\n' "$RUNTIME_STATUS" |
  jq -e --arg version "$STABLE_VERSION" --arg commit "$STABLE_COMMIT" \
    '.version == $version and .commit == $commit'
printf '%s\n' "$RUNTIME_STATUS" | jq '{version, commit}'
```

The deployed image reference must contain the expected digest. Record the
status-reported runtime version and full commit, and compare their
tag/commit/digest mapping with the published runtime evidence. Do not compare
the runtime commit to `CHART_COMMIT`; they identify different artifacts.

The pre-render Secret names/keys or the recorded Secret-owner attestation must
match `upstream.existingSecret` and `privacy.existingSecret` in the reviewed
values file. The chart never creates credentials. Both upstream keys are
required; an empty username value selects Redis's default user. Omit the whole
upstream Secret reference when authentication is disabled. Never grant Secret
read merely for this check. Secret-backed environment variables are read only
at process start, so rotate them with a planned connection-disrupting Pod
replacement.

The default admin listener is loopback-only. The chart creates no admin Service.
The probes use `exec` because a kubelet HTTP probe to the Pod IP cannot reach
`127.0.0.1`.

Before routing a client, restrict the RESP Service to the exact application
namespace/Pod selectors and restrict any shared admin/metrics Service to the
scraper. A broadly reachable unauthenticated RESP port or admin API is a staging
fail, even in `observe`. An applied manifest is not proof that the CNI enforces
it.

Use the existing exact canary application Pod for the positive proof. Never
create a foreign probe with production application labels: it can match the
application Service and join its EndpointSlices. For the negative proof, use a
neutral Pod in the same namespace and inspect source-namespace NetworkPolicies
first. If source-side egress selects the neutral Pod differently, create an
approved isolated test namespace/policy or record the platform's equivalent
proof instead. Resolve the Service ClusterIP once so DNS cannot explain the
denied result:

```sh
export SLIZEN_SERVICE_IP="$(
  kubectl get service "$SLIZEN_RELEASE" -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.spec.clusterIP}'
)"
export NETWORK_TEST_DENIED_LABELS='slizen.dev/network-test=probe'
export NETWORK_TEST_IMAGE=redis:7-alpine@sha256:6ab0b6e7381779332f97b8ca76193e45b0756f38d4c0dcda72dbb3c32061ab99

kubectl get networkpolicy -n "$APP_NAMESPACE"
export APP_POD="$(
  kubectl get pod -n "$APP_NAMESPACE" -l "$APP_LABEL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c "$APP_CONTAINER" -- \
  /path/to/application-slizen-connectivity-smoke \
  "$SLIZEN_SERVICE_IP:$SLIZEN_PORT"

export RESP_DENIED_PROBE_OVERRIDES="$(
  jq -cn \
    --arg image "$NETWORK_TEST_IMAGE" \
    --arg host "$SLIZEN_SERVICE_IP" \
    --arg port "$SLIZEN_PORT" '
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
            name: "slizen-denied-probe",
            image: $image,
            command: ["sh", "-c"],
            args: [
              "echo probe-started; timeout 5 redis-cli -h \"$1\" -p \"$2\" PING; rc=$?; echo probe-exit=$rc; test \"$rc\" -ne 0",
              "probe", $host, $port
            ],
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
if kubectl run slizen-denied-probe -n "$APP_NAMESPACE" \
  --rm -i --restart=Never \
  --labels="$NETWORK_TEST_DENIED_LABELS" \
  --image="$NETWORK_TEST_IMAGE" \
  --overrides="$RESP_DENIED_PROBE_OVERRIDES"
then
  printf '%s\n' 'controlled allowed/denied pair proved RESP ingress enforcement'
else
  printf '%s\n' 'NO-GO: denied probe did not prove policy enforcement' >&2
  exit 1
fi
```

Replace the positive placeholder before the change window with the application's
normal secret-safe Slizen connection test. An enforcing CNI can silently drop
until `timeout` returns non-zero or reject immediately with a connection error;
only a successful denied `PONG` is an immediate fail. A denied non-zero result
is evidence only because the exact allowed canary reached the same ClusterIP
first and source-side egress treatment was reviewed. Step 4 must still run the
full post-route smoke. When metrics are enabled, resolve the exact metrics
Service ClusterIP once and repeat the pair against that IP, not DNS:

```sh
export SLIZEN_METRICS_SERVICE_IP="$(
  kubectl get service "$SLIZEN_RELEASE-metrics" -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.spec.clusterIP}'
)"
export MONITORING_NAMESPACE=monitoring
export MONITORING_POD_SELECTOR=app.kubernetes.io/name=prometheus

kubectl get networkpolicy -n "$MONITORING_NAMESPACE"
export MONITORING_POD="$(
  kubectl get pod -n "$MONITORING_NAMESPACE" \
    -l "$MONITORING_POD_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl exec -n "$MONITORING_NAMESPACE" "$MONITORING_POD" -- \
  /path/to/monitoring-http-smoke \
  "http://$SLIZEN_METRICS_SERVICE_IP:9090/metrics"

export METRICS_DENIED_PROBE_OVERRIDES="$(
  jq -cn \
    --arg image "$NETWORK_TEST_IMAGE" \
    --arg url "http://$SLIZEN_METRICS_SERVICE_IP:9090/metrics" '
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
            name: "slizen-metrics-denied-probe",
            image: $image,
            command: ["sh", "-c"],
            args: [
              "output=$(wget -S -T 5 -O /dev/null \"$1\" 2>&1); rc=$?; if printf \"%s\\n\" \"$output\" | grep -Eq \"HTTP/[0-9.]+\"; then exit 1; fi; test \"$rc\" -ne 0",
              "probe", $url
            ],
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
if kubectl run slizen-metrics-denied-probe -n "$MONITORING_NAMESPACE" \
  --rm -i --restart=Never \
  --labels="$NETWORK_TEST_DENIED_LABELS" \
  --image="$NETWORK_TEST_IMAGE" \
  --overrides="$METRICS_DENIED_PROBE_OVERRIDES"
then
  printf '%s\n' 'controlled allowed/denied pair proved metrics ingress enforcement'
else
  printf '%s\n' 'NO-GO: denied metrics probe reached the Service' >&2
  exit 1
fi
```

Replace the positive placeholder with the monitoring stack's normal safe scrape
check. An unlisted Pod must receive no HTTP response, while the exact configured
scraper peer must scrape successfully. Review source-side egress treatment just
as for the RESP pair; otherwise a failed neutral probe is inconclusive.

## 3B. Install or upgrade the sidecar in `observe`

Copy `deploy/kubernetes/observe-sidecar.yaml` into the application's deployment
source, replace the example application, set the real upstream, and pin the
stable digest. Do not manage the same Deployment from both GitOps and direct
`kubectl`.

When the origin requires an ACL identity, provision
`slizen-sidecar-upstream` through the platform secret manager and add this
fail-closed Secret-backed environment block to the sidecar:

```yaml
env:
  - name: SLIZEN_UPSTREAM_USERNAME
    valueFrom:
      secretKeyRef:
        name: slizen-sidecar-upstream
        key: username
  - name: SLIZEN_UPSTREAM_PASSWORD
    valueFrom:
      secretKeyRef:
        name: slizen-sidecar-upstream
        key: password
```

Both keys are required when this block is present; use an empty `username`
Secret value when Redis authenticates the default user with a password. Omit
the whole block when the origin has no authentication. Never put the
username/password in the manifest, command line, shell history, or trial record.
Because Secrets are namespaced, the Secret owner must attest the namespace,
name, and both key names. If the rollout operator already has Secret-read
access, they may additionally check without printing values:

```sh
export SIDECAR_SECRET_KEY_ATTESTATION_REFERENCE=
test -n "$SIDECAR_SECRET_KEY_ATTESTATION_REFERENCE"
if kubectl auth can-i get secret/slizen-sidecar-upstream \
  -n "$APP_NAMESPACE" | grep -Fx yes >/dev/null; then
  kubectl get secret slizen-sidecar-upstream -n "$APP_NAMESPACE" -o json |
    jq -e '.data.username != null and .data.password != null' >/dev/null
fi
```

Do not grant Secret-read merely to run the optional check.

The exact Secret identity must pass step 2's connection/auth handshake,
readiness `PING`, `GET`/`MGET` plus `PTTL`, and routed-command ACL preflight.

Before applying, ensure all of these are true:

- the application's endpoint in the new Pod template is `127.0.0.1:6380`;
- Slizen's sidecar proxy listener itself is `127.0.0.1:6380`, not a Pod-wide
  `0.0.0.0:6380` listener;
- the application uses the reviewed plaintext/no-downstream-`AUTH` Slizen
  client profile and its exact initialization/reconnect path passed;
- Slizen's origin remains the captured direct Redis/Valkey endpoint;
- the Secret-backed upstream identity, when used, is the one that passed ACL
  preflight;
- global mode and the empty-prefix policy are `observe`;
- the Slizen image is the stable immutable digest;
- `terminationGracePeriodSeconds` is greater than
  `proxy.shutdown_timeout`;
- `slizen.dev/config-revision` changes whenever the subPath-mounted ConfigMap
  changes.

Apply through the normal deployment system. Before any staging apply, run from
the application's deployment-source checkout and prove that the exact parent
manifest/config is clean, pushed, hashed, and accepted by the API server:

```sh
export SIDECAR_MANIFEST=deploy/kubernetes/catalog-api-sidecar.yaml
export EXPECTED_SIDECAR_CONFIG_REVISION=REPLACE_WITH_REVIEWED_MANIFEST_VALUE
test -z "$(git status --porcelain --untracked-files=all)"
export APP_SOURCE_COMMIT="$(git rev-parse HEAD)"
git fetch --prune origin
export APP_SOURCE_REMOTE_REFS="$(git branch -r --contains "$APP_SOURCE_COMMIT")"
test -n "$APP_SOURCE_REMOTE_REFS"
export SIDECAR_MANIFEST_SHA256="$(
  checksum256 "$SIDECAR_MANIFEST" | awk '{print $1}'
)"
printf 'application_source_commit=%s\nsidecar_manifest_sha256=%s\n' \
  "$APP_SOURCE_COMMIT" "$SIDECAR_MANIFEST_SHA256"
printf '%s\n' "$APP_SOURCE_REMOTE_REFS"
kubectl apply --dry-run=server -n "$APP_NAMESPACE" -f "$SIDECAR_MANIFEST"
```

Let the reviewed GitOps/deployment controller apply that commit. Only for a
Deployment explicitly not managed by GitOps, the same immutable source may be
applied directly:

```sh
test "${SIDECAR_DIRECT_APPLY_APPROVED:-}" = yes
kubectl apply -n "$APP_NAMESPACE" -f "$SIDECAR_MANIFEST"
```

After either GitOps reconciliation or the approved direct apply, wait for the
specific reviewed Pod-template revision before using `rollout status`. This
prevents an already-complete old Deployment from satisfying the check:

```sh
wait_for_deployment_config_revision() {
  local expected="$1"
  local deadline="$(( $(date +%s) + 300 ))"
  local live_revision
  while test "$(date +%s)" -lt "$deadline"; do
    live_revision="$(
      kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
        -o jsonpath='{.spec.template.metadata.annotations.slizen\.dev/config-revision}'
    )"
    if test "$live_revision" = "$expected"; then
      return 0
    fi
    sleep 2
  done
  printf '%s\n' 'NO-GO: reviewed sidecar revision did not reconcile' >&2
  return 1
}

wait_for_sidecar_image() {
  local expected="$1"
  local deadline="$(( $(date +%s) + 300 ))"
  local live_image
  while test "$(date +%s)" -lt "$deadline"; do
    live_image="$(
      kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
        -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
    )"
    if test "$live_image" = "$expected"; then
      return 0
    fi
    sleep 2
  done
  printf '%s\n' 'NO-GO: expected sidecar image did not reconcile' >&2
  return 1
}

wait_for_deployment_config_revision "$EXPECTED_SIDECAR_CONFIG_REVISION"
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=5m

export LIVE_SIDECAR_IMAGE="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)"
test "$LIVE_SIDECAR_IMAGE" = "ghcr.io/slizendb/slizen@$STABLE_DIGEST"
kubectl diff -n "$APP_NAMESPACE" -f "$SIDECAR_MANIFEST"

export APP_POD="$(
  kubectl get pod -n "$APP_NAMESPACE" \
    -l "$APP_LABEL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
export RUNTIME_STATUS="$(
  kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c slizen -- slizenctl status
)"
printf '%s\n' "$RUNTIME_STATUS" |
  jq -e --arg version "$STABLE_VERSION" --arg commit "$STABLE_COMMIT" \
    '.version == $version and .commit == $commit and .mode == "observe"'
printf '%s\n' "$RUNTIME_STATUS" | jq '{version, commit, mode}'
```

Record the application commit, manifest/config hash, deployed digest, and
Slizen's status-reported runtime version/full commit as separate identities. A
dirty or unpushed direct apply is only an isolated rehearsal: route no real
application traffic and never record it as a staging pass.

For a later sidecar image upgrade, capture the current revision/image first,
then update the pinned digest:

```sh
export PREVIOUS_SIDECAR_REVISION="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}'
)"
export PREVIOUS_SIDECAR_IMAGE="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)"
export NEW_SLIZEN_DIGEST=sha256:REPLACE_WITH_A_VERIFIED_PUBLISHED_DIGEST

case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    export NEW_SIDECAR_SOURCE_COMMIT=REPLACE_WITH_CLEAN_PUSHED_COMMIT
    /path/to/sync-reviewed-application-revision "$NEW_SIDECAR_SOURCE_COMMIT"
    ;;
  direct)
    test "${SIDECAR_DIRECT_MUTATION_APPROVED:-}" = yes
    kubectl set image deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      slizen="ghcr.io/slizendb/slizen@$NEW_SLIZEN_DIGEST"
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac
wait_for_sidecar_image "ghcr.io/slizendb/slizen@$NEW_SLIZEN_DIGEST"
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=5m
test "$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)" = "ghcr.io/slizendb/slizen@$NEW_SLIZEN_DIGEST"
```

After either path, wait for the rollout, re-check the live immutable digest and
runtime status, and retain the source commit/hash. Never make a direct mutation
to a GitOps-managed Deployment or leave source configured to revert it
unpredictably.

## 4. Route one low-risk workload in `observe`

For the standalone chart, change only one low-risk workload or a small traffic
slice. This endpoint-only example applies only when the reviewed application
profile is already plaintext, sends no downstream `AUTH`, and passed the exact
client initialization check. Make the endpoint change through the system that
owns the Deployment. A direct mutation is allowed only for an explicitly
non-GitOps-managed canary:

```sh
wait_for_application_endpoint() {
  local expected="$1"
  local deadline="$(( $(date +%s) + 300 ))"
  local live_endpoint
  while test "$(date +%s)" -lt "$deadline"; do
    live_endpoint="$(
      kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o json |
        jq -r --arg container "$APP_CONTAINER" --arg variable "$ENDPOINT_ENV" '
          .spec.template.spec.containers[]
          | select(.name == $container)
          | (.env // [])[]
          | select(.name == $variable)
          | .value // empty
        '
    )"
    if test "$live_endpoint" = "$expected"; then
      return 0
    fi
    sleep 2
  done
  printf '%s\n' 'NO-GO: expected application endpoint did not reconcile' >&2
  return 1
}

case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    export APP_ROUTE_SOURCE_COMMIT=REPLACE_WITH_CLEAN_PUSHED_COMMIT
    /path/to/sync-reviewed-application-revision "$APP_ROUTE_SOURCE_COMMIT"
    ;;
  direct)
    test "${APP_DIRECT_MUTATION_APPROVED:-}" = yes
    kubectl set env deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      --containers="$APP_CONTAINER" "$ENDPOINT_ENV=$SLIZEN_ENDPOINT"
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac
wait_for_application_endpoint "$SLIZEN_ENDPOINT"
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=5m
```

For a sidecar, the endpoint change is already part of the parent Pod template.
Do not point other workloads at that Pod.

Now run the application's real integration/smoke path from the routed
application Pod. Cluster-only DNS names below are intentionally never executed
from the operator workstation:

```sh
export APP_POD="$(
  kubectl get pod -n "$APP_NAMESPACE" \
    -l "$APP_LABEL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"

# Replace this with the service's normal secret-aware staging health command.
kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c "$APP_CONTAINER" -- \
  /path/to/application-smoke
```

The application smoke must exercise a disposable staging key through its
configured Slizen endpoint, cover representative value-bearing reads and
writes, and verify the final value independently at Redis/Valkey using an
approved in-cluster, secret-aware diagnostic path. If the application container
already includes a correctly configured Redis diagnostic wrapper, the minimum
sequence is `SET ... EX`, `GET`, direct-origin `GET`, and `DEL`. Do not install a
debug tool or place credentials in shell history during the change window. The
smoke also has to create a fresh client connection and force one bounded
reconnect so connection initialization is evidence, not an untested assumption.

Inspect the private admin API from inside the Slizen container:

```sh
export SLIZEN_POD="$(
  kubectl get pod -n "$SLIZEN_NAMESPACE" \
    -l app.kubernetes.io/instance="$SLIZEN_RELEASE" \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- slizenctl readyz
kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- slizenctl status
kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- slizenctl hotkeys --limit 20
kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- slizenctl audit --limit 100
```

For a sidecar, select the parent Pod in its application namespace instead. At
the start and end of the observe soak, verify:

- mode is `observe`;
- cache-hit delta is zero and retained cache entries remain zero;
- value-bearing routed reads and writes reach origin; local protocol commands
  such as `PING`, `SELECT 0`, and `QUIT`, plus readiness `PING`s, are excluded
  from a one-for-one request comparison;
- no unsupported/over-limit command appears;
- application error and latency deltas remain inside the pre-agreed budgets;
- no unplanned restart, OOM, or readiness flap occurs;
- audit output contains no raw values or credentials.

`telemetry_complete=false` means the bounded report was truncated, tracking
state was evicted, or a key exceeded the tracking byte limit. The v0.2.3
candidate also marks it false when an unseen key is dropped at tracker capacity
to preserve HOT state. Do not call a partial audit a complete workload
inventory. Increase the window or reduce trial scope rather than removing
bounds.

For the standalone chart, metrics and the complete admin API share one
listener. The initial reviewed `STAGING_VALUES_FILE` in step 3A must already
contain the explicit network acknowledgement, continuous scrape mechanism, and
exact monitoring peer:

```yaml
admin:
  listen: 0.0.0.0:9090
  allowNetworkAccess: true
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
networkPolicy:
  metricsIngressPeers:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: monitoring
      podSelector:
        matchLabels:
          app.kubernetes.io/name: prometheus
```

The labels above are placeholders. A ServiceMonitor does not bypass
NetworkPolicy. Protect that Service with the rendered policy or an authenticated
platform proxy and run the paired allowed/denied scrape check described in step
3A. Do not edit the values after their recorded hash; any monitoring change
requires a fresh lint, server dry-run, render hash, atomic upgrade, and
post-upgrade scrape proof before the soak. The ServiceMonitor CRD is needed only
when its flag is enabled.

The raw sidecar example deliberately listens on `127.0.0.1:9090` and creates no
Service or PodMonitor. A normal out-of-Pod Prometheus target cannot scrape that
address. Before a sidecar cache canary, provide a continuous path using the
platform's approved same-Pod collector/proxy, or deliberately bind the admin
listener to the Pod address and protect discovery and ingress so only the
scraper can reach it. Remember that every admin route shares the metrics port.
`kubectl port-forward` is useful for a one-time inspection but is not continuous
monitoring. If the team cannot collect Slizen metrics throughout the soak, keep
the sidecar in `observe`; the cache staging gate is a no-go.

The supplied dashboard has a core signal set compatible with v0.2.2. Its cache
limit gauges, miss-reason breakdown, capacity-drop telemetry, process
collectors, and active-connection gauge are v0.2.3 candidate additions. For
v0.2.2, record configured cache limits beside the measured used bytes/entries,
use platform/application socket telemetry for connections and resources, and
use the stable metrics listed in [OBSERVABILITY.md](OBSERVABILITY.md). An absent
candidate-only series is not evidence of zero pressure, zero connections, or a
complete audit.

Before starting the representative soak, import and review the dashboard and
alerts described in [OBSERVABILITY.md](OBSERVABILITY.md), then test the team's
alert route. Slizen metrics do not report Kubernetes readiness flapping; stable
v0.2.2 also lacks the candidate process collectors. Retain the platform's
probe, restart, CPU, container memory/limit, and OOM signals for every version.

## 5A. Rehearse endpoint-first rollback in under five minutes

Do this while Slizen is healthy and still in `observe`. The measured result is
part of the staging gate.

For the standalone pattern:

The endpoint-only rollback below is valid only when Slizen routing changed no
other application client setting and the endpoint value is explicitly
credential-free. Otherwise roll back the inspected Secret/ConfigMap or complete
application revision/profile so direct-origin TLS/auth and initialization are
restored atomically, then run the same direct-origin health check.

```sh
export ROLLBACK_STARTED_AT="$(date +%s)"

case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    /path/to/restore-reviewed-application-revision "$ROLLBACK_SOURCE_REFERENCE"
    ;;
  direct)
    test "${APP_DIRECT_MUTATION_APPROVED:-}" = yes
    test "$ORIGINAL_ENDPOINT_CREDENTIAL_FREE" = yes
    kubectl set env deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      --containers="$APP_CONTAINER" "$ENDPOINT_ENV=$ORIGINAL_ENDPOINT"
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac

if test "$ORIGINAL_ENDPOINT_CREDENTIAL_FREE" = yes; then
  wait_for_application_endpoint "$ORIGINAL_ENDPOINT"
else
  /path/to/assert-reviewed-direct-origin-profile "$ROLLBACK_SOURCE_REFERENCE"
fi
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=4m

# This command runs inside the application Pod and must prove the direct path.
export APP_POD="$(
  kubectl get pod -n "$APP_NAMESPACE" \
    -l "$APP_LABEL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c "$APP_CONTAINER" -- \
  /path/to/application-direct-origin-smoke

export ROLLBACK_SECONDS="$(( $(date +%s) - ROLLBACK_STARTED_AT ))"
printf 'endpoint rollback seconds=%s\n' "$ROLLBACK_SECONDS"
test "$ROLLBACK_SECONDS" -lt 300
```

Do not uninstall or roll Helm back until the direct application path is healthy.
After the rehearsal, route the canary back to the still-running observe proxy
through the same owning system and repeat its live endpoint assertion and smoke
test if the trial will continue.

For a sidecar, `kubectl rollout undo` restores only a ReplicaSet Pod template;
it does not restore Deployment-level `spec.strategy` or `spec.replicas`. The
rehearsal below is valid only while those fields still equal the captured
pre-Slizen values. If the trial has changed either one, use the service's
pre-recorded GitOps/full-manifest revert instead.

```sh
export ROLLBACK_STARTED_AT="$(date +%s)"
case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    /path/to/restore-reviewed-application-revision "$ROLLBACK_SOURCE_REFERENCE"
    ;;
  direct)
    test "${APP_DIRECT_MUTATION_APPROVED:-}" = yes
    kubectl rollout undo deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      --to-revision="$ORIGINAL_APP_REVISION"
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac
if test "$ORIGINAL_ENDPOINT_CREDENTIAL_FREE" = yes; then
  wait_for_application_endpoint "$ORIGINAL_ENDPOINT"
else
  /path/to/assert-reviewed-direct-origin-profile "$ROLLBACK_SOURCE_REFERENCE"
fi
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=4m
test "$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.replicas}'
)" = "$ORIGINAL_APP_REPLICAS"
test "$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o json |
    jq -c '.spec.strategy'
)" = "$ORIGINAL_APP_STRATEGY_JSON"

export APP_POD="$(
  kubectl get pod -n "$APP_NAMESPACE" \
    -l "$APP_LABEL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c "$APP_CONTAINER" -- \
  /path/to/application-direct-origin-smoke

export ROLLBACK_SECONDS="$(( $(date +%s) - ROLLBACK_STARTED_AT ))"
printf 'sidecar rollback seconds=%s\n' "$ROLLBACK_SECONDS"
test "$ROLLBACK_SECONDS" -lt 300
```

Replace the placeholder with the application's normal secret-aware health check
before the change window. It must run from the application/in-cluster context
and prove a representative direct-origin read, not only Kubernetes rollout
completion. Do not put credentials into shell history or the trial record.

Only use this command after verifying that `ORIGINAL_APP_REVISION` contains the
direct endpoint and Deployment-level strategy/replica fields are unchanged.
Before any cache-stage `Recreate` or replica-count change, record and rehearse
the exact service-owned GitOps/full-manifest revert that restores the complete
parent Deployment. Reapply the reviewed observe-sidecar revision if the trial
continues.

## 5B. Run bounded failure drills in `observe`

Run these only after the endpoint-first rollback rehearsal passed, the isolated
canary has been routed back through Slizen, and its smoke check is green. Keep
global mode and every prefix in `observe`, keep stale fallback disabled, stop
non-idempotent test writes, record an incident owner, and keep the exact direct
rollback command open in another terminal. Define an error/duration abort
threshold before each drill. Crossing it means restore the direct endpoint
immediately and clean up the injector.

Artificial OOM is not a required staging drill. Platform OOM evidence and memory
budgets remain mandatory, but deliberately exhausting a shared node or
application Pod is outside this runbook.

### Graceful `SIGTERM`

A normal Pod deletion sends `SIGTERM` and honors the configured grace period.
For standalone, set:

```sh
export DRILL_NAMESPACE="$SLIZEN_NAMESPACE"
export DRILL_DEPLOYMENT="$SLIZEN_DEPLOYMENT"
export DRILL_SELECTOR="app.kubernetes.io/instance=$SLIZEN_RELEASE"
```

For an isolated sidecar canary, use the parent workload instead:

```sh
export DRILL_NAMESPACE="$APP_NAMESPACE"
export DRILL_DEPLOYMENT="$APP_DEPLOYMENT"
export DRILL_SELECTOR="$APP_LABEL_SELECTOR"
```

Resolve and print the exact disposable target before deleting it:

```sh
wait_for_new_ready_drill_pod() {
  local deadline="$(( $(date +%s) + 120 ))"
  local pods_json new_ready_count total_count
  while test "$(date +%s)" -lt "$deadline"; do
    pods_json="$(
      kubectl get pod -n "$DRILL_NAMESPACE" -l "$DRILL_SELECTOR" -o json
    )"
    total_count="$(printf '%s\n' "$pods_json" | jq '.items | length')"
    new_ready_count="$(
      printf '%s\n' "$pods_json" |
        jq --arg old_uid "$DRILL_OLD_UID" '
          [
            .items[]
            | select(.metadata.uid != $old_uid)
            | select(.metadata.deletionTimestamp == null)
            | select(any(.status.conditions[]?;
                .type == "Ready" and .status == "True"))
          ] | length
        '
    )"
    if test "$total_count" -eq 1 && test "$new_ready_count" -eq 1; then
      export DRILL_REPLACEMENT_POD="$(
        printf '%s\n' "$pods_json" | jq -r '.items[0].metadata.name'
      )"
      export DRILL_REPLACEMENT_UID="$(
        printf '%s\n' "$pods_json" | jq -r '.items[0].metadata.uid'
      )"
      return 0
    fi
    sleep 2
  done
  printf '%s\n' 'NO-GO: no unique different Ready replacement Pod appeared' >&2
  return 1
}

export DRILL_POD_COUNT="$(
  kubectl get pod -n "$DRILL_NAMESPACE" -l "$DRILL_SELECTOR" -o json |
    jq '.items | length'
)"
test "$DRILL_POD_COUNT" -eq 1
export DRILL_POD="$(
  kubectl get pod -n "$DRILL_NAMESPACE" -l "$DRILL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
export DRILL_OLD_UID="$(
  kubectl get pod "$DRILL_POD" -n "$DRILL_NAMESPACE" \
    -o jsonpath='{.metadata.uid}'
)"
kubectl get pod "$DRILL_POD" -n "$DRILL_NAMESPACE" \
  -o jsonpath='{.metadata.namespace}/{.metadata.name}{" uid="}{.metadata.uid}{" image="}{.spec.containers[?(@.name=="slizen")].image}{"\n"}'

export DRILL_STARTED_AT="$(date +%s)"
test "$RUN_DESTRUCTIVE_DRILLS" = yes
kubectl delete pod "$DRILL_POD" -n "$DRILL_NAMESPACE"
wait_for_new_ready_drill_pod
export DRILL_SECONDS="$(( $(date +%s) - DRILL_STARTED_AT ))"
printf 'graceful replacement pod=%s uid=%s seconds=%s\n' \
  "$DRILL_REPLACEMENT_POD" "$DRILL_REPLACEMENT_UID" "$DRILL_SECONDS"
```

Run the application smoke again and record connection errors, active
connections (or the v0.2.2 platform fallback), readiness transitions, drain
logs, and recovery time. A forced/deadline-reached drain, an ambiguous write
handled as definitely failed, or a threshold breach is a no-go.

### Abrupt process-equivalent crash

Only an approved force deletion of the already identified isolated disposable
canary is in scope. It bypasses graceful shutdown and, for a sidecar, kills the
application container too. Never run it against a shared or production Pod.
Record the service/platform owner's approval and resolve a fresh exact target:

```sh
export DRILL_POD_COUNT="$(
  kubectl get pod -n "$DRILL_NAMESPACE" -l "$DRILL_SELECTOR" -o json |
    jq '.items | length'
)"
test "$DRILL_POD_COUNT" -eq 1
export DRILL_POD="$(
  kubectl get pod -n "$DRILL_NAMESPACE" -l "$DRILL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
export DRILL_OLD_UID="$(
  kubectl get pod "$DRILL_POD" -n "$DRILL_NAMESPACE" \
    -o jsonpath='{.metadata.uid}'
)"
kubectl get pod "$DRILL_POD" -n "$DRILL_NAMESPACE" \
  -o jsonpath='{.metadata.namespace}/{.metadata.name}{" uid="}{.metadata.uid}{"\n"}'

export DRILL_STARTED_AT="$(date +%s)"
test "$RUN_DESTRUCTIVE_DRILLS" = yes
kubectl delete pod "$DRILL_POD" -n "$DRILL_NAMESPACE" \
  --grace-period=0 --force
wait_for_new_ready_drill_pod
export DRILL_SECONDS="$(( $(date +%s) - DRILL_STARTED_AT ))"
printf 'abrupt replacement pod=%s uid=%s seconds=%s\n' \
  "$DRILL_REPLACEMENT_POD" "$DRILL_REPLACEMENT_UID" "$DRILL_SECONDS"
```

Run the application smoke and record connection loss/reconnect, errors,
readiness, new-Pod identity, and recovery time. If force deletion is not
permitted, record this drill as a named **Partial** with owner/date; do not
simulate a crash on a less isolated target.

### Standalone origin outage and recovery

This temporary policy selects only the standalone Slizen Pod and denies its
egress. Kubernetes NetworkPolicies are additive: an existing egress allow rule
can keep the origin reachable. Inspect existing policies and replace the example
labels with the exact Deployment selector. The drill is valid only if
`slizenctl readyz` actually fails after injection and recovers after cleanup.
Keep routed drill traffic read-only during the outage; perform disposable
write/value validation before injection and after recovery, not across the
ambiguous interval.

```sh
export OUTAGE_POLICY=slizen-origin-outage-drill
export SLIZEN_POD_COUNT="$(
  kubectl get pod -n "$SLIZEN_NAMESPACE" \
    -l app.kubernetes.io/instance="$SLIZEN_RELEASE" -o json |
    jq '.items | length'
)"
test "$SLIZEN_POD_COUNT" -eq 1
export SLIZEN_POD="$(
  kubectl get pod -n "$SLIZEN_NAMESPACE" \
    -l app.kubernetes.io/instance="$SLIZEN_RELEASE" \
    -o jsonpath='{.items[0].metadata.name}'
)"

cleanup_slizen_origin_drill() {
  kubectl delete networkpolicy "$OUTAGE_POLICY" \
    -n "$SLIZEN_NAMESPACE" --ignore-not-found
}
trap cleanup_slizen_origin_drill EXIT INT TERM

kubectl get networkpolicy -n "$SLIZEN_NAMESPACE"
test "$RUN_DESTRUCTIVE_DRILLS" = yes
kubectl apply -n "$SLIZEN_NAMESPACE" -f - <<EOF
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: ${OUTAGE_POLICY}
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: slizen
      app.kubernetes.io/instance: ${SLIZEN_RELEASE}
  policyTypes:
    - Egress
  egress: []
EOF

if ! kubectl wait --for=condition=Ready=false pod/"$SLIZEN_POD" \
  -n "$SLIZEN_NAMESPACE" --timeout=30s
then
  printf '%s\n' 'NO-GO: Pod readiness did not reflect the outage' >&2
  exit 1
fi
if kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- \
  slizenctl readyz
then
  printf '%s\n' 'NO-GO: outage injector did not block the origin' >&2
  exit 1
fi

# Capture expected readiness/application/upstream-error evidence immediately.
cleanup_slizen_origin_drill
trap - EXIT INT TERM

if ! kubectl wait --for=condition=Ready pod/"$SLIZEN_POD" \
  -n "$SLIZEN_NAMESPACE" --timeout=2m
then
  printf '%s\n' 'NO-GO: readiness did not recover; restore the direct endpoint' >&2
  exit 1
fi
kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- slizenctl readyz
```

After cleanup, require the application smoke to pass, upstream errors to stop
increasing, and errors/latency to return to baseline. Preserve the injected and
recovered readiness timestamps. The `EXIT` trap is a guard, not a substitute
for the explicit delete; confirm the policy no longer exists before leaving the
window.

Do not reuse this NetworkPolicy recipe for a sidecar: all containers in the Pod
share one network namespace, so it would also cut application egress and would
not isolate Slizen's origin path. A sidecar origin-outage drill needs a
service-owned, identity-scoped test proxy/ACL or platform fault injector with
equally explicit cleanup. Without one, record that drill as **Partial**, not
Pass.

## 6. Promote exactly one prefix

Choose one read-heavy, disposable prefix with an agreed staleness budget. Keep
an empty-prefix `observe` catch-all; otherwise unmatched keys inherit global
`cache`.

For a multi-replica sidecar Deployment, stop here until the service owner has
recorded one of the consistency conditions from “Choose one deployment
pattern”: a single routed replica, an operationally read-only prefix, or
acceptance of TTL-bounded cross-sidecar staleness for every write path. This is
a correctness gate, not an optimization preference. A single-replica canary
must prevent old/new cache overlap during rollout, for example with a reviewed
`Recreate` strategy; the raw observe example's `maxSurge: 1` is not sufficient
for that cache condition.

Standalone Helm cache overlay:

```yaml
mode: cache
cache:
  allowStaleOnUpstreamError: false
  policies:
    - prefix: ""
      mode: observe
    - prefix: "catalog:public:"
      mode: cache
      maxItemBytes: 262144
      maxLocalTTL: 5s
```

Store that overlay in the reviewed configuration system. Do not use
`--reuse-values`: live release state cannot reproduce the exact render. Supply
the original environment values and the cache overlay explicitly, hash both,
and validate the exact same inputs before the bounded atomic upgrade:

```sh
export CACHE_VALUES_FILE=/path/to/reviewed/slizen-one-prefix.yaml
test -z "$(git status --porcelain --untracked-files=all)"
test "$(git rev-parse HEAD)" = "$CHART_COMMIT"
test -n "$(git branch -r --contains "$CHART_COMMIT")"
export CURRENT_STAGING_VALUES_SHA256="$(
  checksum256 "$STAGING_VALUES_FILE" | awk '{print $1}'
)"
test "$CURRENT_STAGING_VALUES_SHA256" = "$STAGING_VALUES_SHA256"
export CACHE_VALUES_SHA256="$(
  checksum256 "$CACHE_VALUES_FILE" | awk '{print $1}'
)"
printf 'base_values_sha256=%s\ncache_values_sha256=%s\n' \
  "$STAGING_VALUES_SHA256" "$CACHE_VALUES_SHA256"

helm lint ./charts/slizen \
  -f "$STAGING_VALUES_FILE" \
  -f "$CACHE_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string image.digest="$STABLE_DIGEST"

helm template "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" \
  -f "$STAGING_VALUES_FILE" \
  -f "$CACHE_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string image.digest="$STABLE_DIGEST" |
  kubectl apply --dry-run=server -n "$SLIZEN_NAMESPACE" -f -

export CACHE_RENDER_SHA256="$(
  helm template "$SLIZEN_RELEASE" ./charts/slizen \
    --namespace "$SLIZEN_NAMESPACE" \
    -f "$STAGING_VALUES_FILE" \
    -f "$CACHE_VALUES_FILE" \
    --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
    --set-string image.digest="$STABLE_DIGEST" |
    checksum256 |
    awk '{print $1}'
)"
printf 'cache_render_sha256=%s\n' "$CACHE_RENDER_SHA256"

helm upgrade "$SLIZEN_RELEASE" ./charts/slizen \
  --namespace "$SLIZEN_NAMESPACE" \
  -f "$STAGING_VALUES_FILE" \
  -f "$CACHE_VALUES_FILE" \
  --set-string upstream.address="$ORIGIN_HOST:$ORIGIN_PORT" \
  --set-string image.digest="$STABLE_DIGEST" \
  --atomic --timeout 5m
kubectl rollout status deployment/"$SLIZEN_DEPLOYMENT" \
  -n "$SLIZEN_NAMESPACE" --timeout=2m

export LIVE_SLIZEN_IMAGE="$(
  kubectl get deployment "$SLIZEN_DEPLOYMENT" -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)"
test "$LIVE_SLIZEN_IMAGE" = "ghcr.io/slizendb/slizen@$STABLE_DIGEST"
export LIVE_SLIZEN_CONFIG="$(
  kubectl get configmap "$SLIZEN_DEPLOYMENT" -n "$SLIZEN_NAMESPACE" \
    -o jsonpath='{.data.slizen\.toml}'
)"
printf '%s\n' "$LIVE_SLIZEN_CONFIG" | grep -Fx 'mode = "cache"'
printf '%s\n' "$LIVE_SLIZEN_CONFIG" | grep -Fx 'prefix = ""'
printf '%s\n' "$LIVE_SLIZEN_CONFIG" | grep -Fx 'prefix = "catalog:public:"'
printf '%s\n' "$LIVE_SLIZEN_CONFIG" | grep -Fx 'mode = "observe"'
export SLIZEN_POD="$(
  kubectl get pod -n "$SLIZEN_NAMESPACE" \
    -l app.kubernetes.io/instance="$SLIZEN_RELEASE" \
    -o jsonpath='{.items[0].metadata.name}'
)"
export CACHE_RUNTIME_STATUS="$(
  kubectl exec -n "$SLIZEN_NAMESPACE" "$SLIZEN_POD" -c slizen -- \
    slizenctl status
)"
printf '%s\n' "$CACHE_RUNTIME_STATUS" |
  jq -e --arg version "$STABLE_VERSION" --arg commit "$STABLE_COMMIT" '
    .version == $version
    and .commit == $commit
    and .mode == "cache"
    and .upstream_status == "up"
  '
```

Because the chart uses `Recreate`, this policy change has the same connection
interruption as an image upgrade.

Equivalent sidecar TOML:

```toml
mode = "cache"

[[cache.policies]]
prefix = ""
mode = "observe"

[[cache.policies]]
prefix = "catalog:public:"
mode = "cache"
max_item_bytes = 262144
max_local_ttl = "5s"
```

For a sidecar, put the ConfigMap change and Pod-template annotation bump in the
same reviewed application-source commit, verify its clean pushed identity and
server-side dry-run as in step 3B, and let the normal deployment controller
roll it out. The annotation value should be a deterministic source/config
revision, not an unrelated timestamp.

```sh
export SIDECAR_CONFIG_SOURCE_FILE=/path/to/reviewed/slizen.toml
git ls-files --error-unmatch "$SIDECAR_CONFIG_SOURCE_FILE" >/dev/null
git ls-files --error-unmatch "$SIDECAR_MANIFEST" >/dev/null
export SIDECAR_CONFIG_SOURCE_CONTENT="$(<"$SIDECAR_CONFIG_SOURCE_FILE")"
export CONFIG_REVISION="$(
  printf '%s' "$SIDECAR_CONFIG_SOURCE_CONTENT" |
    checksum256 |
    awk '{print $1}'
)"
# Set metadata.annotations["slizen.dev/config-revision"] to CONFIG_REVISION in
# the reviewed parent manifest before committing and applying it.
test -z "$(git status --porcelain --untracked-files=all)"
export CACHE_SIDECAR_SOURCE_COMMIT="$(git rev-parse HEAD)"
git fetch --prune origin
test -n "$(git branch -r --contains "$CACHE_SIDECAR_SOURCE_COMMIT")"
kubectl apply --dry-run=server -n "$APP_NAMESPACE" -f "$SIDECAR_MANIFEST"
case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    /path/to/sync-reviewed-application-revision \
      "$CACHE_SIDECAR_SOURCE_COMMIT"
    ;;
  direct)
    test "${SIDECAR_DIRECT_MUTATION_APPROVED:-}" = yes
    kubectl apply -n "$APP_NAMESPACE" -f "$SIDECAR_MANIFEST"
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac

wait_for_deployment_config_revision "$CONFIG_REVISION"
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=5m
test "$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)" = "ghcr.io/slizendb/slizen@$STABLE_DIGEST"
export LIVE_SIDECAR_CONFIG="$(
  kubectl get configmap "$SIDECAR_CONFIGMAP" -n "$APP_NAMESPACE" \
    -o jsonpath='{.data.slizen\.toml}'
)"
test "$(
  printf '%s' "$LIVE_SIDECAR_CONFIG" | checksum256 | awk '{print $1}'
)" = "$CONFIG_REVISION"
kubectl diff -n "$APP_NAMESPACE" -f "$SIDECAR_MANIFEST"

export APP_POD="$(
  kubectl get pod -n "$APP_NAMESPACE" -l "$APP_LABEL_SELECTOR" \
    -o jsonpath='{.items[0].metadata.name}'
)"
export CACHE_RUNTIME_STATUS="$(
  kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c slizen -- slizenctl status
)"
printf '%s\n' "$CACHE_RUNTIME_STATUS" |
  jq -e '.mode == "cache" and .upstream_status == "up"'

# Replace with a value-validating read of a disposable key under the exact
# promoted prefix, then prove the effective policy in a complete bounded audit.
kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c "$APP_CONTAINER" -- \
  /path/to/application-selected-prefix-smoke
export CACHE_AUDIT="$(
  kubectl exec -n "$APP_NAMESPACE" "$APP_POD" -c slizen -- \
    slizenctl audit --limit 1000
)"
printf '%s\n' "$CACHE_AUDIT" |
  jq -e '
    .mode == "cache"
    and .telemetry_complete == true
    and .truncated == false
    and any(.entries[]; .effective_policy_mode == "cache")
  '
```

Only a Deployment explicitly not managed by GitOps may use a direct patch, and
then only from the same clean pushed source after a server-side dry-run. A
standalone annotation patch does not write the TOML and is not a valid cache
promotion. The ConfigMap-content hash, live annotation, image digest, runtime
mode, selected-prefix smoke, and effective-policy audit are all mandatory
postconditions; `rollout status` by itself is not evidence that caching began.

Run the one-prefix soak. Require zero mismatches, zero unexpected cached
prefixes, and all safety budgets to pass. Compare the Redis/Valkey
`cmdstat_get:calls` or equivalent exporter delta only for the selected,
attributable workload/prefix. The Slizen logical upstream-call panel is useful
for diagnosis but cannot prove physical origin reduction under retries.
Missing the benefit target means return that prefix to `observe`; it is not
evidence that a wider rollout will work.

## 7. Canary and expand gradually

After the isolated prefix passes:

1. route a small canary application slice to Slizen while the control remains
   direct to origin;
2. compare application errors, p95/p99, origin-side GET command volume, Slizen
   logical upstream calls/errors, memory, connections, evictions, readiness,
   restarts, and value validation;
3. hold for the pre-agreed canary soak, then record a go/no-go decision;
4. expand traffic or add one prefix at a time, for example 10%, 25%, 50%, and
   100%, with a complete observation window at every step;
5. stop at the smallest useful scope. A safe no-benefit result is valid.

For standalone Slizen, canary at the application/router layer. Do not increase
Slizen replica count. For sidecar, use a separate canary Deployment or the
platform's normal traffic-splitting mechanism; do not edit a random subset of
Pods by hand.

Stop and roll back immediately on any value mismatch, unsupported command,
unexplained error, readiness flap, restart/OOM, ambiguous behavior outside the
documented contract, unexpected origin amplification, or threshold breach.

## Endpoint-first rollback and cleanup

Rollback order is mandatory:

1. restore the application's recorded direct Redis/Valkey endpoint;
2. wait for the application rollout and verify direct `PING`, representative
   `GET`, and application health;
3. confirm errors and latency returned to the accepted direct baseline;
4. only then downgrade, scale down, uninstall, or remove Slizen.

Standalone Helm cleanup after the endpoint is direct:

```sh
if test -n "$PREVIOUS_HELM_REVISION"; then
  helm rollback "$SLIZEN_RELEASE" "$PREVIOUS_HELM_REVISION" \
    --namespace "$SLIZEN_NAMESPACE" --wait --timeout 5m
else
  helm uninstall "$SLIZEN_RELEASE" \
    --namespace "$SLIZEN_NAMESPACE" --timeout 5m
fi
```

`helm rollback` has no `--atomic` flag; `--wait --timeout` bounds the rollback.
It also uses `Recreate`, so it closes RESP connections. At this point clients
must already be healthy on the direct endpoint.

Sidecar removal after the application endpoint has been restored uses the
service-owned pre-Slizen source. A direct `rollout undo` is allowed only for an
explicitly non-GitOps-managed Deployment and only when Deployment-level
strategy/replica fields did not change:

```sh
case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    /path/to/restore-reviewed-application-revision "$ROLLBACK_SOURCE_REFERENCE"
    ;;
  direct)
    test "${APP_DIRECT_MUTATION_APPROVED:-}" = yes
    kubectl rollout undo deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      --to-revision="$ORIGINAL_APP_REVISION"
    ;;
  *)
    printf '%s\n' 'NO-GO: APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
    exit 1
    ;;
esac
export SIDECAR_REMOVAL_DEADLINE="$(( $(date +%s) + 300 ))"
while test "$(
    kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" -o json |
      jq '[.spec.template.spec.containers[] | select(.name == "slizen")] | length'
  )" -ne 0
do
  test "$(date +%s)" -lt "$SIDECAR_REMOVAL_DEADLINE"
  sleep 2
done
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=5m
```

`PREVIOUS_SIDECAR_REVISION` is only an explicitly inspected, same-routing
image-downgrade target during the sidecar trial; it is not the final
Slizen-removal target. Never use it after restoring the direct endpoint unless
that exact revision was inspected and also preserves the direct endpoint.
`rollout undo` does not restore `spec.strategy` or `spec.replicas`. After a cache-stage
`Recreate` or replica-count change, use the pre-recorded service-owned
GitOps/full-manifest pre-Slizen revert and verify the live strategy JSON plus
replica count match `ORIGINAL_APP_STRATEGY_JSON` and
`ORIGINAL_APP_REPLICAS`. Prefer reverting the complete parent workload revision
so the endpoint, container list, image, ConfigMap reference, annotation, rollout
strategy, and replica count return together. Run the direct-origin smoke again
after this final removal.
Preserve Slizen logs, Kubernetes events, anonymized audit output, thresholds,
and timestamps for diagnosis. Never attach Redis values, credentials, raw
sensitive keys, or Secret contents.
