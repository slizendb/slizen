# Product Notes

Slizen is a self-hosted adaptive cache layer for read-heavy Redis and Valkey workloads.

The first valuable user workflow is not a distributed mesh. It is this:

1. Put Slizen in `observe` mode in front of a staging Redis/Valkey endpoint.
2. Show which keys create read skew.
3. Switch to `cache` mode for a controlled workload.
4. Measure whether origin QPS drops without unacceptable p99 latency cost.

## Product Boundaries

- Redis or Valkey remains the source of truth.
- Slizen is disposable cache and telemetry state.
- v0.2 is single-node.
- Direct upstream writes may remain stale in Slizen until local TTL expiration.
- Slizen intentionally supports only a small command set.
- Hot-key telemetry should use HMAC-based key identifiers by default.

## Release Position

Slizen v0.2 should be described as a developer preview suitable for local demos and careful observe-first staging experiments, not as a production-ready database component.

## Product Sequence

The near-term product is a safe measurement and caching data plane, not a distributed mesh or enterprise management suite:

1. v0.2: per-prefix safety, observe-mode audit, reproducible workload evidence, and Kubernetes sidecar packaging.
2. Validate the workflow with design partners after implementation and in parallel with release-candidate adoption.
3. v0.3: Redis/Valkey-assisted invalidation with fail-safe behavior.
4. Sell a bounded performance assessment with an agreed before/after result.
5. Build fleet management only after multiple users independently ask for centralized policy rollout, history, alerts, or version control.

## Commercial Direction

The intended model is an open-source data plane that runs inside the customer's VPC or Kubernetes cluster. A future paid control plane may receive bounded metadata, fleet state, and policies, but not Redis values or credentials. Enterprise packaging may later add an on-prem control plane, SSO/RBAC, SLA, and support.

This is a direction, not a commitment to build the control plane now. A Kubernetes Operator, mesh, billing, and enterprise identity features remain deferred until real usage creates repeated operational demand.
