# Product Validation Plan

Slizen should earn a control plane by proving that its data plane solves a costly problem. Product validation runs alongside engineering and does not change the safety requirements for a release.

## v0.2 discovery targets

- 20 to 30 conversations with engineers who operate meaningful Redis or Valkey workloads.
- 5 Slizen demonstrations.
- 3 design partners willing to review the workflow and deployment contract.
- 2 staging installations in `observe` mode.
- 1 anonymized real-workload audit or benchmark report.

A qualified conversation involves an engineer who has seen hot-key skew or a related incident, can describe relevant origin load or latency, and can realistically place a proxy in staging. A positive comment or repository star is not validation.

## First paid offer

The first offer is a bounded Redis/Valkey hot-key performance assessment, not a generic subscription:

1. Run Slizen in `observe` mode and collect a privacy-safe audit.
2. Agree on allowed prefixes and a staleness budget.
3. Enable caching only in staging for those prefixes.
4. Compare origin GET QPS, p95/p99 latency, cache hit rate, Slizen memory, invalidation behavior, and error rate.
5. Deliver a before/after report with limitations and rollback notes.

A candidate success criterion is at least 30% lower origin GET QPS on a suitable workload without a worse error rate and within an agreed staleness budget. This is a validation hypothesis, not a universal Slizen performance claim.

## Continue signals

- Three teams install `observe` mode.
- Two keep Slizen running beyond one test session.
- One is willing to pay for a bounded assessment or pilot.
- Suitable workloads show material origin GET reduction.
- Users ask for safer policies, invalidation, or a fleet view.
- Rollback takes minutes rather than hours.

## Reposition signals

- After 25 to 30 qualified conversations, nobody will provide a staging workload.
- Built-in client-side caching already solves the problem adequately.
- Proxy deployment risk consistently exceeds the hot-key pain.
- Savings are smaller than the cost of operating Slizen.
- Users value only hot-key observability and do not want adaptive caching.

If the last signal repeats, Slizen should consider an observability/audit position instead of forcing a cache-mesh roadmap.

## Control-plane gate

Do not build a hosted control plane because it is a familiar SaaS shape. Build it when multiple active users independently need several of these capabilities:

- fleet health;
- centralized policy rollout and audit history;
- version-skew and canary management;
- alerts and unhealthy-sidecar detection;
- stored before/after evidence and recommendations.

Until then, configuration files, Prometheus, Grafana, a sidecar example, and Helm are sufficient.
