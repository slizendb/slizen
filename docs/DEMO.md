# Demo

The Slizen demo is a local Docker Compose proof path for the v0.1 developer preview.

## What The Demo Shows

- Slizen can run as a Redis/Valkey RESP proxy in front of Valkey.
- `/healthz`, `/readyz`, `/v1/status`, `/v1/hotkeys`, and `/metrics` are reachable.
- A client can write and read a key through the Slizen proxy.
- A repeated hot-key workload can produce local cache hits in `cache` mode.
- Benchmark/report artifacts can show cache hit ratio and upstream GET reduction from real counters.

Run it:

```sh
make demo-up
make demo
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

## Reading Benchmark Output

`slizenctl benchmark hotkey` prints three phases:

- `origin direct`: reads the key directly from Redis or Valkey.
- `slizen cold`: reads through Slizen before intentional hot-key warmup.
- `slizen hot`: reads through Slizen after warmup and promotion.

The important fields are:

- `Cache Hit %`: percentage of Slizen hot-phase reads served from local cache.
- `Upstream GETs`: origin GET requests observed by Slizen during that phase.
- `upstream_get_reduction`: reduction in upstream GETs per successful request compared with direct origin reads.
- `proved_reduction`: true only when the run produced cache hits and fewer upstream GETs.

## What Not To Promise

- Do not claim production readiness.
- Do not claim Slizen is always faster than Redis or Valkey.
- Do not claim full Redis compatibility.
- Do not claim multi-node replication, Redis Cluster support, transactions, Pub/Sub, RESP3, or built-in auth.
- Do not treat local benchmark numbers as production capacity numbers.

## Why Direct Redis Can Be Faster For One Request

For a single cold request, Slizen adds a proxy hop and bookkeeping. Direct Redis or Valkey can be faster when the workload is evenly distributed, cold, write-heavy, or already close to the application.

## Why Slizen Helps During Hot-Key Bursts

Slizen is useful when many clients repeatedly read the same key or small key set. After promotion, local cache hits can serve repeated reads without sending every GET to the origin, reducing upstream pressure during skewed read-heavy bursts.
