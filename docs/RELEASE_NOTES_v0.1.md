# Slizen v0.1.0 — Hot-key autopilot preview for Redis and Valkey

Slizen v0.1.0 is a developer preview of a single-node adaptive cache proxy for Redis and Valkey read-heavy workloads.

## What Works

- RESP proxy on `:6380`.
- Admin API and Prometheus metrics on `:9090`.
- `observe` mode for safe hot-key telemetry without local cache hits.
- `cache` mode with hot-key promotion, bounded local TTL cache, and request coalescing.
- Write-driven invalidation when writes pass through Slizen.
- HMAC key identifiers in `/v1/hotkeys` by default.
- Docker Compose demo with Valkey.
- `slizenctl benchmark hotkey` for reproducible local demo evidence.

## Intentionally Not Supported

- Mesh, gossip, or distributed replication.
- Redis Cluster.
- Pub/Sub.
- Transactions.
- Blocking commands.
- RESP3.
- Kubernetes integration.
- Built-in authentication.
- Durable storage or source-of-truth behavior.

## Quick Start

```sh
make demo-up
make demo
curl http://127.0.0.1:9090/v1/status
make demo-down
```

## Demo Command

```sh
make demo
```

## Benchmark Command

```sh
make demo-up
make benchmark
make demo-report
```

The benchmark compares direct origin GETs with Slizen cold and hot reads, then reports cache hit ratio and upstream GET reduction from real `/v1/status` counters.

## Safety Notes

- Redis or Valkey remains the source of truth.
- Slizen cache and hotness state are disposable.
- Direct upstream writes may remain stale through Slizen until local TTL expiration.
- Keep the admin API private; v0.1 has no built-in auth.
- Test with your workload before production.

## Limitations

- Developer preview, not production-ready.
- Single-node only.
- Limited Redis command compatibility.
- Local benchmark results are workload and machine dependent.
- `observe` mode measures heat but does not reduce origin load.

## Roadmap

- More compatibility tests and documented command coverage.
- More benchmark workloads beyond one fixed hot key.
- Per-prefix cache policy.
- RESP3 and server-assisted invalidation research.
- Static multi-node membership after the single-node contract is stable.
