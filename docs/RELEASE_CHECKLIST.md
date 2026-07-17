# Release Checklist

## Before Release

- Confirm the worktree is clean.
- Confirm `README.md`, `README.ru.md`, `START_HERE_RU.md`, and `AGENT_PROMPT.md` match the real commands.
- Confirm `slizen.example.toml` matches `internal/config.Config`.
- Confirm limitations still say developer preview, single-node, and not a source of truth.

## Local Checks

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build ./...
make check
make release-check
```

## Docker Checks

```sh
make demo-up
make smoke
make demo
make demo-down
```

## Kubernetes Packaging

```sh
make validate-k8s
```

Confirm the raw sidecar example and default Helm render use `observe` mode, loopback admin access, bounded resources, exec probes, and a documented endpoint rollback. Confirm the chart renders no `ServiceMonitor` unless explicitly enabled.

## Benchmark

```sh
make demo-up
make benchmark
make benchmark-workload
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

Check that the workload JSON has four scenarios, runtime versions, an invocation-specific key prefix, and explicit `evidence_valid` fields. Use an exclusive Slizen process; check that valid scenarios include real cache hit ratio and origin GET reduction values from `/v1/status`.

`make release-check` enforces this benchmark as a release gate against its own Docker Compose stack: all four expected scenario names must appear exactly once, the Slizen version and commit must match the built image and CLI, the origin version must be known, the isolated prefix must be invocation-specific, and every scenario must have valid isolated evidence. The stable 99/1 skew must additionally have real cache hits and `proved_origin_get_reduction=true`; a uniform or rapidly moving workload is allowed to report no win without invalidating otherwise sound evidence. Helm, Docker Compose, a running Docker daemon, and `jq` are required; none of these release checks are skipped.

## Docs

- `docs/REDIS_COMPATIBILITY.md` matches command handling.
- `docs/BENCHMARKING.md` explains how to reproduce the benchmark.
- `docs/STAGING_ROLLOUT.md` contains observe-to-cache and rollback gates.
- `docs/RELEASE_NOTES_v0.2.md` is ready to paste into GitHub Releases.

## Tag

```sh
git tag v0.2.0
git push origin v0.2.0
```

## GitHub Release Notes

Use `docs/RELEASE_NOTES_v0.2.md`.

## Known Limitations

- Developer preview.
- Single-node only.
- Redis or Valkey remains the source of truth.
- Cache and hotness state are disposable.
- Direct upstream writes can remain stale until local TTL expiration.
- Admin API has no built-in authentication.
- Limited Redis command compatibility.
