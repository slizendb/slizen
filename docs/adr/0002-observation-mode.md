# ADR 0002: Observation Mode

## Status

Accepted.

## Context

Before Slizen is allowed to serve local cached values, operators need a low-risk way to place it in front of a staging Redis/Valkey endpoint and measure hot-key skew.

Setting cache size to zero is ambiguous because cache validation and cache metrics still look like cache behavior. A named operating mode is clearer.

## Decision

Slizen supports:

- `mode = "cache"`: normal adaptive local caching behavior.
- `mode = "observe"`: forward commands to upstream, record bounded hotness and metrics, but never serve local cache hits, never coalesce `GET` requests, and never store values in the local cache.

The mode can be configured in TOML or with `SLIZEN_MODE`.

`/v1/status` exposes the active mode.

## Consequences

`observe` mode is the safest first staging mode and should be used to validate key skew before enabling cache behavior.

`observe` mode does not reduce upstream read load.
