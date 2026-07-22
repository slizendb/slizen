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
Slizen does not perform negative caching in v0.2.1.
