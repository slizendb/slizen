# ADR 0003: Key Identity Privacy

## Status

Accepted.

## Context

Hot-key telemetry is useful only if operators can correlate repeated observations. At the same time, Redis keys may contain customer identifiers, emails, tokens, or business-sensitive names.

A plain unsalted hash is not enough because key spaces are often guessable. A stable keyed identifier is safer for exported admin output.

## Decision

Slizen supports:

- `privacy.key_visibility = "hash"`: expose HMAC-SHA256-based key identifiers.
- `privacy.key_visibility = "plain"`: expose raw keys for private local debugging.

The HMAC secret is configured by `privacy.key_hash_secret` or `SLIZEN_KEY_HASH_SECRET`.

`/v1/status` exposes the visibility mode but never the secret.

Promotion and demotion logs always use HMAC identifiers, even when admin hot-key output is configured as `plain`.

## Consequences

The default admin output is stable across requests but does not directly reveal raw key names.

Operators must set a non-default secret before sharing telemetry outside a trusted local environment.
