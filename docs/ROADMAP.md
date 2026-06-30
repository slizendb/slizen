# Roadmap

Slizen replicates hot objects before they burn your database. Redis or Valkey remains the authoritative source of truth at every phase.

## v0.1: Single-node adaptive read proxy

- Explicit `cache` and `observe` operating modes.
- Redis-compatible RESP proxy for selected read and write commands.
- Bounded local RAM cache with per-entry expiration and LRU-style eviction.
- Hot-key detection with promotion hysteresis and cooling.
- Request coalescing for cache misses.
- Write-driven local invalidation when writes pass through Slizen.
- Prometheus metrics, administration API, CLI, and Docker Compose demo.

## v0.2: Static multi-node cache mesh

- Privacy-aware key identity modes.
- Per-prefix cache policy.
- Reproducible benchmark harness with skewed workloads.
- Multiple Slizen nodes.
- Static membership.
- Node heartbeats.
- Exchange of top-K hot-key metadata.
- Local sidecar deployment.
- Basic replica placement.

## v0.3: Adaptive mesh placement

- SWIM-style membership or a proven membership library.
- Adaptive hot-read replicas between Slizen nodes.
- Failure detection.
- Load-aware placement.
- Replacement of lost ephemeral replicas.
- Topology-aware routing.

## v0.4: Production fleet integrations

- Redis/Valkey server-assisted client tracking.
- Invalidation for direct upstream writes.
- Kubernetes operator.
- Hosted control plane.
- Fleet-wide dashboard.
- Slack and PagerDuty alerts.
- Capacity recommendations.
- Cost and upstream-load reports.

Gossip and membership do not provide write consensus. Slizen remains a cache layer, not a database or source of truth.
