# Contributing

Slizen is intentionally narrow. Before adding a feature, explain which measured failure mode or user workflow it solves.

## Local Checks

```sh
make fmt
make vet
make test
make race
make build
```

## Pull Requests

- Preserve the Redis/Valkey source-of-truth boundary.
- Add tests for protocol, cache, and consistency semantics.
- Document user-visible consistency changes.
- Avoid new dependencies unless the maintenance and security cost is justified.
- Include benchmarks for hot-path changes.
- Never label the project production-ready without an explicit release decision.

## Commit Style

Use small imperative commits:

```text
proxy: reject unsupported SET GET option
service: add observation-only mode
bench: add Zipf workload generator
```
