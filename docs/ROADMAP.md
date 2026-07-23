# Roadmap

Slizen replicates hot objects before they burn your database. Redis or Valkey remains the authoritative source of truth at every phase.

Near-term execution status and acceptance checklists are tracked in [FIRST_ISSUES.md](../FIRST_ISSUES.md).

## Product readiness axis

Infrastructure detail is not the primary readiness measure. Every release is
also judged by this question:

> Can a team unfamiliar with Slizen reach ready `observe` in no more than
> 30 minutes, measure it, and later restore the direct endpoint in less than
> five minutes without Slizen maintainers operating the trial?

The executable [staging runbook](STAGING_ROLLOUT.md), [failure-mode
contract](FAILURE_MODES.md), and [pass/partial/fail
gate](STAGING_RELEASE_GATE.md) define that evidence. A green engineering release
check is necessary but does not by itself prove self-service staging adoption.

The stable public install target remains v0.2.2 at image index
`sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627`.
v0.2.3-rc.1 is published for external staging trials at image index
`sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a`.
The stable public install remains v0.2.2 until the self-service staging gate
passes.

Staging-adoption closure:

- [x] Document measurable, pre-agreed go/no-go thresholds and representative
  observe, one-prefix, canary, and expansion soak windows.
- [x] Document standalone Helm and sidecar upgrade/rollback commands, immutable
  identity capture, connection disruption, and endpoint-first recovery.
- [x] Publish one operator matrix for crash/OOM, origin outage and recovery,
  ambiguous writes, TTL, races, memory/request bounds, and `SIGTERM`.
- [ ] Have an operator who did not develop Slizen reach ready `observe` mode
  from a clean namespace in no more than 30 minutes without maintainer help.
- [ ] Record a direct-endpoint rollback rehearsal completed in less than five
  minutes from routed canary traffic.
- [ ] Execute the documented failure drills and close every safety-critical
  pass/partial/fail item.
- [ ] Complete a design-partner staging soak with agreed application latency,
  error, origin-load, memory, correctness, and compatibility budgets.

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
- [x] Documented operator-visible failure modes and a self-service staging gate.

Engineering release gate: `observe` stores and serves no local values, and the
repository provides an audit report plus reproducible workload evidence. The
separate staging-adoption gate above remains open until an unfamiliar operator
completes the procedure and rollback rehearsal.

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

Status: released developer preview (`v0.2.2`) on 2026-07-22. The tag resolves to commit `74a12767deb72db9bc78bebd807cbe8717fa572c`; the verified multi-architecture image index is `sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627`. The [release image workflow](https://github.com/slizendb/slizen/actions/runs/29953669287) is green, and the checksummed image-bound bundle plus separate 100,000-key evidence are attached to the [GitHub Release](https://github.com/slizendb/slizen/releases/tag/v0.2.2).

- [x] Pipeline upstream `GET` and `PTTL` into one round trip without weakening error semantics.
- [x] Remove miss-only timeout allocation and redundant tracker/cache-stat locking from verified local GET hits.
- [x] Add a dedicated GET dispatch fast path and pre-bind fixed Prometheus metric children.
- [x] Keep proxy drain socket deadline calls outside the global drain mutex without losing shutdown accounting.
- [x] Remove steady-state drain-admission mutex contention while preserving bounded shutdown accounting.
- [x] Remove redundant final-window boundary delay once a key's current count guarantees promotion without weakening EWMA or consecutive-window hysteresis.
- [x] Correct the cache-hit microbenchmark, add a concurrent dispatch benchmark, and record a repeated before/after allocation baseline.
- [x] Attribute workload read, write, and final-validation latency separately while preserving the backward-compatible aggregate distribution.
- [x] Pass the complete Go, Docker, Kubernetes, and request-bound workload release gate from the intended clean commit before tagging.
- [x] Publish and verify the `v0.2.2` tag, GitHub Release, GHCR digest, provenance, and attached immutable-image evidence.

Release gate: the complete v0.2.1 safety contract remains green, the fixed-size workload evidence proves its sample accounting, and proxy-overhead claims are backed by repeated benchmarks with explicit scope.

## v0.2.3: Bounded two-hit admission

Status: v0.2.3-rc.1 published as a staging prerelease on 2026-07-23. The tag, exact image digest, checksummed release-bound deployment artifacts, provenance, and physical-origin workload evidence are public; the external self-service staging gate remains open.

Implementation:

- [x] Partition the existing cache limits into a seven-eighths protected tier and one-eighth probationary tier without increasing global byte or entry budgets.
- [x] Retain the first eligible successful miss as a short-lived candidate and let one later read promote it while preserving its original absolute local expiry.
- [x] Keep coalesced waiters from turning one cold miss into artificial multi-hit admission.
- [x] Refresh an already admitted cache-policy key after a successful exact option-free `SET`, while preserving conservative invalidation for every other mutation or ambiguous outcome.
- [x] Invalidate protected and probationary state before proxied mutation dispatch and retain a final epoch barrier against overlapping stale refills.
- [x] Attribute misses with fixed bounded `policy_bypass`, `not_admitted`, and `not_present` counters in status and workload evidence.
- [x] Protect the current HOT FIFO tracker victim with O(1) capacity-drop behavior and expose incomplete telemetry through `capacity_observations_dropped` and `slizen_hotness_capacity_observations_dropped_total`.
- [x] Record five unchanged cold request-bound 99/1 local Docker repeats with 798–803 Slizen logical upstream GET calls versus 94,961 direct successful GETs, 99.154390–99.159655% proxy-side avoidance, zero failures or mismatches, and no speed or physical-command claim.
- [x] Replace historical proxy-side estimates with release evidence backed by same-`run_id`, monotonic Redis/Valkey `INFO commandstats` deltas from the exact published image.
- [x] Add an offline version-and-commit-bound compatibility report with a non-zero CI gate for explicitly supplied rejected or unsupported commands.
- [x] Require explicit acknowledgement for command names whose supported argument shapes are narrower than Redis.
- [x] Ship an import-ready Grafana dashboard, conservative Prometheus staging alerts, active-connection and cache-capacity gauges, and bounded runtime/process metrics.
- [x] Default the Helm chart to denied ingress until exact RESP and monitoring peers are declared.
- [x] Define failure behavior, measured endpoint-first rollback, and a pass/partial/fail self-service staging gate; keep runnable examples and every rendered runtime identity pinned to the last published digest until v0.2.3 exists.

Release closure:

- [x] Pass the full clean-commit Go, race, Docker, Kubernetes, and four-scenario request-bound release gate.
- [x] Publish and verify the `v0.2.3-rc.1` tag, GHCR digest, provenance, and exact-image evidence bundle.
- [x] Replace pre-publication wording after those immutable artifacts exist.

Release gate: the v0.2.2 safety and attribution contract remains green; cache tier totals stay within the configured global budgets; stale-refill and write races remain deterministic; and published performance statements distinguish origin-load reduction from end-to-end speed.

Staging gate: do not promote this prerelease merely because its synthetic origin
reduction improved. It now has a real published digest, but must still pass the
self-service staging and rollback evidence before stable v0.2.3.

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
