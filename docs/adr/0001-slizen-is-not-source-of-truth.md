# ADR 0001: Slizen is not a source of truth

## Status

Accepted

## Context

Slizen is designed to protect Redis and Valkey from read-hot keys by promoting frequently read values into a bounded local cache. The long-term product direction includes a cache mesh, but v0.1 is intentionally single-node and non-durable.

## Decision

Slizen will not act as a durable database, transactional store, consensus system, Redis Cluster replacement, or source of truth. Redis or Valkey remains authoritative. Slizen cache entries and hotness state are disposable.

## Consequences

- Restarting Slizen may drop cached values and hotness state.
- Writes that pass through Slizen invalidate local cache entries after upstream acceptance.
- Ambiguous upstream write errors also invalidate affected disposable cache entries conservatively.
- Fixed-size cache epochs prevent overlapping read misses from restoring values superseded by proxied writes.
- Direct upstream writes may remain stale in Slizen until local TTL expiration.
- Future invalidation for direct upstream writes belongs in the roadmap through Redis/Valkey server-assisted client tracking.
