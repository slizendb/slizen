# Redis Compatibility

Slizen v0.2 is a Redis-compatible read proxy for a small command subset. Redis or Valkey remains the source of truth.

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
| `MSET` | rejected | Explicitly rejected because multi-key string writes are outside the v0.2 invalidation contract. | unit |
| `RENAME` | rejected | Explicitly rejected until source and destination invalidation semantics are supported. | unit |
| `HSET` | rejected | Explicitly rejected; hash mutations are outside the v0.2 command set. | unit |
| `HDEL` | rejected | Explicitly rejected; hash mutations are outside the v0.2 command set. | unit |
| `LPUSH` | rejected | Explicitly rejected; list mutations are outside the v0.2 command set. | unit |
| `RPUSH` | rejected | Explicitly rejected; list mutations are outside the v0.2 command set. | unit |
| `LPOP` | rejected | Explicitly rejected; list mutations are outside the v0.2 command set. | unit |
| `RPOP` | rejected | Explicitly rejected; list mutations are outside the v0.2 command set. | unit |
| `SADD` | rejected | Explicitly rejected; set mutations are outside the v0.2 command set. | unit |
| `SREM` | rejected | Explicitly rejected; set mutations are outside the v0.2 command set. | unit |
| `TTL` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `PTTL` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `EXISTS` | pass-through | Forwarded to upstream in v0.2. | unit |
| `MULTI` | rejected | Rejected with a RESP error because transactions are stateful and unsafe for v0.2. | integration |
| `EXEC` | rejected | Rejected with a RESP error because transactions are stateful and unsafe for v0.2. | integration |
| `WATCH` | rejected | Rejected with a RESP error because watched transactions are not supported. | integration |
| `SUBSCRIBE` | rejected | Rejected with a RESP error because Pub/Sub is not supported. | integration |
| `PSUBSCRIBE` | rejected | Rejected with a RESP error because Pub/Sub is not supported. | unit |
| `MONITOR` | rejected | Rejected with a RESP error because monitoring mode is connection-stateful. | integration |
| `SELECT` | supported | `SELECT 0` is accepted as a no-op; other DBs are rejected. | unit |
| `BLPOP` | rejected | Rejected with a RESP error because blocking commands are not supported. | integration |

Any command not listed here should be treated as not supported in v0.2.

Redis key bytes are forwarded unchanged for supported commands. To keep telemetry and memory bounded, keys longer than 1,024 bytes are not admitted to hotness tracking and cannot become locally cache-eligible. This does not reject or rewrite the upstream command; it is an explicit Slizen caching limitation surfaced by audit completeness metadata and a Prometheus counter.
