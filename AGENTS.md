# Slizen Project Rules

Slizen is a self-hosted adaptive cache layer for Redis and Valkey. Version 0.2 is a single-node Redis-compatible read proxy with bounded per-prefix `deny`, `observe`, and `cache` policies, a bounded local cache, hot-key detection, request coalescing, a privacy-safe audit, reproducible workload evidence, metrics, an admin API, CLI tooling, and observe-first Kubernetes packaging.

## Product Boundaries

- Redis or Valkey remains the source of truth.
- Slizen is not a durable database, PostgreSQL replacement, Redis Cluster replacement, consensus system, transactional store, source of truth, fully Redis-compatible server, or distributed mesh in v0.2.
- The cache and hotness state are disposable and may be lost on restart.
- Writes are safest when they pass through Slizen. Direct upstream writes may remain stale until local TTL expiration.
- In `observe` mode, Slizen must forward reads and collect telemetry without serving or storing local cached values.

## Engineering Rules

- Use Go and the module path `github.com/slizendb/slizen`.
- Prefer the standard library unless an approved dependency clearly reduces risk.
- Approved dependencies are:
  - `github.com/tidwall/redcon`
  - `github.com/redis/go-redis/v9`
  - `github.com/prometheus/client_golang`
  - `github.com/pelletier/go-toml/v2`
  - `golang.org/x/sync/singleflight`
- Document dependencies in `docs/DEPENDENCIES.md`.
- Use `context.Context`, `log/slog`, graceful shutdown, bounded memory, deterministic tests, dependency injection where useful, and explicit interfaces around storage, upstream access, hotness tracking, and clocks.
- Do not use global mutable state.
- Do not silently swallow errors.
- Do not log cached values, passwords, authentication data, or complete sensitive keys.
- Never use Redis keys or unbounded user input as Prometheus labels.
- Bound HTTP bodies, cache memory, and hotness tracking memory.
- Do not leave v0.2 core behavior as TODOs. TODOs are acceptable only for documented post-v0.2 roadmap items.

## Verification

Run these before finishing changes when a Go toolchain is available:

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

Docker, workload, and Kubernetes packaging verification:

```sh
make validate-k8s
make smoke
make demo-up
make benchmark-workload
make demo-down
```
