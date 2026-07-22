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

## Install and attest

After the v0.2.2 tag and image have been published:

```sh
docker pull ghcr.io/slizendb/slizen:0.2.2
docker image inspect ghcr.io/slizendb/slizen:0.2.2 \
  --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'
docker image inspect ghcr.io/slizendb/slizen:0.2.2 \
  --format '{{index .RepoDigests 0}}'
gh attestation verify oci://ghcr.io/slizendb/slizen:0.2.2 \
  --repo slizendb/slizen
```

Compare the revision label with the released commit, then use the exact digest from the published `release-evidence-manifest.json` for an immutable deployment. The rolling `0.2` alias is convenient for discovery but is not an immutable release identity.

This document does not assert a final v0.2.2 commit, image digest, release-workflow run, or image-bound benchmark result before those artifacts exist. Publication is complete only after the tagged source gate passes, provenance verifies, and checksummed evidence generated from the exact published digest is attached to the release.

## Evidence contract

The v0.2.2 release gate runs the exact `uniform`, `skew-80-20`, `skew-99-1`, and `moving-flash` scenarios with 1,000 keys, concurrency 32, a 95/5 read/write mix, and 128-byte values. Each direct-origin and Slizen phase must reach exactly 100,000 generated operation attempts and report `termination_reason: "request_limit"`; 30 seconds is a safety cap, not the intended stopping condition. The moving flash key advances every 20,000 generated operations.

Every scenario must have isolated, monotonic status evidence, zero request failures, zero value mismatches, and zero final-validation failures or mismatches. The combined read and write latency sample count must equal generated operation attempts, ordering-wait samples must match their command class, and final-validation samples must match validation reads. The stable 99/1 scenario must additionally record real cache hits and positive measured origin GET reduction. There is deliberately no shared-runner latency or capacity threshold.

## Pre-release local evidence

These results were recorded on an Apple M5 with Go 1.26.5 and describe the tested revisions, host, configuration, and synthetic workloads only. They are not tagged-image evidence or a production capacity claim.

- In corrected in-process cache-hit benchmarks, the serial handler median fell from 488.0 to 159.2 ns/op and the concurrent dispatch median fell from 918.8 to 531.6 ns/op. Benchmark allocations fell from 320 B and 8 allocations per operation to 16 B and 2 allocations for the serial path, and to 15 B and 2 allocations for the concurrent path.
- Against drain-accounting baseline `86623ef`, handler bookkeeping medians were 54.5%, 70.7%, and 69.0% lower at local `GOMAXPROCS=1`, `10`, and `32`. In ten counterbalanced `GOMAXPROCS=10` dispatch pairs, every pair favored the candidate and the median fell from 538.6 to 383.75 ns/op, with allocations unchanged at 15 B and 2 allocations per operation.
- Across three local Docker hot-key repeats, a fully warm Slizen served 100% cache hits with zero origin GETs. Median p99 was 1.277 ms through Slizen versus 1.182 ms direct, a 0.095 ms tax. Three complete request-bound workload gates measured 71.4–79.2% origin GET reduction in the mixed 99/1 scenario with a 0.23–0.52 ms read-p99 tax.
- In five alternating 99/1 Docker A/B pairs against baseline `e35792a`, guaranteed final-window promotion raised the median cache-hit ratio from 36.00% to 61.61%, raised median origin GET reduction from 72.57% to 85.92%, and reduced median upstream GETs from 26,044 to 13,371. Median Slizen read p99 was effectively flat at 1.364 versus 1.368 ms, and all ten benchmark invocations reached the request limit with zero failures, value mismatches, or final-validation mismatches.
- The earlier-promotion change added 0.76 ns/op, or 3.1%, to the steady-state hotness observation median with zero allocations. A separate moving-flash gate measured substantial origin reduction, but most of that saving came from request coalescing, so it is not presented as a cache-only result.

The in-process benchmarks exclude RESP parsing, TCP, socket I/O, operating-system deadline syscalls, and upstream work. The Docker results are single-host synthetic measurements, and the wider adaptive-workload variance is why v0.2.2 makes no universal latency, throughput, or origin-reduction promise. Reproduce comparisons with matching limits, termination reasons, runtime, configuration, seed, and workload shape.

## Known limitations

- Slizen remains a single-node developer preview. It is not a durable database, distributed cache mesh, Redis Cluster replacement, or source of truth.
- Direct writes to Redis or Valkey can remain stale until local TTL expiration. Writes are safest when they pass through Slizen; server-assisted invalidation remains future work.
- Redis compatibility is intentionally limited, negative caching is not implemented, and the admin API has no built-in authentication and must remain private.
- redcon assembles a complete RESP command before Slizen applies byte and argument limits. Those settings bound dispatch and upstream work, not parser allocation, and upstream response sizes are not bounded by them.
- `observe` mode continues to forward reads and collect telemetry without serving or storing local cached values.
- Long-running soak, 100,000-key churn, and workload-specific capacity validation remain required before serious deployment.
