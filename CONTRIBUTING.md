# Contributing

Slizen is intentionally narrow. Before adding a feature, explain which measured failure mode or user workflow it solves.

Use the repository's bug, feature, or design-partner issue form before starting a broad change. Suspected vulnerabilities belong in [private vulnerability reporting](https://github.com/slizendb/slizen/security/advisories/new), never a public issue.

## Local Checks

```sh
make check
make release-check
make vulncheck
```

For individual steps:

```sh
make fmt
make vet
make test
make race
make build
make vulncheck
```

## Style Expectations

- Keep Redis or Valkey as the source of truth.
- Prefer small, testable changes over broad rewrites.
- Use `context.Context`, bounded memory, explicit errors, and deterministic tests.
- Avoid new dependencies unless the maintenance and security cost is justified.
- Do not claim production readiness without an explicit release decision.

## Protocol And Compatibility

- Do not add new Redis commands without tests.
- Update `docs/REDIS_COMPATIBILITY.md` when command behavior changes.
- Rejected commands should return clear RESP errors without breaking the connection.
- Add real Valkey integration coverage when behavior depends on Redis/Valkey protocol details.

## Privacy And Metrics

- Do not log cached values, passwords, authentication data, or raw sensitive keys.
- Do not use Redis keys or unbounded user input as Prometheus labels.
- Keep admin hot-key output hashed by default.

## Pull Requests

- Preserve the Redis/Valkey source-of-truth boundary.
- Add tests for protocol, cache, and consistency semantics.
- Document user-visible consistency changes.
- Include benchmark evidence for hot-path changes.
- Treat non-zero workload `value_mismatches` as invalid evidence, even when cache-hit or origin-reduction counters look favorable.

## Commit Style

Use small imperative commits:

```text
proxy: reject unsupported SET GET option
service: add observation-only mode
bench: add hot-key benchmark output
```
