# Staging rollout and rollback

Slizen v0.2 is packaged for a reversible staging trial. Start in `observe`
mode: Redis or Valkey remains the source of truth, every request is forwarded,
and Slizen does not store or serve local values.

## Choose one deployment pattern

- **Sidecar:** copy `deploy/kubernetes/observe-sidecar.yaml` into the parent
  workload, replace `example-app`, and point that application at
  `127.0.0.1:6380`. A Pod shares its network namespace, so no proxy Service is
  needed.
- **Standalone proxy:** install `charts/slizen` and point one canary workload at
  its ClusterIP Service. This is simpler when several workloads share an
  upstream.

Helm cannot inject a sidecar into an existing Deployment. The chart deploys a
standalone proxy; it is not an Operator or admission webhook.
Slizen v0.2 is single-node, so the chart enforces one replica. Do not manually
scale it behind the Service: replicas would have independent cache and
invalidation state.

## 1. Record the rollback target

Before changing anything, record:

1. the exact original Redis/Valkey host, port, database, and TLS/auth settings;
2. the workload revision and configuration source which holds that endpoint;
3. baseline error rate, Redis request rate, application p95/p99 latency, and
   connection count;
4. the owner who can revert the endpoint and the rollback command for that
   workload.

Keep the original endpoint in the deployment system. Do not overwrite the only
copy with `127.0.0.1:6380` or the Slizen Service name.

## 2. Check compatibility before routing traffic

Slizen supports a deliberately small command set. Compare every command used by
the canary with `docs/REDIS_COMPATIBILITY.md`. In particular, v0.2 requires
database 0 and does not support transactions, Pub/Sub, blocking commands,
arbitrary data structures, or transparent TLS termination.

Run the application's integration suite against Slizen in `observe` mode. Also
exercise a disposable staging key through Slizen and verify the result at the
origin:

```sh
redis-cli -h SLIZEN_HOST -p 6380 SET slizen:staging:smoke ok EX 30
redis-cli -h SLIZEN_HOST -p 6380 GET slizen:staging:smoke
redis-cli -h ORIGIN_HOST -p 6379 GET slizen:staging:smoke
redis-cli -h SLIZEN_HOST -p 6380 DEL slizen:staging:smoke
```

Do not proceed if the client depends on an unlisted command, a non-zero
database, connection-state behavior, or TLS features Slizen does not provide.

## 3. Install observe-first

For the standalone proxy:

```sh
helm upgrade --install slizen ./charts/slizen \
  --namespace slizen-staging --create-namespace \
  --set-string upstream.address=redis.default.svc.cluster.local:6379 \
  --set-string image.digest=sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627
kubectl rollout status deployment/slizen -n slizen-staging --timeout=2m
```

If authentication is required, create the Secret through the cluster's normal
secret manager and set `upstream.existingSecret.name`. The chart reads optional
`username` and `password` keys but never creates credentials. Put a stable HMAC
key in a separate Secret and set `privacy.existingSecret.name` if hot-key IDs
must remain comparable across Pod restarts. If it is omitted, Slizen generates a
cryptographically random process-local HMAC secret and IDs intentionally change
after every restart. Secret-backed environment variables are read at process
start; roll the Slizen Pod after rotating either Secret. Secrets are never
included in startup logs.

For the sidecar pattern, edit the copied ConfigMap's upstream address, pin the
image digest, add the existing Secret references, and apply the parent workload
through its normal deployment system. Do not manage the copied Deployment from
two tools at once. The ConfigMap is mounted with `subPath`, so Kubernetes does
not refresh the file in an existing container. Bump the Pod-template annotation
`slizen.dev/config-revision` on every configuration or policy change and wait
for the resulting rollout. This is mandatory for an observe-to-cache change.

The probes intentionally use `exec` and `slizenctl` against loopback. A kubelet
HTTP probe targets the Pod IP and cannot reach an admin listener bound to
`127.0.0.1`.

## 4. Move one canary endpoint

Change only one low-risk workload or small canary slice:

- sidecar: `REDIS_ADDRESS=127.0.0.1:6380`;
- standalone chart: `REDIS_ADDRESS=slizen.slizen-staging.svc:6380`.

Use the real configuration field for your client and keep the original endpoint
ready as the rollback value. Wait for the workload rollout, then repeat its
integration and smoke tests. Observe for at least one representative traffic
window before increasing the slice.

Inspect the private admin API from inside the Pod:

```sh
POD=$(kubectl get pod -n slizen-staging \
  -l app.kubernetes.io/instance=slizen \
  -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n slizen-staging "$POD" -c slizen -- slizenctl status
kubectl exec -n slizen-staging "$POD" -c slizen -- slizenctl hotkeys --limit 20
kubectl exec -n slizen-staging "$POD" -c slizen -- slizenctl audit --limit 100
```

Do not treat a partial audit as a complete inventory. Investigate
`telemetry_complete=false`: it means the response limit truncated current
entries, the bounded tracker evicted keys, or keys longer than 1,024 bytes were
forwarded without hotness tracking. Increase the report limit only up to 1,000;
increase the observation window or reduce scope rather than removing bounds.

The default chart creates no admin Service. Prometheus scraping is an explicit
exception because metrics and admin routes currently share a listener. To opt
in, set all three values:

```yaml
admin:
  listen: 0.0.0.0:9090
  allowNetworkAccess: true
metrics:
  enabled: true
  serviceMonitor:
    enabled: true
```

This creates a ClusterIP from which the complete admin API, not only `/metrics`,
is reachable. Add a NetworkPolicy or authenticated platform proxy before using
it in a shared cluster. The ServiceMonitor CRD is required only when its flag is
enabled; the default chart has no Prometheus Operator dependency.

## 5. Promote selected prefixes only

Stay in observe mode until error rate and latency match the baseline and the
audit covers representative traffic. Then switch to `cache` mode with explicit
prefix rules. Start with one read-heavy, disposable prefix and conservative
limits. Helm values use `maxItemBytes` and `maxLocalTTL`; the raw sidecar TOML
uses `max_item_bytes` and `max_local_ttl`. Always keep an empty-prefix `observe`
catch-all: without it, unmatched keys inherit the global `cache` mode.

```yaml
mode: cache
cache:
  policies:
    - prefix: ""
      mode: observe
    - prefix: "catalog:public:"
      mode: cache
      maxItemBytes: 262144
      maxLocalTTL: 5s
```

The equivalent raw TOML is:

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

Leave unsafe longer prefixes in `deny` or `observe`. Validate the rendered
configuration before the rollout and confirm that only the intended prefix
reports policy mode `cache` in the privacy-safe audit.

Direct writes to the origin can remain stale until local TTL expiry. Prefer
writes through Slizen and do not enable `allowStaleOnUpstreamError` during the
first staging promotion.

## Roll back within minutes

Rollback order matters:

1. Change the canary application's Redis endpoint back to the recorded original
   Redis/Valkey endpoint.
2. Wait for the application rollout and verify direct `PING`, `GET`, and the
   application health check against the origin.
3. Confirm error rate and latency have returned to baseline.
4. Only then scale down/uninstall the standalone chart or remove the sidecar
   from the parent workload.

For a sidecar managed in Git, reverting the parent workload revision should
restore both the original endpoint and container list in one rollout. For the
standalone pattern, do not uninstall Slizen first: clients still pointing at its
Service would lose connectivity.

Stop and roll back immediately on command errors, connection churn, unexpected
origin request amplification, application error-rate regression, or readiness
flapping. Preserve Slizen logs and the anonymized audit output for diagnosis;
never log or attach Redis values or credentials.
