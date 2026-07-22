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

Existing benchmarks cover cache operations, steady-state hotness observation, key hashing, a serial handler-level proxy GET cache hit, and a concurrent dispatch-level proxy GET cache hit with allocation tracking. Both proxy benchmarks explicitly opt into cache mode and fail if any measured GET reaches the upstream; the concurrent variant also includes handler drain accounting and deadline bookkeeping through a lightweight benchmark connection.

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

The hotness benchmark uses 1,000 pre-registered keys and measures steady-state `Observe` calls without key construction in the timed loop. The serial proxy benchmark invokes the command handler directly for a verified local cache hit; it includes command validation, service cache/hotness work, metrics, and response dispatch, but excludes RESP parsing, TCP, Redis client work, and the miss-only timeout context. The concurrent variant calls the dispatch path, adding handler admission/completion and no-op connection deadline calls, while still excluding RESP parsing, socket I/O, and operating-system deadline syscalls. The recorded table predates the concurrent variant, so collect repeated samples before treating it as a baseline. These figures are local evidence, not a regression threshold or a production capacity claim. No production optimization was made in this measurement slice.

## v0.2.2 Cache-hit Performance Pass: 2026-07-22

Environment:

- Go: `go1.26.5`;
- OS/architecture: `darwin/arm64`;
- CPU: Apple M5;
- repeated samples: `-count=5`.

Before profiling, the serial benchmark was corrected to opt into cache mode so the timed operation was a proven local hit; the same corrected benchmark code was used for the before and after samples. CPU, mutex, and allocation profiles then identified miss-only context creation, repeated tracker/cache-stat locks, Prometheus label lookup, generic GET dispatch, and drain accounting as the measurable proxy tax.

| Benchmark | Before ns/op | After ns/op | Median change | Before B/allocs | After B/allocs |
| --- | --- | --- | ---: | ---: | ---: |
| `BenchmarkProxyGETCacheHit` | 488.0, 481.8, 487.4, 544.9, 502.1 | 162.0, 159.2, 158.5, 158.8, 159.2 | -67.4% | 320 / 8 | 16 / 2 |
| `BenchmarkProxyGETCacheHitParallel` | 920.4, 907.1, 912.2, 926.1, 918.8 | 449.5, 531.6, 442.3, 562.0, 539.2 | -42.1% | 320 / 8 | 15 / 2 |

The serial medians are 488.0 and 159.2 ns/op; the concurrent medians are 918.8 and 531.6 ns/op. Retained allocation bytes fell by about 95%, and allocation count fell by 75%. Remaining concurrent cost is dominated by bounded cache-LRU, tracker, and drain-accounting synchronization; changing those ownership models is intentionally outside this surgical pass. The benchmark still excludes RESP parsing and real socket I/O, so the result supports a narrower “lower Slizen hot-hit overhead” claim, not an end-to-end production latency guarantee.

Three JSON-backed local Docker hot-key repeats from the same working tree used 32 clients and 50,000 requests per phase on a fresh key each time. Their median direct Valkey p50/p95/p99 was 0.628/0.921/1.182 ms; the fully warm Slizen median was 0.632/0.970/1.277 ms with a 100% cache-hit ratio and zero upstream GETs in every repeat. The median warm-hit p99 tax was therefore 0.095 ms (about 8.0%) while median throughput remained within about 1%; this is evidence that the optimized warm path can approach direct latency while removing origin reads, not a universal latency promise. Three complete request-bound four-scenario gates also passed with exact sample-accounting invariants. Across those runs, mixed 99/1 read p99 was 1.36–1.54 ms through Slizen versus 1.02–1.15 ms direct, while origin GET reduction ranged from 71.4% to 79.2%. This wider adaptive-workload spread is why release evidence has no universal latency threshold and why future work should report repeated-run variance.

## v0.2 Workload Harness

`slizenctl benchmark workload` now includes:

- [x] uniform distribution;
- [x] 80/20-like skew;
- [x] 99/1-like skew;
- [x] moving flash key;
- [x] configurable read/write ratio;
- [x] configurable value size, concurrency, duration, operation cap, and deterministic seed;
- [x] JSON output with backward-compatible aggregate p50/p95/p99 latency plus separate read, write, and final-validation sample counts and distributions;
- [x] explicit request-limit versus duration-limit termination attribution, origin GET reduction, cache hit ratio, and runtime versions.

The harness produces reproducible local evidence, not a production capacity claim. Future benchmark work should prioritize anonymized real-workload traces and repeated-run variance rather than adding synthetic scenarios without user evidence.
