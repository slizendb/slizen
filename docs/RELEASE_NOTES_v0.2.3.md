# Slizen v0.2.3 — Bounded two-hit admission release candidate

**Release candidate, not a published release.** These notes describe the current v0.2.3 source tree. There is no claimed `v0.2.3` tag, GHCR image, immutable digest, provenance record, or image-bound evidence yet. Until those artifacts pass the release workflow, use the published [v0.2.2 release](https://github.com/slizendb/slizen/releases/tag/v0.2.2) for a verifiable installation.

Slizen v0.2.3 keeps the single-node developer-preview scope while reducing cold-start origin reads for skewed workloads. Redis or Valkey remains authoritative.

## What changed

- Cache-policy reads use bounded two-hit admission. The first eligible successful miss can retain a probationary value; one later read promotes and serves that value without a second origin GET. Waiters joined to the same coalesced first miss do not count as the later admission read.
- The existing global cache budgets are partitioned: seven eighths for protected admitted values and one eighth for probationary candidates. This does not increase `cache.max_bytes` or `cache.max_entries`; very small limits that cannot be split keep the protected-only behavior.
- A probationary candidate uses the normal bounded local TTL, additionally capped by the hotness window. Promotion carries the remaining TTL forward and preserves the candidate's original absolute expiry instead of restarting it.
- A successful exact `SET key value` with no options can refresh a protected value after the upstream accepts it, but only when the key is already admitted and its effective policy is `cache`. It does not admit a cold key. Option-bearing `SET`, other supported mutations, nil replies, and errors retain conservative invalidation.
- Proxied writes invalidate both cache tiers before upstream dispatch and apply a final epoch barrier after completion. Per-stripe mutation serialization and the existing refill epochs prevent an overlapping miss or candidate from restoring a superseded value.
- `/v1/status`, Prometheus, and workload JSON attribute cache misses with a fixed bounded vocabulary: `policy_bypass`, `not_admitted`, and `not_present`; Prometheus also initializes an internal `unclassified` series. Redis keys and user input never become metric labels.
- When the bounded tracker is full, an unseen observation inspects one FIFO victim in O(1). A current HOT victim is retained and the unseen observation is dropped rather than scanning or evicting it. Audit reports `capacity_observations_dropped`, Prometheus exposes `slizen_hotness_capacity_observations_dropped_total`, and any such drop makes `telemetry_complete=false`. This is bounded hot-victim protection, not unlimited scan resistance.
- `slizenctl compatibility report` prints the deterministic command catalog compiled into the exact CLI version and commit. Passing an explicit list makes it a bounded offline CI gate; `SET`, `SELECT`, `EXPIRE`, and `PEXPIRE` require explicit acceptance after their narrower argument contract is reviewed. It does not inspect or discover a workload.
- `deploy/observability` contains an import-ready Grafana dashboard and conservative Prometheus staging rules. The metrics contract now exposes active downstream connections, configured cache byte and entry bounds, Go runtime/allocation behavior, and Linux process CPU/RSS while keeping labels bounded and privacy-safe.
- The default downstream idle read deadline is now five minutes rather than three seconds. Tune `proxy.read_timeout` above the application's expected pool-idle/reuse interval; the connection count remains bounded by `proxy.max_connections`.
- The staging documentation now defines pre-agreed go/no-go thresholds, observe and one-prefix soaks, gradual canary expansion, a measured endpoint-first rollback rehearsal, failure behavior, and a pass/partial/fail self-service gate.
- The Helm chart now enables a default-deny ingress NetworkPolicy. The operator must name exact application and monitoring peers before the unauthenticated RESP or admin listeners become reachable.

## Upgrade notes

- There are no new `slizen.toml` fields. Existing v0.2.2 observe-first and per-prefix policy files remain valid. The chart adds `networkPolicy.redisIngressPeers` and `networkPolicy.metricsIngressPeers`; both default to an empty deny-all policy.
- `cache.max_bytes` and non-zero `cache.max_entries` remain global bounds across both tiers. Operators should not add one eighth to existing limits.
- A first eligible miss may now consume probationary space even before the EWMA tracker would have promoted the key. It remains disposable, bounded, short-lived state and is visible in aggregate cache bytes and entries.
- At tracker capacity, telemetry can deliberately omit a new key to protect the current HOT FIFO victim. Treat a non-zero `capacity_observations_dropped` as incomplete telemetry when interpreting audit recommendations.
- Exact option-free `SET` can make the next read a local hit for an already admitted key. If a client depends on Redis `SET` options, Slizen conservatively invalidates instead of trying to reconstruct those semantics locally.
- Direct writes to the origin are unchanged: they do not notify Slizen and can leave either local tier stale until local TTL expiration. Route supported writes through Slizen where possible and keep TTLs short enough for the workload.
- Workload JSON consumers should accept the additive `cache_misses_policy_bypass`, `cache_misses_not_admitted`, and `cache_misses_not_present` counters.
- Existing Prometheus consumers may use the additive `slizen_cache_max_bytes` and `slizen_cache_max_entries` gauges to calculate configured cache utilization. These limits do not represent total Go heap or container RSS.
- Until a v0.2.3 image exists, the Helm defaults and raw sidecar example deliberately pin the verified v0.2.2 digest. Do not replace it with a guessed or floating v0.2.3 reference.

## Local release-candidate evidence

Five local Docker repeats exercised the unchanged cold, request-bound `skew-99-1` scenario with seed 42, 1,000 keys, 100,000 generated operations per phase, a 95/5 read/write mix, 128-byte values, and concurrency 32.

| Metric | Five-repeat result |
| --- | ---: |
| Direct successful GETs | 94,961 in every run |
| Slizen logical upstream GET calls | 798–803 |
| Proxy-side logical-call avoidance | 99.154390%–99.159655% |
| Cache-hit ratio | 99.121745%–99.151231% |
| Slizen read p99 | 1.175–1.251 ms |
| Direct read p99 | 0.986–1.042 ms |

Every repeat reached the request limit with zero request failures, value mismatches, final-validation failures, and final-validation mismatches. The p99 ranges do not show a speed win over direct local Valkey. These historical repeats used Slizen's logical `/v1/status` delta and did not capture Redis/Valkey `commandstats`, so the defensible result is approximately 99.15% proxy-side logical GET-call avoidance for this exact skewed workload on this host. It is not proof of physical wire-command reduction under retries, “Slizen makes Redis faster,” a production capacity number, or a universal 99% guarantee.

These measurements came from a local source-tree candidate. Published v0.2.3 evidence must be regenerated from the exact immutable image digest after the tag passes the full release gate.

## Release gate before publication

- Run formatting, vet, unit, race, build, Docker smoke, Kubernetes rendering, and the complete four-scenario request-bound workload gate from one clean intended commit.
- Confirm every scenario has exact sample accounting and zero request or value-validation failures.
- Confirm the stable 99/1 scenario includes fixed miss attribution and positive physical origin reduction from a same-`run_id`, monotonic `INFO commandstats` delta. The gate intentionally has no latency or universal 99% threshold.
- Publish only after the tagged source succeeds, then bind the image digest, full commit, version, workload JSON, demo report, checksums, and provenance in the release evidence bundle.

## Known limitations

- Slizen remains a single-node developer preview. It is not a durable database, Redis Cluster replacement, distributed mesh, transactional store, or source of truth.
- Direct-origin invalidation is not implemented. Server-assisted Redis/Valkey tracking remains planned for v0.3.
- Redis compatibility is intentionally limited and negative caching is not implemented. The downstream RESP listener has no client `AUTH` or TLS, the upstream client has no Redis/Valkey TLS, and the admin API has no built-in authentication. Every plaintext listener/path must remain private; a TLS-required origin needs a separately reviewed external termination/tunnel or cannot use v0.2.
- The upstream client uses one standalone address. Redis Cluster redirections/cross-slot behavior and Sentinel topology/failover discovery are not supported.
- Every sidecar replica owns independent cache state. v0.2 does not broadcast invalidations across application Pods, so multi-replica cache mode requires read-only prefixes or an explicitly accepted local-TTL staleness budget.
- `observe` mode still forwards reads and records bounded telemetry without serving or storing local values.
- Synthetic local evidence does not replace a workload-specific soak, memory profile, outage drill, or rollback rehearsal.
