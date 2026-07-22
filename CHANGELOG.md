# Changelog

## v0.2.2 - 2026-07-22 - Proxy Tax Reduction and Benchmark Attribution

### Changed

- Redis-backed GET misses fetch the value and its remaining TTL in one pipelined round trip while preserving missing-key, cancellation, and transport-error semantics.
- The verified local-cache GET path avoids miss-only timeout allocation, redundant hotness/cache-stat locks, generic command parsing, and repeated Prometheus label lookup.
- Proxy drain deadline syscalls no longer hold the global drain mutex; a second draining-state check preserves shutdown accounting when draining starts concurrently.
- Steady-state handler drain accounting uses a double-checked atomic reservation and one completion critical section instead of three global mutex handoffs; raced reservations roll back without executing a command.
- The hotness tracker can commit the final required qualifying window as soon as its count makes the promotion threshold mathematically unavoidable. EWMA scoring, consecutive-window hysteresis, and cooldown semantics remain unchanged, but hot keys no longer wait for an otherwise redundant wall-clock boundary.
- Workload evidence attributes read, write, and final-validation latency separately and records whether a phase stopped at its request or duration limit.

### Performance

- On Apple M5 with Go 1.26.5, the corrected handler-level cache-hit benchmark median fell from 488.0 ns/op to 159.2 ns/op; the concurrent dispatch median fell from 918.8 ns/op to 531.6 ns/op. Allocations fell from 320 B and 8 allocations per operation to 16 B and 2 allocations per operation. These are local microbenchmark results, not production capacity claims.
- Against commit `86623ef`, steady-state handler drain bookkeeping fell by 54–71% across local `GOMAXPROCS=1,10,32` microbenchmarks. In ten counterbalanced `GOMAXPROCS=10` dispatch-level warm-hit A/B pairs, every pair favored the candidate and median time fell from 538.6 to 383.75 ns/op (28.7%) with allocations unchanged at 15 B and 2 allocations per operation. These in-process results exclude TCP, RESP parsing, socket I/O, and upstream work.
- Across three local Docker hot-key repeats, warm Slizen p99 had a 0.095 ms median tax over direct Valkey while serving 100% cache hits with zero origin GETs. Across three complete request-bound gates, the mixed 99/1 workload reduced origin GETs by 71.4–79.2% with a 0.23–0.52 ms read-p99 tax. These measurements describe this machine and workload only.
- Across five alternating local Docker 99/1 A/B pairs, guaranteed final-window promotion raised the median cache-hit ratio from 36.00% to 61.61%, raised median origin GET reduction from 72.57% to 85.92%, and cut median upstream GETs from 26,044 to 13,371. Median Slizen read p99 remained effectively flat at 1.364 versus 1.368 ms, with zero failures or value/final-validation mismatches. The steady tracker benchmark cost rose by 0.76 ns/op (3.1%) with zero allocations; all figures are specific to this host and workload.

## v0.2.1 - 2026-07-22 - Launch Hardening

### Added

- Explicit RESP request-size, argument-count, MGET-key-count, and concurrent-connection admission limits, including configuration and packaging bounds.
- Key-and-write-version workload verification, final validation of every written key, and `value_mismatches`; stale-after-write or otherwise unexpected successful GETs now invalidate benchmark evidence.
- Immutable-image release evidence manifest and checksums, GitHub-native OCI provenance, pinned GitHub Actions, pinned container bases, and automated dependency update configuration.
- Go 1.26.5 as the minimum build toolchain, including the standard-library fix for GO-2026-5856.
- GHCR install path, design-partner intake, issue and pull-request templates, CODEOWNERS, private security-report link, and canonical Apache-2.0 licensing metadata.

### Changed

- Zero-config startup is observe-first. Selective cache promotion now requires global `cache` mode plus an empty-prefix `observe` catch-all and explicit narrower cache policies.
- An omitted HMAC key now produces a cryptographically random process-local secret instead of a shared placeholder; configure a secret only when identifiers must remain stable across restarts.
- Hotness summary metrics use maintained counters, and full-tracker unseen-key admission uses bounded deterministic FIFO eviction instead of an O(n) scan under the tracker lock.
- Cache statistics report bounded retained storage without deleting expired entries that may still be eligible for an explicitly configured stale-grace fallback.
- Shared GET refills are independent of an individual caller but bounded by the stricter proxy/upstream read timeout and canceled on service shutdown.
- Over-limit pipelined commands discard their parsed tail before connection close, preventing a trailing command from reaching the origin.
- Tagged images are published only after the tagged source passes the release gate. Version and full commit stamping are checked again against the exact published image digest.

### Limitations

- redcon assembles one complete RESP command before Slizen can apply command byte and argument limits. The limits bound dispatch and upstream work but are not a pre-allocation parser ceiling.
- Upstream response sizes are not bounded by the new request-admission settings.
- High-cardinality and long-running performance evidence remains a separate engineering track; shared CI has no universal latency or capacity threshold.
- All v0.2 developer-preview limitations still apply, including single-node operation, limited Redis compatibility, direct-origin staleness, and an unauthenticated admin API.

## v0.2.0 - Safe Staging Preview

### Added

- Bounded per-prefix `deny`, `observe`, and `cache` policies with longest-prefix matching, explicit item-size limits, and local TTL caps.
- Privacy-safe `slizen.audit.v1` report through `/v1/audit` and `slizenctl audit`, including effective policy, hotness state, neutral recommendations, and stable reason codes.
- Release-grade workload scenarios for uniform traffic, 80/20-like skew, 99/1-like skew, and a moving flash key, with JSON latency percentiles and runtime metadata.
- Observe-first Kubernetes sidecar example, Helm packaging without an Operator, and a documented rollout/rollback workflow.
- Allocation baselines for cache, hotness, and proxy hit paths.

### Changed

- Concurrent GET/MGET refills now use bounded cache epochs so a read overlapping a proxied write cannot restore a superseded value.
- Ambiguous upstream write errors conservatively invalidate affected local cache entries.
- Proxy shutdown now drains accepted handlers and connections up to a bounded deadline before force-closing them.
- Proxy response flushes now enforce `proxy.write_timeout`; hotness window catch-up is constant-time per key and tracking eviction immediately removes any corresponding cached value.
- Audit output reports whether limits, tracker eviction, or oversized keys made telemetry incomplete.
- Unsupported write families are explicitly rejected and documented instead of falling through ambiguously.
- The near-term roadmap now prioritizes safe workload evidence and direct-origin invalidation before mesh or fleet-management work.

### Limitations

- Single-node only; no mesh or cross-node value replication.
- Direct writes to Redis or Valkey can remain stale until local TTL expiration; server-assisted invalidation is planned for v0.3.
- The admin API has no built-in authentication and must remain private.
- Kubernetes packaging does not inject sidecars or provide an Operator.
- Developer preview; production use still requires workload-specific staging validation.

## v0.1.0 - Developer Preview

### Added

- Single-node Slizen RESP proxy for a documented Redis/Valkey command subset.
- `cache` mode with hot-key detection, bounded local cache, request coalescing, and write-driven invalidation.
- `observe` mode for safe hot-key telemetry without cache hits or value storage.
- Admin API, Prometheus metrics, CLI tooling, Docker Compose demo, smoke scripts, and demo-report generation.
- Real Valkey integration tests and release-check workflow.
- Reproducible `slizenctl benchmark hotkey` command.

### Changed

- Public docs now describe Slizen as a developer preview.
- Release docs now include compatibility, benchmark, safety, and known-limitation guidance.
- CI now runs Go checks, race tests, integration tests, Docker Compose smoke, and benchmark/demo-report artifact generation.

### Limitations

- Single-node only.
- Redis or Valkey remains the source of truth.
- Limited Redis command compatibility.
- Direct upstream writes may remain stale until local TTL expiration.
- Admin API has no built-in authentication in v0.1.
- Not production-ready.
