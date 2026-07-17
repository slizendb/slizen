# Slizen Helm chart

This chart deploys Slizen as a cluster-internal Redis/Valkey proxy. It defaults
to `observe` mode, exposes only the RESP port, and keeps the admin API on Pod
loopback. Redis credentials are referenced from an existing Secret and are
never generated or stored in chart values.

Slizen v0.2 is single-node. The chart enforces exactly one replica; running
independent caches behind one Service would create invalidation gaps.
The default image location is `ghcr.io/slizendb/slizen`; verify the requested
release exists or override `image.repository` and `image.tag`, and pin
`image.digest` for an actual rollout.

Helm cannot inject a sidecar into an existing Deployment. Use
`deploy/kubernetes/observe-sidecar.yaml` as the concrete sidecar pattern, or
deploy this chart and point a canary workload at the chart's ClusterIP Service.
There is no admission webhook or Operator in v0.2.

Read `docs/STAGING_ROLLOUT.md` before changing a client endpoint. At minimum:

```sh
helm upgrade --install slizen ./charts/slizen \
  --namespace slizen-staging --create-namespace \
  --set-string upstream.address=redis.default.svc.cluster.local:6379
```

For stable anonymized key IDs, set `privacy.existingSecret.name`. Without it,
Slizen safely uses the Pod UID as the HMAC key, so IDs change after restarts.

Prometheus support is opt-in because Slizen currently serves metrics and admin
routes on one listener. Enabling it requires `admin.listen=0.0.0.0:9090` and
`admin.allowNetworkAccess=true`; the metrics ClusterIP can therefore also reach
the admin routes. Apply a namespace-specific NetworkPolicy or an authenticated
metrics proxy supplied by your platform. `metrics.serviceMonitor.enabled=true`
additionally renders a ServiceMonitor and requires the Prometheus Operator CRD.
With the default `false`, the chart has no CRD dependency and no admin Service.
