# Slizen v0.2.1 — Launch hardening

Slizen v0.2.1 keeps the v0.2 single-node developer-preview scope and hardens its defaults, resource admission, workload evidence, and release supply chain. Redis or Valkey remains the source of truth.

## What changed

- Fresh installs start in `observe` mode: supported reads are forwarded and measured, never stored or served locally.
- Selective caching now has a documented safe pattern: global `cache` mode, an empty-prefix `observe` catch-all, and explicit narrower `cache` policies.
- When no stable HMAC key is configured, Slizen generates a cryptographically random process-local secret instead of using a shared placeholder. It is never logged; anonymized IDs change after restart.
- RESP commands have configurable, hard-capped byte, argument, and MGET-key admission limits; concurrent accepted connections are bounded.
- Cache status reads no longer discard expired values that may still be eligible for an explicitly configured stale-grace fallback. Reported cache entries and bytes describe bounded retained storage.
- Hot-key summary counts no longer scan the full tracker on every status request, and at-capacity unseen-key admission now uses bounded deterministic eviction instead of an O(n) scan under the tracker lock.
- Shared GET refills survive an individual caller cancellation but are capped by the stricter proxy/upstream read timeout and canceled when the service closes.
- Rejecting an oversized pipelined command now discards the already parsed tail before closing the connection, so a trailing mutation cannot reach the origin.
- The workload benchmark orders same-key writes and reads, verifies deterministic key-and-write-version payloads, and checks every written key's final generation. `value_mismatches` and validation counters are additive JSON evidence; any mismatch invalidates the scenario.
- Release Actions and container bases are digest/SHA pinned, Dependabot update paths are declared, OCI labels include Apache-2.0, and the canonical license plus notice ship in the image.
- The minimum Go toolchain and pinned builder are 1.26.5, which includes the standard-library fix for GO-2026-5856.
- A tag must pass the full source release gate before its multi-architecture image is published. The workflow then attaches GitHub-native provenance and generates checksummed evidence from the exact image digest.

## Upgrade notes

- Configurations that explicitly set `mode = "cache"` keep cache behavior. Configurations that omitted `mode` now start observe-only and require an explicit promotion decision.
- Keep an empty-prefix `observe` policy when enabling global cache mode for selected prefixes; unmatched keys otherwise inherit the global mode.
- If anonymized key IDs must survive restart, load `privacy.key_hash_secret` or `SLIZEN_KEY_HASH_SECRET` from a secret manager. Otherwise leave it unset.
- Review the new `proxy.max_command_bytes`, `proxy.max_command_args`, `proxy.max_mget_keys`, and `proxy.max_connections` defaults before high-concurrency staging.
- `cache_entries` and `cache_bytes` are retained-memory counters. An expired entry may remain bounded in storage until access or eviction, including during stale grace.

## Install and verify

```sh
docker pull ghcr.io/slizendb/slizen:0.2.1
docker image inspect ghcr.io/slizendb/slizen:0.2.1 \
  --format '{{index .Config.Labels "org.opencontainers.image.revision"}}'
gh attestation verify oci://ghcr.io/slizendb/slizen:0.2.1 \
  --repo slizendb/slizen
```

Use the digest from `release-evidence-manifest.json` for an immutable deployment. The same bundle includes `SHA256SUMS`, the demo report, benchmark JSON, workload JSON, status snapshots, hot-key output, and the privacy-safe audit.

Published release identity:

- Commit: `4ba2c1c5c9a1c89073ba47d90c83f98441dfe9a1`
- Multi-architecture image: `ghcr.io/slizendb/slizen@sha256:4006733aa64b6f55f25855f48a026d7b488ed44b5ad82d1a52ef5968d08daece`
- [GitHub Release and checksummed evidence](https://github.com/slizendb/slizen/releases/tag/v0.2.1)
- [Release workflow](https://github.com/slizendb/slizen/actions/runs/29918332099)
- [100,000-key extended validation](https://github.com/slizendb/slizen/actions/runs/29917703703)

## Evidence contract

The release gate runs uniform, 80/20-like, 99/1-like, and moving-flash scenarios with 1,000 keys, concurrency 32, and 10 seconds per phase. All scenarios must have zero failures and zero value mismatches. The stable 99/1 scenario must additionally show real cache hits and positive measured origin GET reduction. There is deliberately no universal latency or capacity threshold on a shared runner.

The image-bound v0.2.1 run completed all four scenarios with zero request failures and zero value or final-validation mismatches. Its synthetic 99/1 scenario measured 88.734% fewer origin GETs and a 65.736% cache-hit ratio; proxy p99 was `2.856 ms` versus `1.777 ms` direct, so this is evidence of upstream-load reduction for that run, not a speed claim. A separate 100,000-key validation also completed all four scenarios without failures or mismatches; its raw JSON and five-run high-cardinality benchmark output are attached to the release.

The v0.2.0 release's earlier 100-key synthetic 99/1 run measured 91.376% fewer origin GETs per successful read, while proxy p99 was higher than direct p99. It remains evidence for that one run, not a claim that Slizen is universally faster. v0.2.1 publishes a new, image-bound evidence bundle rather than reusing that result as its own.

## Known limitations

- Slizen remains single-node, is not a durable database, and is not a source of truth.
- Direct origin writes can remain stale until local TTL expiration. Server-assisted invalidation remains the v0.3 safety milestone.
- Redis compatibility is intentionally limited, and the admin API has no built-in authentication.
- redcon reads one complete RESP command before Slizen applies byte and argument limits. These settings bound dispatch and upstream work, not parser allocation; upstream response sizes are not bounded by them.
- Long-running soak, 100,000-key churn, and workload-specific capacity validation remain required before serious deployment.
