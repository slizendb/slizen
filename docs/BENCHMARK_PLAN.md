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

Existing benchmarks cover cache operations, hotness observation, and key hashing. Future work should add proxy-level GET benchmarks and allocation tracking.

## Future Workloads

A release-grade benchmark harness should include:

- uniform distribution;
- Zipf-like 80/20 skew;
- Zipf-like 99/1 skew;
- moving flash key;
- configurable read/write ratio;
- configurable value size, concurrency, and duration;
- JSON output with p50/p95/p99 latency and runtime versions.
