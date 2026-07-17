# Slizen v0.1.0 — Hot-key autopilot preview for Redis and Valkey

Slizen v0.1.0 is a developer preview of a single-node adaptive cache proxy for Redis and Valkey read-heavy workloads.

## What works

- Redis-compatible RESP proxy for a documented v0.1 command subset.
- `cache` mode with hot-key detection, bounded local cache, request coalescing, and write-driven invalidation.
- `observe` mode for safe telemetry without local cache hits or value storage.
- Admin API with `/healthz`, `/readyz`, `/v1/status`, `/v1/hotkeys`, `/v1/cache`, and Prometheus `/metrics`.
- HMAC key identifiers in hot-key output by default.
- Docker Compose demo with Valkey.
- `slizenctl benchmark hotkey` and `make demo-report` for reproducible local demo evidence.
- CI coverage for Go tests, race tests, real Valkey integration tests, Docker Compose smoke, and benchmark/demo-report artifacts.

## Quick start

```sh
git clone https://github.com/slizendb/slizen.git
cd slizen
make demo-up
make demo
curl http://127.0.0.1:9090/v1/status
make demo-down
```

## Demo

```sh
make demo-up
make demo
make demo-down
```

The demo starts Valkey and Slizen, waits for readiness, writes and reads a test key through the Slizen RESP proxy, runs a short hot-key load, and prints status/hot-key evidence.

## Benchmark

```sh
make demo-up
make benchmark
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

The benchmark compares direct origin GETs with Slizen cold and hot reads. It reports cache hit ratio and upstream GET reduction from real `/v1/status` counters. It is local demo evidence, not a scientific production benchmark.

## Safety model

- Redis or Valkey remains the source of truth.
- Slizen cache and hotness state are disposable.
- Writes are safest when they pass through Slizen because accepted writes invalidate local cache entries.
- Direct upstream writes may remain stale through Slizen until local TTL expiration.
- Keep the admin API private. v0.1 has no built-in admin authentication.
- Slizen should not log values, authentication data, or raw Redis keys in metrics/logs.

## Known limitations

- Developer preview; not production-ready.
- Single-node only.
- Limited Redis command compatibility.
- No mesh, gossip, Redis Cluster, Pub/Sub, transactions, blocking commands, RESP3, Kubernetes integration, or built-in auth.
- Local benchmark results depend on machine, Docker, Redis/Valkey, and workload shape.
- `observe` mode measures heat but does not reduce origin load.

## What is next

- Keep tightening compatibility tests and documentation.
- Finish the privacy-safe hot-key audit and skewed workload harness.
- Package a safe observe-first Kubernetes sidecar and Helm deployment.
- Research and implement fail-safe Redis/Valkey-assisted invalidation after v0.2 workload evidence.
- Consider mesh or fleet management only after real users demonstrate repeated demand.
