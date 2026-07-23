# Kubernetes packaging

`observe-sidecar.yaml` is a concrete, runnable parent Deployment containing an
example Redis client and a Slizen sidecar. It is a pattern, not an injector:
replace `example-app` with the real application, preserve the Slizen container
and ConfigMap, and change the application's Redis endpoint to
`127.0.0.1:6380`. The admin API is loopback-only and has no Service.
Because the config uses a `subPath` mount, bump the Pod-template annotation
`slizen.dev/config-revision` on every configuration or policy change.

Helm cannot mutate an existing Deployment. `charts/slizen` instead deploys a
standalone, cluster-internal proxy Service. Slizen v0.2 does not ship an
Operator, admission webhook, or automatic sidecar injection.

v0.2 has no built-in downstream `AUTH`/TLS or upstream Redis/Valkey TLS. Keep
both plaintext paths private. A TLS-required origin needs a separately reviewed
external local termination/tunnel or is not compatible with this release.
The upstream must also be one standalone-compatible address; v0.2 does not
follow Redis Cluster redirections or discover Sentinel failover.
An application client that normally authenticates to Redis needs a separate
Slizen-facing profile with downstream credentials/TLS disabled; configure the
origin identity on Slizen and test the exact library initialization.
For an authenticated origin, provision an application-specific Secret through
the platform secret manager and add required refs to the Slizen container:

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

Both keys are required so a typo fails Pod startup instead of silently using a
different Redis identity; use an empty username value for the default user.
Omit the entire env block when origin has no authentication.

The sidecar admin listener is deliberately bound to `127.0.0.1:9090`. A
Prometheus server in another Pod cannot scrape it. Continuous monitoring for a
sidecar trial therefore requires an existing per-Pod agent or monitoring
sidecar that scrapes localhost. `kubectl port-forward` is useful for manual
inspection, but it does not satisfy a soak-window monitoring gate. If the
platform cannot scrape localhost, remain in `observe` and use the standalone
chart with explicitly restricted `networkPolicy.metricsIngressPeers`, or
design and review an equivalent protected admin path before enabling cache.

The raw example uses `RollingUpdate` with surge because it is an `observe`
canary. Do not carry that strategy into cache mode: overlapping Pods have
independent local caches and cannot invalidate one another. A cache-mode
sidecar trial must keep one active application replica with a `Recreate`
strategy, or explicitly accept the bounded cross-Pod staleness risk before
routing writes.

The stable public image is v0.2.2:

```text
ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627
```

v0.2.3-rc.1 is published for staging trials at:

```text
ghcr.io/slizendb/slizen@sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a
```

Use the release-bound raw sidecar asset from the
[GitHub prerelease](https://github.com/slizendb/slizen/releases/tag/v0.2.3-rc.1);
do not apply a floating tag.

Follow [the staging runbook](../../docs/STAGING_ROLLOUT.md) for compatibility,
thresholds, soak windows, canary expansion, and the mandatory endpoint-first
rollback. Read [failure modes](../../docs/FAILURE_MODES.md) and record the
[self-service gate](../../docs/STAGING_RELEASE_GATE.md). Do not apply the
example unchanged: set the upstream address, replace the example application,
and pin the Slizen image to the verified stable digest first.

## Sidecar upgrade

Changing any sidecar field rolls the entire parent Pod. The application
restarts, its loopback connection closes, and in-flight operations can fail. A
write without a received response is ambiguous.

Capture the current parent revision and sidecar image before changing the Pod
template. These are only a same-routing image-downgrade target: the revision
normally still points the application at loopback and is not a direct-origin
rollback target. Run the upgrade and rollback commands below from one dedicated
fail-fast Bash session.

```sh
bash
set -Eeuo pipefail

export APP_NAMESPACE=app-staging
export APP_DEPLOYMENT=catalog-api
export APP_CONTAINER=app

export PREVIOUS_SIDECAR_REVISION="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.metadata.annotations.deployment\.kubernetes\.io/revision}'
)"
export PREVIOUS_SIDECAR_IMAGE="$(
  kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
    -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
)"

printf 'same_routing_revision=%s\nsidecar_image=%s\n' \
  "$PREVIOUS_SIDECAR_REVISION" "$PREVIOUS_SIDECAR_IMAGE"
```

Separately record and inspect the service-owned pre-Slizen GitOps/full-manifest
revision, ConfigMap/Secret reference, or other configuration source that restores
the complete direct-origin client profile and removes the sidecar. Never capture
or expand a credential-bearing Redis URI in a shell command. Do not continue
without a tested direct-origin restoration path.

After updating a clean pushed manifest in the normal deployment source, let the
GitOps/deployment controller apply it and assert the expected live digest before
waiting for rollout. Do not also mutate that Deployment with `kubectl`.

For a reviewed image-only change on an explicitly non-GitOps-managed
Deployment:

```sh
set -Eeuo pipefail
export NEW_SLIZEN_DIGEST=sha256:REPLACE_WITH_A_VERIFIED_PUBLISHED_DIGEST
test "${SIDECAR_DIRECT_MUTATION_APPROVED:-}" = yes
kubectl set image deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
  slizen="ghcr.io/slizendb/slizen@$NEW_SLIZEN_DIGEST"
export SIDECAR_IMAGE_WAIT_DEADLINE="$(( $(date +%s) + 300 ))"
while ! test "$(
    kubectl get deployment "$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}'
  )" = "ghcr.io/slizendb/slizen@$NEW_SLIZEN_DIGEST"
do
  test "$(date +%s)" -lt "$SIDECAR_IMAGE_WAIT_DEADLINE"
  sleep 2
done
kubectl rollout status deployment/"$APP_DEPLOYMENT" \
  -n "$APP_NAMESPACE" --timeout=5m
```

The staging runbook adds a bounded wait, clean-source identity, server dry-run,
runtime version/commit/mode assertion, and live config revision check. This
short example is not a substitute for that gate.

## Endpoint-first sidecar rollback

The preferred rollback target is the inspected service-owned source revision
from before Slizen. It must restore the direct Redis/Valkey client profile and
remove the sidecar together. A GitOps-managed Deployment must be restored
through that controller; direct `rollout undo` is allowed only for an explicitly
non-GitOps-managed Deployment whose external config, strategy, and replica
count have not changed:

```sh
set -Eeuo pipefail
export APP_DEPLOYMENT_CONTROL=REPLACE_WITH_gitops_OR_direct
export ROLLBACK_SOURCE_REFERENCE=
export ORIGINAL_APP_REVISION=REPLACE_WITH_INSPECTED_PRE_SLIZEN_REVISION
case "$APP_DEPLOYMENT_CONTROL" in
  gitops)
    test -n "$ROLLBACK_SOURCE_REFERENCE"
    /path/to/restore-reviewed-application-revision \
      "$ROLLBACK_SOURCE_REFERENCE"
    ;;
  direct)
    test "${APP_DIRECT_MUTATION_APPROVED:-}" = yes
    kubectl rollout undo deployment/"$APP_DEPLOYMENT" -n "$APP_NAMESPACE" \
      --to-revision="$ORIGINAL_APP_REVISION"
    ;;
  *)
    printf '%s\n' 'APP_DEPLOYMENT_CONTROL must be gitops or direct' >&2
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
  -n "$APP_NAMESPACE" --timeout=4m
kubectl exec -n "$APP_NAMESPACE" deployment/"$APP_DEPLOYMENT" \
  -c "$APP_CONTAINER" -- \
  /path/to/application-direct-origin-smoke
```

Verify the application is healthy directly against origin before any separate
Slizen cleanup. Never use `PREVIOUS_SIDECAR_REVISION` for final cleanup: it is
normally a sidecar revision whose application endpoint remains loopback. It may
be used only as an explicitly inspected same-routing downgrade while the old
sidecar path is still accepted. For endpoint-first rollback and final removal,
restore the pre-Slizen service-owned revision above and prove direct-origin
health. The complete staging runbook also captures and verifies the original
strategy/replica fields and handles credential-bearing endpoints without shell
expansion; follow it for any real trial.
