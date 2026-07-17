# First GitHub Issues

Create these as separate issues rather than one mega-ticket.
Execution status is tracked here until the work is represented by GitHub issues.

## 1. Add per-prefix cache policy

Status: complete (2026-07-17).

Labels: `milestone/v0.2`, `correctness`, `configuration`

Acceptance:

- [x] support longest-prefix policy matching;
- [x] support `deny`, `observe`, and `cache` modes per prefix;
- [x] enforce per-prefix max item bytes and max TTL;
- [x] add an ADR before implementation.

## 2. Add fuzz tests for command handling

Status: complete (2026-07-17).

Labels: `security`, `testing`, `protocol`

Acceptance:

- [x] fuzz `ParseCommand` and response conversion helpers;
- [x] no panic;
- [x] seed corpus covers empty commands, mixed case, unsupported commands, huge arguments, and binary bulk data.

## 3. Expand invalidation coverage

Status: complete (2026-07-17).

Labels: `correctness`, `protocol`

Acceptance:

- [x] table-driven tests for supported write commands;
- [x] explicit rejection or support decision for `MSET`, `RENAME`, and common hash/list/set mutations;
- [x] documentation update for the command table.

## 4. Measure cache-hit allocations

Status: complete (2026-07-17).

Labels: `performance`, `benchmark`

Acceptance:

- [x] record benchmark output for cache hit, cache miss, hotness observe, and proxy GET integration;
- [x] do not optimize until numbers are recorded.

## 5. Add admin pprof behind an explicit flag

Status: planned.

Labels: `observability`, `security`

Acceptance:

- [ ] disabled by default;
- [ ] only on the private admin listener;
- [ ] documentation warning;
- [ ] no import-side registration on the default mux.

## 6. Add graceful connection drain accounting

Status: complete (2026-07-17).

Labels: `reliability`

Acceptance:

- [x] track active proxy handlers during shutdown;
- [x] wait with a bounded deadline;
- [x] do not block forever on slow or malicious clients.

## 7. Prevent stale refill across concurrent writes

Status: complete (2026-07-17).

Labels: `correctness`, `concurrency`, `cache`

Acceptance:

- [x] prevent in-flight `GET` and `MGET` misses from restoring values superseded by a proxied write;
- [x] conservatively invalidate affected cache entries when the upstream write outcome is ambiguous;
- [x] add deterministic concurrency and write-error tests;
- [x] document the consistency behavior.

## v0.2 release backlog

These completed issues replace the old static-mesh-first plan. Mesh, an Operator, SSO/RBAC, billing, and a hosted control plane are intentionally out of scope for v0.2.

## 8. Add a privacy-safe hot-key audit report

Status: complete (2026-07-17).

Labels: `milestone/v0.2`, `observability`, `product`

Acceptance:

- [x] produce a bounded machine-readable report from observe-mode telemetry;
- [x] include the measurement window, request rate, hotness state, and applicable `observe` or `cache` policy without exposing raw values or policy prefixes; document that `deny` bypasses tracking and is excluded from the audit;
- [x] include stable recommendation reason codes rather than unsupported performance promises;
- [x] expose the report through the admin API and `slizenctl`;
- [x] add deterministic service, handler, and CLI tests.

## 9. Add release-grade skewed workload scenarios

Status: complete (2026-07-17).

Labels: `milestone/v0.2`, `performance`, `benchmark`

Acceptance:

- [x] cover uniform, 80/20-like skew, 99/1-like skew, and a moving flash key;
- [x] configure concurrency, duration, value size, and read/write ratio;
- [x] emit JSON with p50/p95/p99, origin GET reduction, hit rate, and runtime versions;
- [x] keep results reproducible and avoid universal performance claims.

## 10. Add a safe Kubernetes sidecar example

Status: complete (2026-07-17).

Labels: `milestone/v0.2`, `kubernetes`, `operations`

Acceptance:

- [x] default to `observe` mode and a private admin listener;
- [x] include ConfigMap, readiness, liveness, resource requests/limits, and graceful termination;
- [x] document the Redis endpoint change and supported-command compatibility check;
- [x] document a rollback that restores the original Redis endpoint within minutes.

## 11. Add a Helm chart without an Operator

Status: complete (2026-07-17).

Labels: `milestone/v0.2`, `kubernetes`, `packaging`

Acceptance:

- [x] package a standalone single-node deployment aligned with the sidecar example's safety defaults;
- [x] expose policies and safety limits without exposing credentials in rendered defaults;
- [x] support optional Prometheus `ServiceMonitor` integration without requiring its CRD;
- [x] validate rendered manifests in CI.

## 12. Document the observe-to-cache staging workflow

Status: complete (2026-07-17).

Labels: `milestone/v0.2`, `documentation`, `operations`

Acceptance:

- [x] start with no local value storage;
- [x] define how to select explicitly allowed prefixes and staleness budgets;
- [x] define before/after metrics and rollback triggers;
- [x] avoid an absolute "zero application changes" or full drop-in compatibility claim.

## v0.3 release backlog

## 13. Add Redis/Valkey-assisted invalidation

Status: planned after v0.2 evidence.

Labels: `milestone/v0.3`, `correctness`, `redis`, `valkey`

Acceptance:

- [ ] track direct-origin changes for explicitly allowed prefixes;
- [ ] purge affected entries on invalidation;
- [ ] purge all entries and disable local caching when tracking health is unknown;
- [ ] expose invalidation connection health and reconnect counters without key labels;
- [ ] cover disconnect, reconnect, expiration, and stale-refill races with deterministic tests.
