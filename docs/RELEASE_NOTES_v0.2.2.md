# Slizen v0.2.2 — Proxy tax reduction and benchmark attribution

Slizen v0.2.2 keeps the v0.2 single-node developer-preview scope while reducing verified cache-hit overhead, removing avoidable delay from hot-key promotion, and making workload evidence easier to interpret. Redis or Valkey remains the source of truth.

## What changed

- Redis-backed `GET` misses now fetch the value and remaining TTL in one pipelined round trip. Missing keys, cancellation, Redis reply errors, and transport failures retain explicit handling.
- The verified local-cache `GET` path avoids miss-only timeout allocation, redundant hotness and cache-stat locks, generic command parsing, and repeated Prometheus label lookup.
- Steady-state handler drain accounting now uses a double-checked atomic reservation and one completion critical section instead of three global mutex handoffs. Deadline syscalls no longer run while holding the global drain mutex, and a handler that races with drain startup is either accounted for or rolled back without executing a command.
- A key can enter `HOT` during the final required qualifying window once promotion is mathematically unavoidable. The configured EWMA, threshold, consecutive-window hysteresis, and cooldown rules are unchanged; the tracker no longer waits for a redundant wall-clock boundary.
- `slizenctl benchmark workload` now reports separate read, write, ordering-wait, and final-validation latency distributions. It also records generated operation attempts and whether issuance stopped at the request or duration limit while retaining the aggregate latency fields for schema compatibility.

## Upgrade notes

- No configuration keys were added or removed. Existing v0.2.1 observe-first and selective-cache configurations remain applicable.
- Hot keys may promote earlier within their final qualifying window. Review alerting or tests that assumed state transitions occurred only at a completed window boundary; thresholds, hysteresis, demotion, and cooldown behavior have not been relaxed.
- Upstream `GET` misses still require both `GET` and `PTTL` permissions. v0.2.2 sends those commands as one pipeline rather than two serial round trips, so restrictive Redis or Valkey ACLs must continue to allow both.
- Workload JSON consumers should accept the additive `operation_attempts`, `termination_reason`, and per-operation latency objects. The top-level `p50_ms`, `p95_ms`, and `p99_ms` fields remain mixed aggregate distributions; use the new objects when distinguishing command time from harness ordering wait and final validation.
- Local cache and hotness state remain disposable. Restarting Slizen still starts those states cold and does not require data migration.

## Published artifacts

- The annotated `v0.2.2` tag resolves to commit `74a12767deb72db9bc78bebd807cbe8717fa572c`.
- The immutable multi-architecture image index is `ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627`, with `linux/amd64` and `linux/arm64` manifests. The `v0.2.2`, `0.2.2`, `0.2`, and `latest` aliases were independently verified against that index digest.
- The exact-commit [public CI](https://github.com/slizendb/slizen/actions/runs/29952948422), [100,000-key extended validation](https://github.com/slizendb/slizen/actions/runs/29953153624), and [release image workflow](https://github.com/slizendb/slizen/actions/runs/29953669287) completed successfully.
- GitHub-native provenance verifies for the immutable digest. The checksummed image evidence bundle and both extended-validation artifacts are attached to the [GitHub Release](https://github.com/slizendb/slizen/releases/tag/v0.2.2).

## Install and attest

```sh
docker pull ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627
docker image inspect ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627 \
  --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'
gh attestation verify oci://ghcr.io/slizendb/slizen@sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627 \
  --repo slizendb/slizen
```

The revision label must equal the released commit above. The rolling `0.2` alias is convenient for discovery but is not an immutable release identity.

## Evidence contract

The v0.2.2 release gate runs the exact `uniform`, `skew-80-20`, `skew-99-1`, and `moving-flash` scenarios with 1,000 keys, concurrency 32, a 95/5 read/write mix, and 128-byte values. Each direct-origin and Slizen phase must reach exactly 100,000 generated operation attempts and report `termination_reason: "request_limit"`; 30 seconds is a safety cap, not the intended stopping condition. The moving flash key advances every 20,000 generated operations.

Every scenario must have isolated, monotonic status evidence, zero request failures, zero value mismatches, and zero final-validation failures or mismatches. The combined read and write latency sample count must equal generated operation attempts, ordering-wait samples must match their command class, and final-validation samples must match validation reads. The stable 99/1 scenario additionally required real cache hits and positive proxy-side logical upstream-call avoidance. There is deliberately no shared-runner latency or capacity threshold.

## Image-bound release evidence

The published-image run used the exact digest above with Valkey 8.1.9 on a GitHub-hosted Linux/amd64 runner. Every direct-origin and Slizen phase reached exactly 100,000 generated operation attempts with `termination_reason: "request_limit"`. All four scenarios recorded zero request failures, value mismatches, final-validation failures, and final-validation mismatches; the complete sample-accounting contract passed.

The v0.2.2 evidence schema compared successful direct reads with the
`/v1/status` logical upstream-call delta. It did not capture origin
`INFO commandstats`, a stable origin `run_id`, or physical retry attempts.
Consequently, the historical counts below are proxy-side logical estimates,
not proof of physical origin traffic.

- In the stable 99/1 scenario, logical upstream GET calls fell from `94,961` direct successful reads to `9,707` through Slizen, an `89.778%` proxy-side avoidance estimate, with a `73.628%` cache-hit ratio. Attributed read p99 was `2.137 ms` through Slizen versus `1.460 ms` direct, so this is neither a speed claim nor a physical-origin claim.
- In the moving-flash scenario, logical upstream GET calls fell from `94,956` direct successful reads to `8,735`, a `90.801%` proxy-side avoidance estimate, while the cache-hit ratio was `13.451%`. Request coalescing contributes substantially to that gap, so it must not be presented as cache-hit reduction alone. Attributed read p99 was `1.166 ms` through Slizen versus `0.801 ms` direct.
- In the separate single-hot-key image test, the fully warm phase served 20,000 requests with 100% cache hits and zero logical upstream GET calls. Its p99 was `1.241 ms` through Slizen versus `1.191 ms` direct; this narrow synthetic best case is not a production-capacity or physical-wire result.

See the [image-bound workload JSON](https://github.com/slizendb/slizen/releases/download/v0.2.2/slizen-workload-result.json), [image benchmark JSON](https://github.com/slizendb/slizen/releases/download/v0.2.2/slizen-benchmark-result.json), and checksummed manifest attached to the release. The separate [100,000-key workload](https://github.com/slizendb/slizen/releases/download/v0.2.2/extended-workload-result.json) and [five-run high-cardinality benchmarks](https://github.com/slizendb/slizen/releases/download/v0.2.2/high-cardinality-benchmarks.txt) also passed without failures or mismatches. That workload used a duration safety cap and is not a fixed-request throughput comparison or published-image run.

## Pre-release local optimization evidence

These results were recorded on an Apple M5 with Go 1.26.5 and describe the tested revisions, host, configuration, and synthetic workloads only. They are not tagged-image evidence or a production capacity claim.

- In corrected in-process cache-hit benchmarks, the serial handler median fell from 488.0 to 159.2 ns/op and the concurrent dispatch median fell from 918.8 to 531.6 ns/op. Benchmark allocations fell from 320 B and 8 allocations per operation to 16 B and 2 allocations for the serial path, and to 15 B and 2 allocations for the concurrent path.
- Against drain-accounting baseline `86623ef`, handler bookkeeping medians were 54.5%, 70.7%, and 69.0% lower at local `GOMAXPROCS=1`, `10`, and `32`. In ten counterbalanced `GOMAXPROCS=10` dispatch pairs, every pair favored the candidate and the median fell from 538.6 to 383.75 ns/op, with allocations unchanged at 15 B and 2 allocations per operation.
- Across three local Docker hot-key repeats, a fully warm Slizen served 100% cache hits with zero logical upstream GET calls. Median p99 was 1.277 ms through Slizen versus 1.182 ms direct, a 0.095 ms tax. Three complete request-bound workload gates estimated 71.4–79.2% logical upstream-call avoidance in the mixed 99/1 scenario with a 0.23–0.52 ms read-p99 tax.
- In five alternating 99/1 Docker A/B pairs against baseline `e35792a`, guaranteed final-window promotion raised the median cache-hit ratio from 36.00% to 61.61%, raised median logical upstream-call avoidance from 72.57% to 85.92%, and reduced median logical upstream GET calls from 26,044 to 13,371. Median Slizen read p99 was effectively flat at 1.364 versus 1.368 ms, and all ten benchmark invocations reached the request limit with zero failures, value mismatches, or final-validation mismatches.
- The earlier-promotion change added 0.76 ns/op, or 3.1%, to the steady-state hotness observation median with zero allocations. A separate moving-flash gate measured substantial origin reduction, but most of that saving came from request coalescing, so it is not presented as a cache-only result.

The in-process benchmarks exclude RESP parsing, TCP, socket I/O, operating-system deadline syscalls, and upstream work. The Docker results are single-host synthetic measurements, and the wider adaptive-workload variance is why v0.2.2 makes no universal latency, throughput, or origin-reduction promise. Reproduce comparisons with matching limits, termination reasons, runtime, configuration, seed, and workload shape.

## Known limitations

- Slizen remains a single-node developer preview. It is not a durable database, distributed cache mesh, Redis Cluster replacement, or source of truth.
- Direct writes to Redis or Valkey can remain stale until local TTL expiration. Writes are safest when they pass through Slizen; server-assisted invalidation remains future work.
- Redis compatibility is intentionally limited, negative caching is not implemented, and the admin API has no built-in authentication and must remain private.
- redcon assembles a complete RESP command before Slizen applies byte and argument limits. Those settings bound dispatch and upstream work, not parser allocation, and upstream response sizes are not bounded by them.
- `observe` mode continues to forward reads and collect telemetry without serving or storing local cached values.
- The attached 100,000-key run is a bounded synthetic check, not a long-running soak or workload-specific capacity validation. Both remain required before serious deployment.
