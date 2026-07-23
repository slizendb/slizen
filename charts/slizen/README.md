# Slizen Helm chart

This chart deploys Slizen as a cluster-internal Redis/Valkey proxy. It defaults
to `observe` mode, exposes only the RESP port, and keeps the admin API on Pod
loopback. Redis credentials are referenced from an existing Secret and are
never generated or stored in chart values.

Slizen v0.2 is single-node. The chart enforces exactly one replica; running
independent caches behind one Service would create invalidation gaps.
Slizen does not authenticate downstream RESP clients. The chart therefore
enables a deny-by-default NetworkPolicy: neither RESP nor admin traffic is
admitted until the operator explicitly configures the corresponding peers.
The upstream client also has no Redis/Valkey TLS support in v0.2. Use only a
private plaintext origin path or a separately reviewed external tunnel.
It connects to one standalone address and does not implement Cluster
redirections or Sentinel discovery/failover.
Applications that normally authenticate to Redis need a separate
Slizen-facing client profile with downstream `AUTH` and TLS disabled; configure
origin credentials through `upstream.existingSecret` and test the exact client
library handshake before routing. Once `upstream.existingSecret.name` is set,
both configured keys are required so Pod startup fails closed; store an empty
username for Redis's default user.

The stable public image is v0.2.2:

```text
ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627
```

v0.2.3 is a source-tree release candidate, not a published image. Never derive
or guess its digest. Verify a newer release exists before changing
`image.digest`.

The source chart has candidate `version: 0.2.3`, but its `appVersion`, default
image tag, digest, and rendered application label intentionally remain
`0.2.2`. When v0.2.3 is published, release closure must update all four runtime
identity surfaces to the verified v0.2.3 artifact together.

Helm cannot inject a sidecar into an existing Deployment. Use
`deploy/kubernetes/observe-sidecar.yaml` as the concrete sidecar pattern, or
deploy this chart and point a canary workload at the chart's ClusterIP Service.
There is no admission webhook or Operator in v0.2.

## Install or upgrade

Use the [30-minute observe install](../../docs/STAGING_QUICKSTART.md) for the
shortest unrouted first deployment. Read [the staging
runbook](../../docs/STAGING_ROLLOUT.md), [failure-mode
contract](../../docs/FAILURE_MODES.md), and [self-service
gate](../../docs/STAGING_RELEASE_GATE.md) before changing a client endpoint.

Capture any current Helm revision and image first:

```sh
export SLIZEN_NAMESPACE=slizen-staging
export SLIZEN_RELEASE=slizen
export SLIZEN_DEPLOYMENT=slizen
export STABLE_DIGEST=sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627

helm history "$SLIZEN_RELEASE" -n "$SLIZEN_NAMESPACE"
kubectl get deployment "$SLIZEN_DEPLOYMENT" -n "$SLIZEN_NAMESPACE" \
  -o jsonpath='{.spec.template.spec.containers[?(@.name=="slizen")].image}{"\n"}'
```

The first command is expected to report "release not found" on an initial
install. The block below is verified with Helm 3.18.4 and deliberately uses
`--atomic --timeout`. If the platform supplies another Helm version, verify its
failure and wait semantics before the change window. Render, validate, and
install `observe` with the immutable stable image. Copy the bundled staging
example to a reviewed location, then replace its upstream, namespace, and Pod
label selectors with the exact staging identities. An empty
`redisIngressPeers` list intentionally makes the Service unreachable.

```sh
export CHART_REF=./charts/slizen
export REVIEWED_VALUES=/private/path/slizen-staging-values.yaml
cp "$CHART_REF/examples/staging-values.yaml" "$REVIEWED_VALUES"

helm lint "$CHART_REF" \
  -f "$REVIEWED_VALUES" \
  --set-string mode=observe \
  --set-string image.digest="$STABLE_DIGEST"

helm upgrade --install "$SLIZEN_RELEASE" "$CHART_REF" \
  --namespace "$SLIZEN_NAMESPACE" --create-namespace \
  --atomic --timeout 5m \
  -f "$REVIEWED_VALUES" \
  --set-string mode=observe \
  --set-string image.digest="$STABLE_DIGEST"

kubectl rollout status deployment/"$SLIZEN_DEPLOYMENT" \
  -n "$SLIZEN_NAMESPACE" --timeout=2m
```

Before routing, confirm the cluster CNI enforces Kubernetes NetworkPolicy.
Verify the labelled staging application can connect and an otherwise identical
unlabelled Pod cannot. Treat a connection from an unlisted Pod as a no-go:
Slizen v0.2 has no downstream `AUTH` boundary. Disabling
`networkPolicy.enabled` is safe only when an equivalent, already-enforced
platform policy selects this Pod.

The chart deliberately uses Deployment strategy `Recreate`. An upgrade or Helm
rollback stops the old Pod before the new Pod becomes ready. The Service has no
ready endpoint during that interval, existing RESP connections close, and
in-flight requests can fail. A write without a received response is ambiguous.
`--atomic` restores Helm state after a failed upgrade; it is not a zero-downtime
mechanism and cannot preserve open connections.

For stable anonymized key IDs, set `privacy.existingSecret.name`. Without it,
Slizen generates a cryptographically random process-local HMAC secret; the
secret is never logged and IDs intentionally change after restarts.

Prometheus support is opt-in because Slizen currently serves metrics and admin
routes on one listener. Enabling it requires `admin.listen=0.0.0.0:9090` and
`admin.allowNetworkAccess=true`; the metrics ClusterIP can therefore also reach
the admin routes. Set `networkPolicy.metricsIngressPeers` to only the exact
Prometheus or platform-scraper identities; an empty list denies access.
`metrics.serviceMonitor.enabled=true` additionally renders a ServiceMonitor and
requires the Prometheus Operator CRD. With the default `false`, the chart has no
CRD dependency and no admin Service.

## Endpoint-first rollback

Record the original application Redis/Valkey endpoint and deployment revision
before routing traffic. On any no-go signal:

1. restore the application endpoint to the origin;
2. wait for the application rollout and verify direct-origin health;
3. only then roll Helm back or uninstall Slizen.

After clients are confirmed healthy on the direct endpoint:

```sh
export PREVIOUS_HELM_REVISION=REPLACE_WITH_RECORDED_REVISION

helm rollback "$SLIZEN_RELEASE" "$PREVIOUS_HELM_REVISION" \
  --namespace "$SLIZEN_NAMESPACE" --wait --timeout 5m
```

If the trial was the first install and there is no previous revision:

```sh
helm uninstall "$SLIZEN_RELEASE" \
  --namespace "$SLIZEN_NAMESPACE" --timeout 5m
```

`helm rollback` has no `--atomic` flag. Its `--wait --timeout` bounds the
operation, and `Recreate` still closes connections; this is why endpoint
restoration comes first.
