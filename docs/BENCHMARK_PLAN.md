# Benchmark Plan

Slizen should not claim performance wins without reproducible measurements.

## Local Smoke Benchmarks

Use `scripts/loadtest.sh` to compare:

1. direct Valkey requests;
2. Slizen before promotion;
3. Slizen after promotion.

Report:

- total operations;
- elapsed time;
- operations per second;
- cache hit ratio;
- Slizen upstream request count.

This is a local demonstration, not a scientific production benchmark.

## Go Microbenchmarks

Run:

```sh
go test -bench=. ./...
```

Existing benchmarks cover cache operations, steady-state hotness observation, key hashing, and a handler-level proxy GET cache hit with allocation tracking.

## Allocation Baseline: 2026-07-17

Environment:

- base revision: `0943efc` plus the working-tree changes that add this benchmark set;
- Go: `go1.26.4`;
- OS/architecture: `darwin/arm64`;
- CPU: Apple M5;
- benchmark parallelism suffix: `-10`.

Command:

```sh
go test -run '^$' \
  -bench '^(BenchmarkCacheHit|BenchmarkCacheMiss|BenchmarkHotnessObservation|BenchmarkProxyGETCacheHit)$' \
  -benchmem -count=5 \
  ./internal/cache ./internal/hotness ./internal/proxy
```

Recorded samples:

| Benchmark | ns/op samples | B/op | allocs/op |
| --- | --- | ---: | ---: |
| `BenchmarkCacheHit` | 58.46, 57.23, 58.44, 58.52, 57.79 | 8 | 1 |
| `BenchmarkCacheMiss` | 36.15, 35.89, 36.18, 36.96, 36.08 | 0 | 0 |
| `BenchmarkHotnessObservation` | 20.62, 20.35, 20.11, 20.07, 20.04 | 0 | 0 |
| `BenchmarkProxyGETCacheHit` | 467.8, 452.0, 452.2, 455.2, 454.2 | 320 | 8 |

The hotness benchmark uses 1,000 pre-registered keys and measures steady-state `Observe` calls without key construction in the timed loop. The proxy benchmark invokes the RESP handler directly for a verified local cache hit; it includes command parsing, request context creation, service cache/hotness work, metrics, and response dispatch, but excludes TCP and Redis client work. These figures are a local baseline, not a regression threshold or a production capacity claim. No production optimization was made in this measurement slice.

## v0.2 Workload Harness

`slizenctl benchmark workload` now includes:

- [x] uniform distribution;
- [x] 80/20-like skew;
- [x] 99/1-like skew;
- [x] moving flash key;
- [x] configurable read/write ratio;
- [x] configurable value size, concurrency, duration, operation cap, and deterministic seed;
- [x] JSON output with p50/p95/p99 latency, origin GET reduction, cache hit ratio, and runtime versions.

The harness produces reproducible local evidence, not a production capacity claim. Future benchmark work should prioritize anonymized real-workload traces and repeated-run variance rather than adding synthetic scenarios without user evidence.
