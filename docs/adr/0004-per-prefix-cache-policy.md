# ADR 0004: Per-prefix cache policy

## Status

Accepted.

## Context

A single process-wide cache mode is too coarse for Redis and Valkey deployments that mix public catalog data, high-churn sessions, sensitive keys, and telemetry-only namespaces. Operators need static, bounded rules that decide which keys may participate in adaptive caching without turning Slizen into an authorization layer.

## Decision

Slizen supports ordered-independent `[[cache.policies]]` rules. Each rule matches a literal, case-sensitive Redis key byte prefix. The longest matching prefix wins; an empty prefix is an explicit catch-all. Exact duplicate prefixes are invalid. A key with no matching rule inherits the process-wide mode and global cache limits.

Runtime matching is bounded by configuration limits: at most 1,024 policies, at most 1,024 bytes per prefix, and at most 262,144 prefix bytes in total. Validation errors identify policy indices but never include prefix contents.

Each rule has one mode:

- `deny`: forward reads and writes to upstream, but do not record hotness, coalesce misses, read or write local cache entries, or serve stale data. This denies cache participation, not Redis access.
- `observe`: forward every read and record bounded hotness telemetry, but do not coalesce misses, read or write local cache entries, or serve stale data.
- `cache`: use adaptive hotness, local cache reads and writes, request coalescing, and the existing stale-on-error opt-in.

The global `mode = "observe"` remains a safety ceiling. It downgrades matching `cache` rules to `observe`; matching `deny` rules remain `deny`. This lets an operator disable every local cache path with `SLIZEN_MODE=observe` without maintaining a second policy file.

`cache` rules require positive `max_item_bytes` and `max_local_ttl` values that do not exceed their global cache ceilings. `max_item_bytes` uses Slizen's documented estimated entry footprint: key bytes, value bytes, and fixed entry overhead. `max_local_ttl` caps fresh local residency; the effective TTL is the minimum of a positive upstream PTTL, the rule limit, and the global limit. A key without upstream expiry uses the rule limit. A zero upstream PTTL is not cached.

An oversized value is still returned to the client but is not stored locally. A successful response that is now oversized or has no positive local TTL deletes any older local entry so stale-on-error cannot later expose the superseded value. The global `stale_grace` remains an explicit outage option and may extend serving beyond the fresh per-prefix TTL.

Policies are immutable after startup in this version. `MGET` resolves a policy independently for every input key, preserves result order, and retains one upstream batch for all local misses and bypassed keys. Writes, cache epochs, and invalidation remain policy-independent and conservative.

Policy prefixes may reveal key-space structure. Slizen does not include them in logs, status output, errors, or Prometheus labels. Startup summaries expose only the number of configured policies.

## Consequences

- An empty policy list preserves v0.1 behavior.
- Overlapping namespaces are expressible without depending on file order.
- A cache policy cannot grant access or prevent an upstream mutation.
- Per-prefix item limits cap individual entries, not aggregate namespace memory; all entries still share the bounded global LRU cache.
- Runtime policy reload is out of scope. A future reload must define cache purge and in-flight request generation semantics before replacing the immutable matcher.
