# Threat Model

This document covers Slizen v0.2 as a single-node Redis/Valkey proxy.

## Assets

- upstream Redis/Valkey credentials;
- cached values in process memory;
- Redis key names;
- admin API output;
- Prometheus metrics;
- service availability.

## Trust Boundaries

- The RESP proxy may be reachable by application clients.
- The admin API is unauthenticated and must bind to a private interface or localhost.
- Downstream and upstream RESP are plaintext in v0.2; upstream credentials do
  not provide transport encryption.
- Redis/Valkey remains the authoritative data store.

## Risks And Mitigations

| Risk | Mitigation |
| --- | --- |
| Raw values leak through logs or admin output | Do not log values; admin cache endpoints expose aggregate metadata only. |
| Key names leak through telemetry | Hot-key output uses HMAC-SHA256 identifiers by default; keys are never Prometheus labels. |
| Metrics cardinality explosion | Metrics labels are normalized to bounded command/result/reason sets. |
| Unbounded request bodies | Admin purge uses a bounded body reader. |
| Cache memory growth | Cache bytes and entry count are bounded. |
| Hotness memory growth | Tracked keys are bounded. |
| Direct upstream writes cause stale reads | Documented limitation; future work covers server-assisted invalidation. |
| Unauthenticated downstream RESP permits arbitrary supported reads/writes | Bind to loopback or enforce an exact application-peer allow-list; the Helm chart defaults to denied ingress and requires a positive/negative enforcement test. |
| Plaintext upstream credentials or values are observed in transit | Keep the origin path private or use a separately reviewed external tunnel; a TLS-required origin is incompatible with v0.2 by itself. |
| Cluster redirection or Sentinel failover changes the authoritative endpoint | v0.2 accepts one standalone address and does not follow/discover topology; reject these deployments unless a separately managed standalone-compatible endpoint owns that behavior. |
| Admin API exposed publicly | Default bind is localhost; when network-bound, restrict the complete shared admin/metrics listener to exact monitoring peers. |

## Out Of Scope In v0.2

- TLS/mTLS termination;
- admin authentication;
- Redis ACL proxying beyond upstream client credentials;
- Redis Cluster redirection and Sentinel discovery/failover;
- durable storage;
- multi-node trust and peer authentication.
