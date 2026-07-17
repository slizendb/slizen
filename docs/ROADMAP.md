# Roadmap

Slizen replicates hot objects before they burn your database. Redis or Valkey remains the authoritative source of truth at every phase.

Near-term execution status and acceptance checklists are tracked in [FIRST_ISSUES.md](../FIRST_ISSUES.md).

## v0.1: Single-node adaptive read proxy

Status: released developer preview (`v0.1.0`).

- Explicit `cache` and `observe` operating modes.
- Redis-compatible RESP proxy for selected read and write commands.
- Bounded local RAM cache with per-entry expiration and LRU-style eviction.
- Hot-key detection with promotion hysteresis and cooling.
- Request coalescing for cache misses.
- Write-driven local invalidation when writes pass through Slizen.
- Prometheus metrics, administration API, CLI, and Docker Compose demo.

## v0.2: Safe staging and workload evidence

Status: implementation complete (2026-07-17); `v0.2.0` release candidate. Public CI, image publication, tag, and release publication remain pending in [PUBLIC_RELEASE_CHECKLIST.md](PUBLIC_RELEASE_CHECKLIST.md).

Included reliability work: bounded graceful proxy handler and connection drain, including forced cutoff at `proxy.shutdown_timeout`.

- [x] Per-prefix cache policy.
- [x] Hot-key audit report with bounded, privacy-safe output.
- [x] Stable recommendation reasons for tracked `observe` and `cache` entries; keys matched by `deny` are deliberately excluded because `deny` bypasses hotness tracking.
- [x] Reproducible benchmark harness for uniform, skewed, and moving-hot-key workloads.
- [x] Kubernetes sidecar example with readiness, liveness, resources, and a safe `observe` default.
- [x] Helm chart without an Operator.
- [x] Documented shadow/observe rollout and rollback procedure.

Release gate: a team unfamiliar with Slizen can place it in front of a staging Redis or Valkey endpoint in `observe` mode, store no local values, and produce a useful audit report plus reproducible workload evidence.

Customer discovery runs in parallel with v0.2. Product validation targets are defined in [VALIDATION_PLAN.md](VALIDATION_PLAN.md); they are business evidence, not software release requirements.

## v0.3: Direct-origin invalidation safety

Status: planned.

- Redis/Valkey server-assisted client tracking for explicitly allowed prefixes.
- Invalidation after direct upstream `SET`, `DEL`, expiration, and other supported mutations.
- Purge and disable local caching when the invalidation connection is lost.
- Invalidation health and lag metrics.
- Deterministic stale-refill race and reconnect tests.
- Fail-safe defaults and an operator runbook.

Release gate: for configured prefixes, direct-origin writes invalidate local copies, and loss of the invalidation channel cannot silently leave caching enabled.

## Later: mesh and fleet management

Status: hypothesis; not committed to a version.

- Static membership and top-K metadata exchange may be explored only after the single-node invalidation contract is safe on real workloads.
- Adaptive placement, failure detection, and topology-aware routing follow only if multi-node demand is demonstrated.
- A Kubernetes Operator is justified only when several design partners operate enough Slizen instances that Helm-based rollout is a repeated problem.
- A hosted control plane follows repeated demand for fleet health, policy rollout, history, alerts, and before/after reports.
- Enterprise packaging may add an on-prem control plane, SSO/RBAC, SLA, and support after production references exist.

The intended commercial direction is an open-source data plane inside the customer's infrastructure plus an optional paid control plane. The control plane is not part of v0.2 or v0.3.

Gossip and membership do not provide write consensus. Slizen remains a cache layer, not a database or source of truth.
