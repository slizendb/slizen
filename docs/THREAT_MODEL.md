# Threat Model

This document covers Slizen v0.1 as a single-node Redis/Valkey proxy.

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
| Admin API exposed publicly | Default bind is localhost; docs warn against public exposure. |

## Out Of Scope In v0.1

- TLS/mTLS termination;
- admin authentication;
- Redis ACL proxying beyond upstream client credentials;
- durable storage;
- multi-node trust and peer authentication.
