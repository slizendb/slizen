# Changelog

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
