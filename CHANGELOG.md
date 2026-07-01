# Changelog

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
