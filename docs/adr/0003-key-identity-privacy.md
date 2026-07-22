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

The HMAC secret can be configured by `privacy.key_hash_secret` or `SLIZEN_KEY_HASH_SECRET`. When both are omitted, Slizen generates a cryptographically random process-local secret. It is never logged; anonymized identifiers intentionally change after restart.

`/v1/status` exposes the visibility mode but never the secret.

Promotion and demotion logs always use HMAC identifiers, even when admin hot-key output is configured as `plain`.

## Consequences

The default admin output is stable within one process lifetime but does not directly reveal raw key names.

Operators need a high-entropy stable secret from their secret manager only when identifiers must be compared across restarts. Telemetry must still be handled as potentially sensitive because HMAC identifiers can reveal repeated access patterns.
