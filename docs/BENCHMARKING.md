# Benchmarking

Slizen includes a reproducible local hot-key benchmark for demo and regression evidence.

## Run

Start the Docker Compose demo:

```sh
make demo-up
```

Run the benchmark:

```sh
go run ./cmd/slizenctl benchmark hotkey \
  --proxy 127.0.0.1:6380 \
  --origin 127.0.0.1:6379 \
  --admin http://127.0.0.1:9090 \
  --key product:iphone_17 \
  --value '{"name":"iPhone 17","price":999}' \
  --warmup 5s \
  --duration 15s \
  --concurrency 32 \
  --requests 50000 \
  --output text \
  --json-file ./tmp/slizen-benchmark-result.json
```

Or use:

```sh
make benchmark
make demo-report
```

## What It Measures

`slizenctl benchmark hotkey` runs three phases:

1. `origin direct`: reads the key directly from Redis or Valkey.
2. `slizen cold`: reads through Slizen before intentionally warming the key.
3. `slizen hot`: warms the key, then reads through Slizen after promotion.

Latency and ops/sec are measured from real client requests. Cache hits, misses, upstream GETs, promotions, and invalidations are read from `/v1/status` before and after each phase.

## Interpreting Results

The main proof fields are:

- `cache_hit_ratio_percent`: percentage of Slizen hot-phase reads served from local cache.
- `upstream_get_reduction_percent`: reduction in upstream GETs per successful request compared with direct origin reads.
- `proved_reduction`: true only when the run produced cache hits and fewer upstream GETs during the hot phase.

If `proved_reduction` is false, the benchmark reports that honestly. Common causes are `observe` mode, insufficient warmup, a threshold that was not reached, or a workload that is not hot-key shaped.

## What This Is Not

This is not a scientific benchmark and not a production capacity claim. It runs on local hardware, local Docker networking, a single key, and one Slizen node.

Slizen is not always faster than direct Redis or Valkey. For cold keys, evenly distributed reads, small local deployments, or write-heavy workloads, the proxy hop can cost more than the cache saves.

Slizen is useful when the workload is read-heavy and skewed: one or a few keys receive enough repeated GET traffic that local cache hits reduce origin pressure.
