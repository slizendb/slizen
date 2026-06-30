# Product Notes

Slizen is a self-hosted adaptive cache layer for read-heavy Redis and Valkey workloads.

The first valuable user workflow is not a distributed mesh. It is this:

1. Put Slizen in `observe` mode in front of a staging Redis/Valkey endpoint.
2. Show which keys create read skew.
3. Switch to `cache` mode for a controlled workload.
4. Measure whether origin QPS drops without unacceptable p99 latency cost.

## Product Boundaries

- Redis or Valkey remains the source of truth.
- Slizen is disposable cache and telemetry state.
- v0.1 is single-node.
- Direct upstream writes may remain stale in Slizen until local TTL expiration.
- Slizen intentionally supports only a small command set.
- Hot-key telemetry should use HMAC-based key identifiers by default.

## Release Position

Slizen v0.1 should be described as an experimental starter suitable for local demos and careful staging experiments, not as a production-ready database component.
