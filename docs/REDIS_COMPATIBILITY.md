# Redis Compatibility

Slizen v0.1 is a Redis-compatible read proxy for a small command subset. Redis or Valkey remains the source of truth.

| Command | Status | Behavior | Tested |
| --- | --- | --- | --- |
| `PING` | supported | Handled by Slizen and returns `PONG` or the provided payload. | unit + integration |
| `GET` | supported | Cache-aware read in `cache` mode; upstream read only in `observe` mode. | unit + integration |
| `MGET` | supported | Ordered multi-key read with local hits where available in `cache` mode. | unit + integration |
| `SET` | supported | Forwarded to upstream, then affected local cache entry is invalidated. `SET GET` is rejected. | unit + integration |
| `SETEX` | supported | Forwarded to upstream, then affected local cache entry is invalidated. | unit |
| `PSETEX` | supported | Forwarded to upstream, then affected local cache entry is invalidated. | unit |
| `DEL` | supported | Forwarded to upstream, then affected local cache entries are invalidated. | unit + integration |
| `UNLINK` | supported | Forwarded to upstream, then affected local cache entries are invalidated. | unit |
| `EXPIRE` | supported | Forwarded to upstream, then affected local cache entry is invalidated. | integration |
| `PEXPIRE` | supported | Forwarded to upstream, then affected local cache entry is invalidated. | integration |
| `PERSIST` | supported | Forwarded to upstream, then affected local cache entry is invalidated. | unit |
| `TTL` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `PTTL` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `EXISTS` | pass-through | Forwarded to upstream in v0.1. | unit |
| `MULTI` | rejected | Rejected with a RESP error because transactions are stateful and unsafe for v0.1. | integration |
| `EXEC` | rejected | Rejected with a RESP error because transactions are stateful and unsafe for v0.1. | integration |
| `WATCH` | rejected | Rejected with a RESP error because watched transactions are not supported. | integration |
| `SUBSCRIBE` | rejected | Rejected with a RESP error because Pub/Sub is not supported. | integration |
| `PSUBSCRIBE` | rejected | Rejected with a RESP error because Pub/Sub is not supported. | unit |
| `MONITOR` | rejected | Rejected with a RESP error because monitoring mode is connection-stateful. | integration |
| `SELECT` | supported | `SELECT 0` is accepted as a no-op; other DBs are rejected. | unit |
| `BLPOP` | rejected | Rejected with a RESP error because blocking commands are not supported. | integration |

Any command not listed here should be treated as not supported in v0.1.
