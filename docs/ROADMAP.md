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

Status: released developer preview (`v0.2.0`) on 2026-07-18. The completed public release evidence is recorded in [PUBLIC_RELEASE_CHECKLIST.md](PUBLIC_RELEASE_CHECKLIST.md).

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

## v0.2.1: Launch hardening

Status: released developer preview (`v0.2.1`) on 2026-07-22. The tag resolves to commit `4ba2c1c5c9a1c89073ba47d90c83f98441dfe9a1`; the verified multi-architecture image index is `sha256:4006733aa64b6f55f25855f48a026d7b488ed44b5ad82d1a52ef5968d08daece`. Checksummed evidence is attached to the [GitHub Release](https://github.com/slizendb/slizen/releases/tag/v0.2.1).

Implemented and released:

- [x] Observe-first defaults and an explicit empty-prefix safety catch-all for selective cache promotion.
- [x] Random process-local HMAC secret when an operator does not configure a stable one.
- [x] Configurable, hard-capped RESP request and connection admission limits with the parser-level limitation documented.
- [x] Stale-grace retention that is not destroyed by cache metrics or inspection.
- [x] O(1) hot-key summary counters on the request/status path.
- [x] Key-specific workload value verification with mismatch-invalidated evidence.
- [x] Canonical licensing, pinned release inputs and Actions, dependency update automation, GitHub-native OCI provenance, and evidence generated from the exact image digest.
- [x] GHCR-first install docs, honest v0.2.0 evidence language, and design-partner intake.

Release closure:

- [x] Run the complete release gate from the intended clean commit.
- [x] Confirm public CI and immutable-image evidence are green.
- [x] Publish and verify the `v0.2.1` tag, GitHub Release, GHCR digest, provenance, and attached evidence.
- [x] Enable the repository security and branch rules listed in [PUBLIC_RELEASE_CHECKLIST.md](PUBLIC_RELEASE_CHECKLIST.md).

Release gate: safe defaults and bounded failure behavior survive unit, race, integration, Docker, Kubernetes, and workload validation, and every public evidence artifact resolves to one full commit and immutable image digest.

## v0.2.2: Proxy tax reduction and benchmark attribution

Status: implemented and locally validated on 2026-07-22; unreleased.

- [x] Pipeline upstream `GET` and `PTTL` into one round trip without weakening error semantics.
- [x] Remove miss-only timeout allocation and redundant tracker/cache-stat locking from verified local GET hits.
- [x] Add a dedicated GET dispatch fast path and pre-bind fixed Prometheus metric children.
- [x] Keep proxy drain socket deadline calls outside the global drain mutex without losing shutdown accounting.
- [x] Remove steady-state drain-admission mutex contention while preserving bounded shutdown accounting.
- [x] Remove redundant final-window boundary delay once a key's current count guarantees promotion without weakening EWMA or consecutive-window hysteresis.
- [x] Correct the cache-hit microbenchmark, add a concurrent dispatch benchmark, and record a repeated before/after allocation baseline.
- [x] Attribute workload read, write, and final-validation latency separately while preserving the backward-compatible aggregate distribution.
- [x] Pass the complete Go, Docker, Kubernetes, and request-bound workload release gate from the intended clean commit before tagging.

Release gate: the complete v0.2.1 safety contract remains green, the fixed-size workload evidence proves its sample accounting, and proxy-overhead claims are backed by repeated benchmarks with explicit scope.

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
