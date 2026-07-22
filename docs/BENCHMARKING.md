# Benchmarking

Slizen ships two local benchmark paths:

- `benchmark hotkey` is the v0.1 single-key demo and remains available for existing scripts.
- `benchmark workload` is the v0.2 release workload harness for uniform, skewed, and moving-hot-key traffic, with key-and-write-version value verification from v0.2.1, request-limit plus phase-latency attribution from v0.2.2, and fixed cache-miss attribution in the v0.2.3 release candidate.

Both paths produce local evidence for a specific machine and configuration. They are not scientific benchmarks or universal production capacity claims.

## Start the local stack

```sh
make demo-up
```

Use a dedicated Redis or Valkey instance and a quiescent, exclusive Slizen process. The workload harness creates persistent keys under a unique invocation-specific suffix and runs writes against them. The unique namespace gives every run unseen hotness state without a production hotness-reset endpoint. The Docker Compose demo caches only the reviewed `product:` prefix, so the example below uses `product:slizen:benchmark`; the CLI's generic default remains `slizen:benchmark`.

When per-prefix cache policy is enabled, choose a `--key-prefix` covered by the policy you intend to measure. A bypassed prefix should correctly report no local-cache reduction.

## Run the v0.2 workload suite

The default `all` selection runs all four scenarios:

```sh
go run ./cmd/slizenctl benchmark workload \
  --proxy 127.0.0.1:6380 \
  --origin 127.0.0.1:6379 \
  --admin http://127.0.0.1:9090 \
  --scenario all \
  --key-prefix product:slizen:benchmark \
  --keys 1000 \
  --value-size 1024 \
  --read-ratio 95 \
  --concurrency 32 \
  --duration 10s \
  --requests 100000 \
  --seed 42 \
  --flash-every 5000 \
  --output text \
  --json-file ./tmp/slizen-workload-result.json
```

Select one scenario with `--scenario uniform`, `--scenario skew-80-20`, `--scenario skew-99-1`, or `--scenario moving-flash`. Use `--output json` to write JSON to stdout; `--json-file` writes the same structured result independently of stdout format.

`--duration` and `--requests` are both issuance limits for the generated operations in each measured phase. The harness stops issuing at either limit, lets already-issued operations finish under the client's bounded network timeouts, then performs one final validation GET for every key successfully written during that phase. Each phase records `operation_attempts` for generated GET/SET operations and a `termination_reason` of `request_limit` or `duration_limit`; final validation is outside both issuance limits. The backward-compatible `requests` and `reads` totals include successful final-validation GETs, so `requests` can exceed `--requests` only by those reads. `--read-ratio 95` means approximately 95 percent GET and 5 percent SET operations. `--read-ratio 100` produces a read-only workload and needs no final write validation.

For `moving-flash` and `all`, `--flash-every` must be smaller than `--requests`; otherwise the advertised hot key could never move. A duration-limited run may still finish before a move, so choose limits that let at least `--flash-every + 1` operations complete.

### Scenarios

| Scenario | Deterministic request shape |
| --- | --- |
| `uniform` | Keys are selected uniformly from the configured key set. |
| `skew-80-20` | Approximately 80 percent of operations select 20 percent of the keys; the rest select the remaining keys. |
| `skew-99-1` | Approximately 99 percent of operations select 1 percent of the keys; the rest select the remaining keys. |
| `moving-flash` | Approximately 99 percent of operations select one flash key. The flash key advances after `--flash-every` operations. |

These are controlled workload shapes, not claims that every real traffic distribution follows those ratios.

### Reproducibility

The seed and zero-based operation index determine the operation type and selected key. Selection does not depend on goroutine scheduling, and the same seed and configuration produce the same operation sequence prefix. `isolated_key_prefix` is intentionally different for each invocation; it isolates disposable cache/hotness state and does not change the index distribution.

Wall-clock duration can still change how much of that prefix completes. For closer comparisons, keep the machine idle, reuse the same runtime configuration, set `--requests` low enough that both phases reach the request limit before `--duration`, and confirm that every compared phase reports `termination_reason: "request_limit"`.

### Measurement method

For each selected scenario, the harness:

1. Generates bounded keys and deterministic fixed-size, key-and-write-version values and seeds generation zero directly into the origin.
2. Runs the deterministic workload directly against the origin.
3. Validates the final generation of every written key, then resets the origin dataset to generation zero.
4. Initializes all benchmark client connections, then purges both disposable Slizen cache tiers.
5. Runs the same deterministic workload shape through Slizen and validates every written key's final generation.
6. Reads Slizen counters before and after the proxy phase.

Operations for the same key are ordered by the harness: GETs may remain concurrent with other GETs, while a SET and its surrounding reads cannot overlap. Every successful read is compared with the exact generation expected at that point. This makes a cached pre-write generation a value mismatch instead of a valid hit. Operations on different keys remain concurrent.

The JSON result includes:

- backward-compatible aggregate p50, p95, and p99 end-to-end harness latency for all successful generated GETs, generated SETs, and final-validation GETs; generated operations include time waiting for the per-key ordering lock;
- `read_latency` and `write_latency` for the Redis command after per-key ordering has been acquired, plus `read_ordering_wait_latency` and `write_ordering_wait_latency` for the lock wait itself; each object has its own successful sample count and p50/p95/p99 distribution;
- `final_validation_latency` for final-validation GET commands, which do not use the per-key ordering lock;
- successful reads and writes, failures, elapsed time, and operations per second for each phase;
- generated `operation_attempts` and `termination_reason`, which distinguish request-bound from duration-bound phases;
- `value_mismatches` for successful GET responses that did not match the expected key and write generation;
- `validation_reads`, `validation_failures`, and `validation_mismatches` for the final post-write generation check;
- origin GET reduction normalized per successful GET;
- Slizen cache hit ratio from `/v1/status` counter deltas;
- cache miss deltas attributed to the fixed `policy_bypass`, `not_admitted`, and `not_present` reasons;
- Slizen CLI and daemon versions, origin version, and the benchmark CLI's Go, operating system, and architecture information.
- an `evidence_valid` flag and notes explaining any failed, mismatched, non-isolated, reset, or restarted measurement.

The origin version is read with `INFO server`. If the origin ACL denies that command or the version is absent, the field is `unknown` and the result records a note instead of failing the workload.

`origin_get_reduction_percent` can be negative if a valid run observes more origin GETs per successful read through Slizen than in the direct phase. `proved_origin_get_reduction` is true only when the measured proxy phase has cache hits, a positive reduction, no failed operations, zero value mismatches, monotonic daemon identity/counters, and an exact process-global request delta. Any stale-generation, cross-key, truncated, or corrupted GET payload invalidates the evidence instead of being counted as success. Any unrelated traffic through the same Slizen process also invalidates the reduction evidence instead of being silently attributed to the benchmark. A zero or false result is valid evidence, especially for uniform, write-heavy, short, or `observe`-mode runs.

The top-level phase and scenario p50/p95/p99 fields remain mixed end-to-end aggregate distributions for schema compatibility. Use the command and ordering-wait objects to distinguish Redis round-trip time from harness serialization. Slizen read command latency still combines cache hits and misses: `/v1/status` provides phase-wide hit and attributed-miss counter deltas, not a per-request cache outcome that can be joined safely to individual latency samples. A latency object is omitted when its operation class has no successful samples; this keeps the workload-only objects out of `benchmark hotkey` JSON. Compare runs only when value size, ratio, concurrency, limits, termination reason, scenario, seed, Slizen configuration, and runtime environment match.

### v0.2.3 release-candidate local repeats

Five local Docker repeats used the unchanged cold, request-bound `skew-99-1` workload with seed 42, 1,000 keys, 100,000 generated operations per phase, a 95/5 read/write mix, 128-byte values, and concurrency 32. Every direct phase made `94,961` origin GETs. Slizen made `798`–`803`, a `99.154390%`–`99.159655%` reduction, with a `99.121745%`–`99.151231%` cache-hit ratio. Every phase reached the request limit with zero request failures, value mismatches, final-validation failures, and final-validation mismatches.

Slizen read p99 was `1.175`–`1.251 ms`, while direct read p99 was `0.986`–`1.042 ms`. These results support a narrow origin-load statement for this exact local workload; they do not establish that Slizen is faster, guarantee 99% reduction for another distribution, or replace tagged immutable-image evidence. v0.2.3 is still a release candidate, and publication must regenerate the bundle from the exact image digest.

### Resource bounds

The CLI rejects configurations outside these limits:

- concurrency: 1 to 1,024 workers;
- measured operations: 1 to 1,000,000 per phase;
- keys: 2 to 100,000 per scenario; `skew-80-20` needs at least 5, while `all` and `skew-99-1` need at least 100;
- value size: 16 bytes to 1 MiB; the first 16 bytes bind the write generation and key identity;
- aggregate generated dataset: 256 MiB across selected scenarios;
- duration: greater than zero and at most one hour;
- generated key prefix: at most 128 bytes.
- admin status response: at most 64 KiB; other CLI admin JSON responses are capped at 4 MiB.

Latency samples are bounded by the operation limit plus at most one final validation GET per key, seed pipelines are bounded by bytes, and scenario/key state is bounded by the configured dataset limits. Final validation reuses the configured concurrency and has a bounded 10-second-to-one-minute deadline derived from the phase duration.

## Run the v0.1 hot-key benchmark

Existing demo and report scripts continue to use:

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

`benchmark hotkey` runs `origin direct`, `slizen cold`, and `slizen hot` phases. It reports client latency plus cache hits, misses, and origin GET reduction from `/v1/status`. Its JSON schema remains additive-compatible with the v0.1 demo report and now includes runtime versions.

## Interpreting results

Slizen is not always faster than a direct local Redis or Valkey connection. The extra proxy hop can cost more than it saves for cold keys, uniform traffic, small deployments, short tests, or write-heavy workloads.

The intended signal is narrower: under a repeated, read-heavy skew, determine whether local cache hits reduce measured origin GET pressure while keeping latency acceptable for that environment. Repeat runs and preserve the JSON artifacts before using the result to make a rollout decision.

## Release evidence

`make release-check` validates tagged source with 1,000 keys, concurrency 32, a fixed cap of 100,000 generated operations per phase, and a 30-second safety duration. The 20,000-operation moving-flash interval yields five deterministic flash windows when the cap is reached. Release evidence requires the exact `uniform`, `skew-80-20`, `skew-99-1`, and `moving-flash` scenario set. Every origin and Slizen phase must report exactly 100,000 `operation_attempts`, `termination_reason: "request_limit"`, command-latency samples matching those attempts, final-validation samples matching `validation_reads`, and total `requests` matching attempts plus validation reads. Slizen miss reasons must be numeric and sum to its total cache misses. An unexpectedly slow or internally inconsistent phase fails the reproducibility gate explicitly. It also requires all four scenarios to have zero failures and zero `value_mismatches`; the stable 99/1 scenario must prove positive origin GET reduction with real cache hits. These are correctness and reproducibility gates, not pass/fail latency, capacity, or universal origin-reduction thresholds.

After a tag passes validation, the release workflow publishes the multi-architecture image, records GitHub-native provenance, and runs `scripts/release_evidence.sh` against the exact `ghcr.io/slizendb/slizen@sha256:...` image. The resulting manifest binds the image digest, full commit, version, workload JSON, demo report, and SHA-256 checksums.

The manually dispatched `extended-validation` workflow runs 100,000-cardinality microbenchmarks five times and a 100,000-key, concurrency-128 workload without a universal latency threshold. It is intentionally separate from every-push and release CI because shared runners are not a stable performance baseline and the run is materially more expensive. Longer soak, resource profiling, upstream outage, and restart/drain drills still require dedicated engineering evidence.
