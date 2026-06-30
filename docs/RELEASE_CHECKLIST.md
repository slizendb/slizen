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

## Benchmark

```sh
make demo-up
make benchmark
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

Check that the report includes real cache hit ratio and upstream GET reduction values from `/v1/status`.

## Docs

- `docs/REDIS_COMPATIBILITY.md` matches command handling.
- `docs/BENCHMARKING.md` explains how to reproduce the benchmark.
- `docs/RELEASE_NOTES_v0.1.md` is ready to paste into GitHub Releases.

## Tag

```sh
git tag v0.1.0
git push origin v0.1.0
```

## GitHub Release Notes

Use `docs/RELEASE_NOTES_v0.1.md`.

## Known Limitations

- Developer preview.
- Single-node only.
- Redis or Valkey remains the source of truth.
- Cache and hotness state are disposable.
- Direct upstream writes can remain stale until local TTL expiration.
- Admin API has no built-in authentication.
- Limited Redis command compatibility.
