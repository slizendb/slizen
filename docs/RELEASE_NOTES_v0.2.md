# Slizen v0.2.0 — Safe staging and workload evidence

Slizen v0.2.0 is a single-node developer preview focused on letting a team evaluate hot-key pressure safely before enabling local caching.

## What is new

- Static, bounded per-prefix `deny`, `observe`, and `cache` policies.
- `/v1/audit` and `slizenctl audit` with the versioned `slizen.audit.v1` JSON schema.
- Stable recommendation reason codes without Redis values or policy prefixes; HMAC key identifiers remain the default.
- Uniform, 80/20-like, 99/1-like, and moving-flash workload scenarios with p50/p95/p99, cache hit ratio, origin GET reduction, and runtime versions.
- An observe-first Kubernetes sidecar example and Helm chart without an Operator.
- A staging guide with compatibility checks, explicit allowed prefixes, before/after evidence, rollback triggers, and a minutes-scale endpoint rollback.
- Bounded graceful proxy drain and stale-refill protection for writes that pass through Slizen.
- Atomic hotness audit snapshots, bounded top-K selection, explicit telemetry-completeness metadata, and prompt cache invalidation when tracking evicts a hot key.
- Normal-response write deadlines so non-reading clients cannot pin proxy response flushes indefinitely.

## Upgrade notes

- Existing configurations without `[[cache.policies]]` preserve the global mode behavior.
- A `cache` policy requires explicit `max_item_bytes` and `max_local_ttl` values that do not exceed global cache limits.
- `deny` is a cache/telemetry decision, not an ACL: Redis commands are still forwarded according to the compatibility contract.
- Keys matched by `deny` are deliberately absent from audit output because they bypass hotness tracking.
- The default Kubernetes examples use `observe` mode and keep the unauthenticated admin listener on loopback.
- `hotness.max_tracked_keys` is capped at 100,000. Keys longer than 1,024 bytes are forwarded but excluded from hotness tracking and local caching.

## Verify locally

```sh
make check
make validate-k8s
make demo-up
make smoke
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

## Known limitations

- Slizen remains single-node and is not a source of truth.
- Local cache and hotness state are disposable and may be lost on restart.
- `observe` mode forwards reads and collects telemetry but does not serve local values or reduce origin GET load.
- Direct origin writes may remain stale until local TTL expiration. Redis/Valkey-assisted invalidation is the v0.3 safety milestone.
- Redis command compatibility remains intentionally limited.
- The admin API has no built-in authentication.
- Helm deploys a standalone proxy; sidecar injection remains the application's deployment responsibility.
- No mesh, Operator, hosted control plane, SSO/RBAC, or billing is included.
- This is still a developer preview, not a production-readiness claim.

## Next milestone

v0.3 focuses on fail-safe Redis/Valkey-assisted invalidation for explicitly allowed prefixes. Mesh and fleet management remain demand-gated hypotheses.
