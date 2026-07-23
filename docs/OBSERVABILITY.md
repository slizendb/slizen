# Observability

Slizen ships an import-ready Grafana dashboard and conservative Prometheus
alert rules for a staging trial:

- `deploy/observability/grafana-dashboard.json`
- `deploy/observability/prometheus-rules.yaml`

The pack uses only Slizen's bounded metric labels. Redis keys, values, policy
prefixes, credentials, and user-provided command names do not become labels.

## Version scope

The dashboard and rule file can be loaded for either the stable v0.2.2 image or
the published v0.2.3-rc.1 staging prerelease, but not every series exists in
both:

| Signal | Stable v0.2.2 | v0.2.3-rc.1 prerelease |
| --- | --- | --- |
| Requests, request/upstream latency, upstream requests/errors | yes | yes |
| Aggregate cache hits/misses, bytes, entries, evictions | yes | yes |
| Promotions, demotions, invalidations, coalescing | yes | yes |
| Oversized-key hotness drops | yes | yes |
| Cache miss reasons and configured max byte/entry gauges | no | yes |
| Hotness tracker capacity drops | no | yes |
| Go/runtime and process CPU/RSS collectors | no | yes |
| Active downstream connections | no | yes |

A query returning no candidate-only series on v0.2.2 does not mean zero cache
pressure, complete hotness telemetry, or zero process resource use. For v0.2.2,
record configured cache limits beside `slizen_cache_bytes` and
`slizen_cache_entries`, retain Pod/container CPU and working-set/RSS metrics
from the platform, use application/platform socket telemetry for connection
counts, and use the aggregate miss counters. Do not use a v0.2.3 source-tree
dashboard panel as evidence about a v0.2.2 binary.

## Expose metrics deliberately

Slizen serves `/metrics` on the admin listener. The Helm chart keeps that
listener private by default because every admin route, not only `/metrics`,
shares the same port. To opt in to the chart's metrics Service and
ServiceMonitor, set:

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

Replace the example monitoring labels with the exact scraper identity. A
ServiceMonitor does not bypass NetworkPolicy. Prove both that the exact peer can
scrape and that an unlisted Pod receives no HTTP response; a negative result
alone can be a broken Service rather than enforced isolation. If the Prometheus
Operator is not installed, leave `serviceMonitor` disabled and configure the
platform scraper against `/metrics` itself with the same restriction.

The raw sidecar example is different: it binds the admin listener to
`127.0.0.1:9090` and creates no Service or PodMonitor. Provide a continuous
same-Pod collector/proxy, or deliberately bind to the Pod address and restrict
discovery plus ingress to the exact scraper. A temporary `kubectl port-forward`
supports inspection, not a staging soak. Without a continuous scrape path, keep
the sidecar in `observe`; it cannot pass the cache observability gate.

## Import

In Grafana, import `grafana-dashboard.json` and select the Prometheus
datasource. Then select the Slizen scrape job and instance variables. The
dashboard sums the selected instances, so use one instance when diagnosing a
single-node staging canary.

Load `prometheus-rules.yaml` using the normal Prometheus or Prometheus Operator
rule-management path. The file is plain Prometheus rule YAML; it is not a
Kubernetes `PrometheusRule` custom resource.

Every supplied alert is a conservative staging default. Establish the
application's direct-Redis baseline and tune thresholds, traffic guards, and
evaluation windows before production. The default latency alerts use an
absolute 100 ms threshold because Slizen cannot infer an application's latency
budget.

## Read the signals correctly

The dashboard keeps three related measurements separate:

- **GET cache-hit ratio** is
  `cache hits / (cache hits + cache misses)` for `GET`.
- **Logical upstream GET-call ratio** is
  `Slizen logical upstream GET calls / proxy GET requests`.
- **Logical upstream GET-call avoidance** is `1 - logical call ratio`, clamped
  to 0–100% for display.

The origin panels intentionally exclude `MGET`: an `MGET` proxy request and its
per-key cache outcomes have different counting units. Request coalescing can
reduce logical upstream demand without creating cache hits, so cache-hit ratio
and logical call avoidance should not be presented as interchangeable.

`slizen_upstream_requests_total` counts one completed Slizen data-path call,
not physical Redis or Valkey commands. The upstream client can retry a call;
`GET` also uses a `GET` plus `PTTL` pipeline, and readiness/status `PING`s are
outside this counter. `slizen_upstream_errors_total` counts terminal logical
errors after retry handling, while `slizen_upstream_duration_seconds` includes
pool wait, retries, and backoff. Therefore the supplied proxy-side panels are
an avoidance estimate, not authoritative origin load or retry-amplification
evidence.

For the staging origin-traffic gate, record the `cmdstat_get:calls` delta from
Redis or Valkey `INFO commandstats`, or the equivalent origin-side exporter
series, over the same control/canary window. Use the operator/benchmark
identity for this read; do not grant `INFO` to Slizen's runtime identity.
Require a dedicated or otherwise attributable origin, a monotonic counter, and
no `CONFIG RESETSTAT` during the window.

`slizen_coalesced_requests_total` counts requests receiving a shared
singleflight result. It is a concurrency signal, not an exact count of upstream
calls saved.

`slizen_cache_bytes` is Slizen's bounded cache accounting of keys, values, and a
fixed per-entry overhead estimate. It is not process RSS or exact Go heap usage.
`slizen_cache_max_bytes` and `slizen_cache_max_entries` expose the configured
global cache bounds across the protected and probationary tiers in the v0.2.3
candidate. A maximum entry count of zero means no separate entry-count limit;
the byte bound still applies. Stable v0.2.2 has one bounded LRU and does not
export either max gauge, so compare its usage with the recorded configuration
outside PromQL.

Any increase in an available hotness-drop counter makes the bounded audit
incomplete for that interval:

- `slizen_hotness_oversized_observations_dropped_total` exists in v0.2.2 and
  later;
- `slizen_hotness_capacity_observations_dropped_total` is a v0.2.3 candidate
  addition.

The associated Redis or Valkey command is still forwarded. Investigate tracker
capacity and oversized keys before making a cache-policy decision from the
audit.

The v0.2.3 candidate also registers the standard Go and process collectors.
The dashboard's process CPU, resident-memory, goroutine, and allocation-rate
panels use `process_cpu_seconds_total`, `process_resident_memory_bytes`,
`go_goroutines`, and `go_memstats_alloc_bytes_total`. Allocation rate measures
churn, not live heap size. Container CPU, working set, memory limit, restart,
OOM, and readiness signals still belong in the platform's Kubernetes
monitoring; the process collectors do not replace them.

`slizen_active_connections` is a v0.2.3 candidate gauge of currently accepted
downstream RESP connections. Use it to verify bounded reconnect behavior and
that rollback/drain returns the count to the expected level. It is absent in
v0.2.2; use platform socket or application client-pool telemetry for that
version rather than interpreting a missing series as zero.

## Latency and proxy tax

`slizen_request_duration_seconds` measures the request handler's end-to-end
duration. `slizen_upstream_duration_seconds` measures calls Slizen makes to
Redis or Valkey. The dashboard shows p95 and p99 for both.

Do not subtract one histogram quantile from another to claim proxy tax.
Quantiles are not paired observations, may describe different request
populations, and do not compose by subtraction. Measure proxy overhead with a
controlled direct-versus-Slizen workload or application-side telemetry using
the same request mix and time window.

## Alert intent

The supplied rules cover:

- global or single-canary loss of the pre-initialized metrics series;
- sustained proxy and upstream error ratios, with low-traffic guards;
- absolute GET and upstream p99 staging budgets;
- cache pressure only when high utilization coincides with eviction churn
  (v0.2.3 candidate max gauges);
- oversized-key telemetry loss on v0.2.2 and later;
- tracker-capacity telemetry loss on the v0.2.3 candidate.

The two hotness alerts are intentionally separate. PromQL arithmetic with a
missing v0.2.3 capacity series would otherwise suppress the v0.2.2
oversized-key alert.

`SlizenMetricsMissing` uses the pre-initialized
`slizen_requests_total{command="GET",result="ok"}` child, so an idle canary still
exports the series. It detects that no matching Slizen target has been scraped
for five minutes. It is intentionally a global or single-canary signal: when
several targets share one Prometheus, any remaining target suppresses
`absent_over_time`. Do not rewrite it as a generic `up == 0`, which can select
unrelated scrape jobs. Add per-target Kubernetes probe or blackbox alerts in
the deployment's monitoring stack to catch readiness loss and flapping; Slizen
request metrics cannot establish that condition by themselves.

A cache sitting near its configured limit is normal, so capacity alone does not
alert. The supplied cache-pressure alerts are inactive on v0.2.2 because that
image does not expose max gauges; use recorded configuration and manual/platform
thresholds for a v0.2.2 trial. Logical upstream-call avoidance and cache-hit
ratio are dashboard decision signals rather than universal alerts: their
acceptable values depend on mode, selected prefixes, workload shape, and the
staging objective. The pack deliberately does not claim a Slizen-side physical
origin-amplification alert: configure that alert from Redis/Valkey
`commandstats` or an origin-side exporter.
Active-connection growth is likewise budgeted per client pool and reconnect
strategy, so the pack visualizes it but does not impose a universal count alert.

During a canary, roll back on unexplained command errors, error-rate regression,
readiness flapping, or origin-side command amplification. Use the procedure in
`docs/STAGING_ROLLOUT.md`; Redis or Valkey remains the source of truth.
