# Self-service staging release gate

Slizen is evaluated on one product question:

> Can a team unfamiliar with Slizen reach ready `observe` in no more than
> 30 minutes, measure it, and later restore the direct endpoint in less than
> five minutes without Slizen maintainers operating the trial?

This gate is separate from unit, race, packaging, and synthetic workload tests.
Those checks are necessary, but they do not prove that another team will accept
a new component in its Redis data path.

The stable public image is v0.2.2 at commit
`74a12767deb72db9bc78bebd807cbe8717fa572c` and image index
`sha256:7989b6ff17659b3f1b2f1d3feec8af6422b48f1f5486eb77247a5c82ba86b627`.
The v0.2.3-rc.1 staging prerelease is published at commit
`7662a1fb5974a6fc369ca486d2a59c85f68cd3b7` and image index
`sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a`.
Its immutable identity is verified; self-service staging is not yet proven.

## Result definitions

- **Pass:** an operator who did not develop Slizen completes the criterion from
  public documentation, records the required evidence, and needs no Slizen
  maintainer intervention.
- **Partial:** the safety property holds, but the operator needs a documented
  workaround, manual interpretation, or maintainer clarification. Every partial
  result needs an owner and a due date; it is not silently rounded up to pass.
- **Fail:** a safety invariant is violated, required evidence is missing, the
  operator cannot recover within the agreed window, or the result depends on an
  undocumented maintainer action.

Any fail in compatibility, correctness, source-of-truth safety, image identity,
or rollback makes the overall gate **fail**. Partials may support a limited
design-partner trial only when the partner accepts them in writing; they do not
support a general staging-ready claim.

## Gate matrix

| Criterion | Pass | Partial | Fail |
| --- | --- | --- | --- |
| Install time | From a clean namespace, a new operator reaches ready `observe` mode in no more than 30 minutes using the public guide. | Installation completes safely but needs one documented platform-specific workaround. | It exceeds 30 minutes, needs a Slizen maintainer, or exposes the admin API/credentials unintentionally. |
| Immutable identity | Evidence separately identifies (1) the runtime release tag, stamped full commit, and deployed image digest, and (2) the chart/manifest/config source commit and version plus hashes of the exact environment values/rendered configuration. The mapping is reproducible even when a newer chart commit deliberately deploys an older stable runtime. | Both executable artifacts are pinned, but one non-executable evidence record is missing. | A floating image/chart reference, dirty or unpushed deployment source, unhashed environment values, identity mismatch within either artifact, or nonexistent claimed image is used. |
| Compatibility and upstream ACL | The exact application client library creates a pool, connects, reconnects, and passes its integration suite through a plaintext/no-downstream-`AUTH` Slizen profile; every required initialization command is compatible or demonstrably optional. Its maximum pipeline depth and representative worst-case response sizes remain inside pre-agreed Pod-memory headroom. The exact Slizen upstream identity completes its `go-redis/v9` connection/auth handshake and can run readiness `PING`, `GET`/`MGET`, required per-key `PTTL`, and every routed write/pass-through command. The origin is one standalone-compatible endpoint; any TLS or Cluster/Sentinel topology/failover is owned by a separately reviewed and tested private proxy/service because Slizen v0.2 implements neither. | Coverage is incomplete but the uncovered call path is excluded from the canary with an owner; connection initialization, origin topology, pipeline-memory scope, and ACL coverage themselves are complete. | Downstream `AUTH`/TLS, a required unsupported `HELLO`/`CLIENT` or other initialization command, non-zero DB, unsupported value command, unbounded request, unbounded or unprofiled pipeline response memory, rejected Slizen handshake/readiness operation, missing upstream application-command permission, unhandled TLS, or direct Redis Cluster/Sentinel topology/failover is present in routed traffic. |
| Observe invariant | Value-bearing routed reads and writes reach origin, cache-hit delta and retained cache entries stay zero, and audit output contains no raw values or credentials. Locally handled protocol commands (`PING`, `SELECT 0`, and `QUIT`) and Slizen readiness `PING`s are accounted for separately. | Traffic is safe, but bounded audit coverage is incomplete and the missing scope is explicitly excluded. | A local application value is served/stored in `observe`, sensitive data is exposed, or a value-bearing operation silently bypasses origin. |
| Correctness | Zero value mismatches and zero final-validation mismatches in the agreed application and workload checks. | Not applicable; correctness is binary for the checked scope. | Any mismatch, cross-key value, truncated value, or unexplained result occurs. |
| Failure behavior | In an isolated observe-only canary, the team executes the bounded graceful `SIGTERM`, approved abrupt crash, and origin-outage/recovery drills from the runbook; cleanup and observed behavior match [FAILURE_MODES.md](FAILURE_MODES.md). A sidecar uses a service-owned injector that isolates Slizen's origin path without indiscriminately cutting the whole Pod. | One non-safety drill is unavailable or not permitted (including no safe sidecar-specific origin injector), with owner/date; endpoint rollback still passes. | A drill targets shared traffic, lacks bounded cleanup/rollback, behavior contradicts the contract, a failure is silent, or an ambiguous write is treated as definitely failed. |
| Rollback | From a routed canary, the original endpoint is restored and direct application health is verified in less than five minutes; removal happens afterward. | The endpoint is safe within five minutes, but cleanup or evidence capture exceeds the window. | Slizen is removed before clients are redirected, rollback exceeds five minutes, or the original endpoint/revision was not recorded. |
| Network isolation | Before application traffic is routed, only the recorded application peers can reach the unauthenticated standalone RESP port, and only the recorded scraper/administrative peers can reach a network-bound admin port. A denied test Pod plus an allowed-peer success proves the cluster CNI actually enforces the policy. A sidecar RESP listener remains on loopback. | The platform uses an equivalent enforced control outside Kubernetes NetworkPolicy; its owner and paired positive/negative test are recorded. | A manifest exists but enforcement was not tested, an unapproved Pod can connect, the CNI does not enforce NetworkPolicy, a sidecar RESP listener is Pod-wide, or RESP/admin is broadly reachable. |
| Observability | The operator imports the version-appropriate dashboard/queries and alerts, restricts the shared metrics/admin endpoint, tests the alert route, and can distinguish cache hits, proxy-side logical upstream-call avoidance, actual origin command volume from Redis/Valkey `commandstats` or an origin exporter, proxy latency, upstream errors, memory, and readiness continuously for the soak. A sidecar has an approved continuous same-Pod collector/proxy or a deliberately network-bound and restricted scrape path. | Safe manual queries exist, but one dashboard or alert integration remains a named, dated follow-up; this can support `observe`, not a cache staging pass. | The trial cannot detect a no-go signal, a sidecar relies on temporary port-forwarding, the admin API is exposed broadly, or Slizen logical-call/cache-hit counters are presented as physical origin traffic. |
| Overhead | Application error delta, p95/p99 tax, CPU, memory, connections, and origin traffic are measured against pre-recorded budgets. Results may be unfavorable but are published honestly. | Measurements exist but one non-safety dimension lacks enough representative samples. | Budgets were invented after the run, samples cannot be attributed, or a speed/offload claim exceeds the evidence. |
| One-prefix cache canary | One explicitly selected prefix stays inside its staleness, error, latency, memory, and origin-side command-volume budgets for the pre-agreed soak; all unmatched keys remain `observe`. A sidecar records either one routed replica with no old/new cache overlap, an operationally read-only prefix, or accepted TTL-bounded cross-sidecar staleness for every write path. | Safety gates pass, but the prefix misses its origin-command-reduction target; return it to `observe` and record a no-benefit result. | An unmatched prefix caches, direct-write or cross-sidecar staleness was not accepted, a mutable multi-sidecar/overlapping-rollout prefix has no consistency decision, actual origin traffic is not attributable, or any correctness/safety gate fails. |
| Gradual expansion | Each traffic/prefix step has a separate observation window and recorded go/no-go decision; the single-node Helm proxy is never scaled behind one Service, and independent sidecars remain within their recorded consistency condition. | Expansion stops safely at a smaller scope because the business target is not met. | Scope is expanded without evidence, multiple standalone replicas are load-balanced, sidecar cache scope outgrows its consistency decision, or a no-go signal is ignored. |
| Operator handoff | The team can identify owner, dashboards/queries, alert routes, secrets, endpoint source, rollback revision, digest, and known limitations from one trial record. | One operational integration is manual but documented and owned. | Ownership, alerting, credentials, or rollback data depends on tribal knowledge. |

## Required trial record

A staging pass is evidence, not a verbal approval. Store these items in the
team's normal change record:

1. operator name, service owner, date, cluster/namespace, and workload scope;
2. original origin endpoint source and application revision;
3. runtime release tag, stamped full commit, exact deployed image digest, and
   the published evidence that maps them;
4. chart/manifest/config repository and pushed commit, chart/config version,
   and hashes of the exact environment values and rendered configuration;
5. the filled threshold worksheet and observe/cache soak windows;
6. compatibility inventory plus exact application client
   pool/connect/reconnect and integration-test result, including the
   plaintext/no-downstream-`AUTH` Slizen endpoint profile;
7. exact Slizen upstream identity/ACL preflight result, including handshake,
   readiness `PING`, `GET`/`MGET`, `PTTL`, routed command coverage, and
   standalone origin topology or separately owned proxy/failover evidence;
8. allowed RESP/admin peers, applied isolation mechanism, and positive plus
   denied-peer connectivity results proving CNI/policy enforcement;
9. before/after application errors and p95/p99, Redis/Valkey
   `cmdstat_get:calls` or equivalent origin-exporter rate, Slizen logical
   upstream calls/errors, cache counters, active connections, CPU/Pod memory,
   restarts, and readiness events; v0.2.2 uses platform/application telemetry
   for connections and resources;
10. deployed-version metric inventory; for v0.2.2, the configured cache limits
   recorded beside usage because max-limit gauges and miss reasons are absent;
11. value-validation result and anonymized audit completeness;
12. for multi-replica sidecars, the single-replica/read-only/accepted-TTL
    consistency decision and rolling-update behavior;
13. graceful/crash/origin-outage injection and cleanup timestamps, exact
    disposable targets, readiness/application/error evidence, and recovery
    results;
14. rollback rehearsal start/end timestamps and direct-origin health result;
15. every partial result, its accepted risk, owner, and due date.

Do not attach Redis values, passwords, complete sensitive keys, or Secret
contents.

## Current prerelease disposition

Until a fresh operator runs this gate, Slizen may have a green engineering
release check without having a self-service staging **pass**. v0.2.3-rc.1 now
passes the immutable-identity portion: its tag, commit, multi-architecture image
digest, release-bound chart/raw manifest, checksums, and provenance are public.
Use those prerelease assets for the external RC trial. A source checkout may
still package the older stable runtime; record runtime and deployment identities
separately.

The observability pack is intentionally mixed-version. Core request, upstream,
latency, cache usage/eviction, coalescing, invalidation, and oversized-key
signals exist in v0.2.2. Cache miss reasons, configured max gauges, and the
capacity-drop counter are v0.2.3-rc.1 additions. A v0.2.2 pass must use the
stable core signals plus an external record/comparison of configured limits;
missing candidate-only series never means zero pressure or complete telemetry.

The executable procedure is in [STAGING_ROLLOUT.md](STAGING_ROLLOUT.md).
