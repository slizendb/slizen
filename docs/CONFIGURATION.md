# Configuration safety and resource bounds

Slizen starts in `observe` mode. In this mode every supported request is sent
to Redis or Valkey and Slizen collects bounded telemetry, but it never stores
or serves a local value. Configuration files are applied over built-in defaults
and non-empty `SLIZEN_*` environment variables are applied last.

## Selective cache promotion

A key with no matching policy inherits the global mode. Therefore every
selective promotion must combine global `cache` mode with an empty-prefix
`observe` catch-all. Longer literal prefixes win:

```toml
mode = "cache"

[[cache.policies]]
prefix = ""
mode = "observe"

[[cache.policies]]
prefix = "catalog:public:"
mode = "cache"
max_item_bytes = 262144
max_local_ttl = "5s"
```

Without the first rule, every unmatched key is eligible for adaptive caching
under the global limits. Keep writes flowing through Slizen where possible;
direct origin writes may remain stale until the local TTL expires.

In `cache` mode, v0.2.3 partitions the existing `cache.max_bytes` and non-zero
`cache.max_entries` limits into seven eighths for protected admitted values and
one eighth for probationary candidates. There is no extra allocation or new
configuration switch. An eligible first miss can retain a candidate for at most
the normal local TTL and `hotness.window`; one later read can promote it, carrying
forward the remaining TTL instead of restarting expiration. Limits too small to
split retain protected-only behavior.

A successful exact `SET key value` without options can refresh an already
admitted key after Redis or Valkey accepts it. The key must still match a
`cache` policy; cold keys are not admitted by writes. Option-bearing `SET`, all
other mutations, nil replies, and errors remain conservatively invalidating.
Direct origin writes bypass this behavior and can leave either tier stale until
local TTL expiration.

At `hotness.max_tracked_keys`, an unseen observation performs one O(1) FIFO
victim check. If that current victim is `HOT`, Slizen keeps it, advances the
cursor, and drops the unseen observation instead of scanning. The audit field
`capacity_observations_dropped` and Prometheus metric
`slizen_hotness_capacity_observations_dropped_total` expose these events, and
any drop makes `telemetry_complete=false`. This bounds request work and protects
the current HOT victim; it is not unlimited scan resistance.

## RESP request and connection bounds

The proxy applies these defaults:

| TOML field | Environment variable | Default | Hard configuration maximum |
| --- | --- | ---: | ---: |
| `proxy.max_command_bytes` | `SLIZEN_PROXY_MAX_COMMAND_BYTES` | 1,048,576 | 16,777,216 |
| `proxy.max_command_args` | `SLIZEN_PROXY_MAX_COMMAND_ARGS` | 1,024 | 4,096 |
| `proxy.max_mget_keys` | `SLIZEN_PROXY_MAX_MGET_KEYS` | 512 | 2,048 |
| `proxy.max_connections` | `SLIZEN_PROXY_MAX_CONNECTIONS` | 1,024 | 10,000 |

An over-limit parsed command receives an error and its connection is closed so
the enlarged per-connection read buffer is released. The connection limit is
checked before redcon starts reading commands from an accepted connection.
`max_mget_keys` must be lower than `max_command_args`.

These are request-admission limits, not a strict process-memory ceiling.
redcon v1.6.2 assembles one complete RESP command before invoking Slizen's
handler and does not expose a parser byte/argument limit. Consequently the byte
and argument checks prevent conversion, dispatch, and upstream work but occur
after that one command has been read, and they do not aggregate in-flight bytes
across connections. Enforcing the same limits before parser allocation or as a
global byte budget requires a bounded parser in redcon or replacing/forking
that dependency. Upstream GET and MGET replies are fully materialized before
Slizen can forward or cache them. They do not have a separate heap-byte cap and
are not bounded by these request settings. Keep the Pod/container memory limit
in place and use trusted cluster-internal network access for the developer
preview.

The read and write timeouts bound clients that stop sending or receiving data;
malformed RESP is rejected by redcon and the connection is closed.

## Privacy-safe key identifiers

With `privacy.key_visibility = "hash"`, omitting `privacy.key_hash_secret` and
`SLIZEN_KEY_HASH_SECRET` makes Slizen generate a cryptographically random
process-local HMAC secret. It is never logged. This is the safest zero-config
behavior, but anonymized key IDs change at restart.

Configure a high-entropy stable value through a secret manager only when IDs
must be compared across restarts:

```sh
export SLIZEN_KEY_HASH_SECRET='value-loaded-by-your-secret-manager'
```

`cache.negative_ttl` is reserved for a later release and must remain `0s`.
Slizen does not perform negative caching in v0.2.3.
