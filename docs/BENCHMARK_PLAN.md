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

The serial medians are 488.0 and 159.2 ns/op; the concurrent medians are 918.8 and 531.6 ns/op. Retained allocation bytes fell by about 95%, and allocation count fell by 75%. After this first pass, remaining concurrent cost was dominated by bounded cache-LRU, tracker, and drain-accounting synchronization. The drain-accounting component is addressed in the follow-up below; larger cache and tracker ownership changes remain outside this surgical pass. The benchmark still excludes RESP parsing and real socket I/O, so the result supports a narrower “lower Slizen hot-hit overhead” claim, not an end-to-end production latency guarantee.

Three JSON-backed local Docker hot-key repeats from the same working tree used 32 clients and 50,000 requests per phase on a fresh key each time. Their median direct Valkey p50/p95/p99 was 0.628/0.921/1.182 ms; the fully warm Slizen median was 0.632/0.970/1.277 ms with a 100% cache-hit ratio and zero upstream GETs in every repeat. The median warm-hit p99 tax was therefore 0.095 ms (about 8.0%) while median throughput remained within about 1%; this is evidence that the optimized warm path can approach direct latency while removing origin reads, not a universal latency promise. Three complete request-bound four-scenario gates also passed with exact sample-accounting invariants. Across those runs, mixed 99/1 read p99 was 1.36–1.54 ms through Slizen versus 1.02–1.15 ms direct, while origin GET reduction ranged from 71.4% to 79.2%. This wider adaptive-workload spread is why release evidence has no universal latency threshold and why future work should report repeated-run variance.

### Adaptive-promotion follow-up

The mixed 99/1 variance was traced to the global one-second scoring boundary. With the default two-window hysteresis, a key whose final required window was already guaranteed to qualify could still wait until the next boundary before entering `HOT`. The optimization commits only that inevitable final window: it uses the current count as a lower bound on the eventual full-window request rate, preserves the configured EWMA formula and threshold, and still requires the preceding completed qualifying windows.

Five alternating baseline/candidate Docker pairs used fresh Slizen processes, unique key prefixes, 100,000 operations per phase, 1,000 keys, 128-byte values, a 95/5 read/write mix, concurrency 32, and seed 42. The baseline was commit `e35792a`; both images were otherwise built and exercised on the same Apple M5 host.

| 99/1 metric | Baseline median (range) | Candidate median (range) | Median change |
| --- | ---: | ---: | ---: |
| Cache-hit ratio | 36.00% (35.91–45.15%) | 61.61% (60.78–69.48%) | +25.61 pp |
| Origin GET reduction | 72.57% (72.45–77.51%) | 85.92% (85.65–89.89%) | +13.35 pp |
| Slizen upstream GETs | 26,044 (21,360–26,160) | 13,371 (9,599–13,629) | -48.7% |
| Cache share of prevented GETs | 49.61% (49.54–58.25%) | 71.71% (70.97–77.29%) | +22.09 pp |
| Slizen read p99 | 1.364 ms (1.344–1.415) | 1.368 ms (1.282–1.462) | +0.004 ms |
| Read p99 tax versus paired direct | 0.334 ms (0.291–0.378) | 0.315 ms (0.264–0.422) | -0.019 ms |
| Slizen throughput | 34,717 ops/s (34,440–35,104) | 35,528 ops/s (33,564–35,742) | +2.3% |

All ten benchmark invocations and their 20 measured phases reached the request limit with zero failures, value mismatches, or final-validation mismatches. A separate complete four-scenario release gate also passed. Its moving-flash phase produced a 6.2% cache-hit ratio and 90.2% origin GET reduction; most moving-flash savings still came from request coalescing, so that figure is not presented as a cache-only win.

The added steady-state branch is measurable but bounded: 15 local `BenchmarkHotnessObservation` samples moved from a 24.35 ns/op median to 25.11 ns/op (+3.1%, or 0.76 ns/op), with zero allocations before and after. This is inside the 5% guard chosen before the experiment and is outweighed by the measured reduction in adaptation delay for the target workload.

### Drain-accounting follow-up

A mutex profile of baseline commit `86623ef` identified steady-state handler drain accounting as a concurrent dispatch bottleneck. Every successful request acquired the global drain mutex once during admission and twice around normal completion. The candidate uses a double-checked atomic reservation during admission and one completion critical section. A reservation that races with drain startup is rolled back and returns without executing a command; connection admission, drain startup, and completion signaling remain serialized by the existing mutex.

The identical `BenchmarkDrainTrackerHandlerParallel` harness was copied unmodified from the revision containing this section into a source archive of baseline commit `86623ef`; it is retained as `internal/proxy/drain_bench_test.go` in the candidate. It measures normal handler admission and completion with no connection or active drain. Both revisions used this command:

```sh
go test ./internal/proxy -run '^$' \
  -bench '^BenchmarkDrainTrackerHandlerParallel$' \
  -benchmem -benchtime=1s -count=10 -cpu=1,10,32
```

The raw ns/op samples were:

- `GOMAXPROCS=1`: baseline 12.30, 12.30, 12.32, 12.34, 12.37, 12.41, 12.42, 12.42, 12.50, 12.51; candidate 5.641, 5.639, 5.636, 5.662, 5.643, 5.643, 5.634, 5.629, 5.634, 5.656.
- `GOMAXPROCS=10`: baseline 212.8, 212.5, 212.6, 213.7, 211.8, 214.2, 211.9, 211.7, 213.3, 211.9; candidate 64.02, 62.07, 61.60, 62.65, 62.82, 61.43, 62.03, 62.71, 61.97, 62.52.
- `GOMAXPROCS=32`: baseline 202.7, 200.4, 201.0, 199.9, 200.4, 200.6, 203.9, 202.4, 201.1, 202.8; candidate 62.43, 63.04, 62.41, 61.93, 62.31, 62.98, 62.34, 63.02, 62.10, 62.92.

Their medians are:

| `GOMAXPROCS` | Baseline ns/op | Candidate ns/op | Lower ns/op | B/allocs before and after |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 12.39 | 5.640 | 54.5% | 0 / 0 |
| 10 | 212.55 | 62.295 | 70.7% | 0 / 0 |
| 32 | 201.05 | 62.42 | 69.0% | 0 / 0 |

The dispatch-level `BenchmarkProxyGETCacheHitParallel` adds the real cache-hit dispatch path and a pre-parsed command, but still uses a no-op benchmark connection. Each revision used the command below once per pair, alternating baseline-first and candidate-first order:

```sh
go test ./internal/proxy -run '^$' \
  -bench '^BenchmarkProxyGETCacheHitParallel$' \
  -benchmem -benchtime=1s -count=1 -cpu=10
```

| Pair | Execution order | Baseline ns/op | Candidate ns/op |
| ---: | --- | ---: | ---: |
| 1 | baseline, candidate | 539.8 | 381.2 |
| 2 | candidate, baseline | 535.1 | 378.9 |
| 3 | baseline, candidate | 456.1 | 392.7 |
| 4 | candidate, baseline | 542.1 | 396.7 |
| 5 | baseline, candidate | 554.1 | 380.0 |
| 6 | candidate, baseline | 538.8 | 374.5 |
| 7 | baseline, candidate | 537.0 | 392.6 |
| 8 | candidate, baseline | 452.3 | 394.1 |
| 9 | baseline, candidate | 538.4 | 382.9 |
| 10 | candidate, baseline | 544.6 | 384.6 |

All ten pairs favored the candidate. Median time was 538.6 versus 383.75 ns/op, or 28.7% lower, with paired reductions from 12.9% to 31.4%; benchmark allocations were unchanged at 15 B and 2 allocations per operation. An earlier independent ten-sample batch measured a smaller 14.9% reduction, from 445.8 to 379.45 ns/op, so the defensible conclusion is a repeatable reduction in local parallel dispatch contention rather than one universal percentage.

These benchmarks exclude TCP, RESP parsing, socket I/O, and upstream requests. `GOMAXPROCS=32` oversubscribes this Apple M5 and is a contention stress case. The results do not establish an end-to-end p99 or production-capacity improvement.

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
