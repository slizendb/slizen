# Redis Compatibility

Slizen v0.2 is a Redis-compatible read proxy for a deliberately small command
subset. Redis or Valkey remains the source of truth. The table below is the
complete command contract in the published v0.2.3-rc.1 prerelease; any other command is classified as `unsupported`. The published v0.2.2 image has the
same command-name subset, but it predates the offline compatibility-report
command and the v0.2.3 pre-dispatch invalidation/epoch refinements.

| Command | Status | Behavior | Tested |
| --- | --- | --- | --- |
| `BLMOVE` | rejected | Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model. | classification unit |
| `BLPOP` | rejected | Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model. | unit + integration |
| `BRPOP` | rejected | Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model. | classification unit |
| `BRPOPLPUSH` | rejected | Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model. | classification unit |
| `BZPOPMAX` | rejected | Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model. | classification unit |
| `BZPOPMIN` | rejected | Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model. | classification unit |
| `DEL` | supported | Affected local entries are invalidated before upstream dispatch and remain invalidated if the outcome errors or is ambiguous. | unit + integration |
| `EXEC` | rejected | Rejected before upstream dispatch because transactions are stateful and unsupported. | integration |
| `EXISTS` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `EXPIRE` | supported | Only `EXPIRE key seconds` is accepted; Redis `NX`, `XX`, `GT`, and `LT` options are unsupported. The affected local entry is invalidated before dispatch and remains invalidated on an error or ambiguous outcome. | unit + integration |
| `GET` | supported | Cache-aware read in `cache` mode, including bounded two-hit admission; upstream read only in `observe` mode. | unit + integration |
| `HDEL` | rejected | Rejected before upstream dispatch; hash mutations are outside the v0.2 invalidation contract. | unit |
| `HSET` | rejected | Rejected before upstream dispatch; hash mutations are outside the v0.2 invalidation contract. | unit |
| `LPOP` | rejected | Rejected before upstream dispatch; list mutations are outside the v0.2 invalidation contract. | unit |
| `LPUSH` | rejected | Rejected before upstream dispatch; list mutations are outside the v0.2 invalidation contract. | unit |
| `MGET` | supported | Ordered multi-key read with protected hits or probationary promotions where available in `cache` mode. | unit + integration |
| `MONITOR` | rejected | Rejected before upstream dispatch because monitoring mode is connection-stateful. | integration |
| `MSET` | rejected | Rejected before upstream dispatch because multi-key writes are outside the v0.2 invalidation contract. | unit |
| `MULTI` | rejected | Rejected before upstream dispatch because transactions are stateful and unsupported. | integration |
| `PERSIST` | supported | The affected local entry is invalidated before upstream dispatch and remains invalidated if the outcome errors or is ambiguous. | unit |
| `PEXPIRE` | supported | Only `PEXPIRE key milliseconds` is accepted; Redis `NX`, `XX`, `GT`, and `LT` options are unsupported. The affected local entry is invalidated before dispatch and remains invalidated on an error or ambiguous outcome. | unit + integration |
| `PING` | supported | Handled locally and returns `PONG` or the provided payload. | unit + integration |
| `PSETEX` | supported | The affected local entry is invalidated before upstream dispatch and remains invalidated if the outcome errors or is ambiguous. | unit |
| `PSUBSCRIBE` | rejected | Rejected before upstream dispatch because Pub/Sub is connection-stateful and unsupported. | classification unit |
| `PTTL` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `QUIT` | supported | Returns `OK`, stops dispatching the current pipeline, and closes the client connection. | parity unit |
| `RENAME` | rejected | Rejected before upstream dispatch until source and destination invalidation semantics are supported. | unit |
| `RPOP` | rejected | Rejected before upstream dispatch; list mutations are outside the v0.2 invalidation contract. | unit |
| `RPUSH` | rejected | Rejected before upstream dispatch; list mutations are outside the v0.2 invalidation contract. | unit |
| `SADD` | rejected | Rejected before upstream dispatch; set mutations are outside the v0.2 invalidation contract. | unit |
| `SELECT` | supported | `SELECT 0` is accepted as a local no-op; other databases are rejected. | unit |
| `SET` | supported | The key is invalidated before dispatch and remains invalidated on an error or ambiguous outcome. `SET GET` is rejected. Only a successful exact option-free `SET` may refresh an already admitted cache-policy key. | unit + integration |
| `SETEX` | supported | The affected local entry is invalidated before upstream dispatch and remains invalidated if the outcome errors or is ambiguous. | unit |
| `SREM` | rejected | Rejected before upstream dispatch; set mutations are outside the v0.2 invalidation contract. | unit |
| `SSUBSCRIBE` | rejected | Rejected before upstream dispatch because Pub/Sub is connection-stateful and unsupported. | classification unit |
| `SUBSCRIBE` | rejected | Rejected before upstream dispatch because Pub/Sub is connection-stateful and unsupported. | integration |
| `TTL` | pass-through | Forwarded to upstream without local cache behavior. | unit |
| `UNLINK` | supported | Affected local entries are invalidated before upstream dispatch and remain invalidated if the outcome errors or is ambiguous. | unit |
| `UNWATCH` | rejected | Rejected before upstream dispatch because transactions are stateful and unsupported. | classification unit |
| `WATCH` | rejected | Rejected before upstream dispatch because watched transactions are stateful and unsupported. | integration |
| `XREAD` | rejected | The entire command is rejected because stream reads may block and are outside the v0.2 connection model. | classification unit |
| `XREADGROUP` | rejected | The entire command is rejected because stream reads may block and are outside the v0.2 connection model. | classification unit |

Commands that are not in the table, including `EVAL`, `EVALSHA`, `SCRIPT`, `FCALL`, `AUTH`, `HELLO`, and `CLIENT`, receive Slizen's unsupported-command error. `rejected` identifies commands Slizen recognizes and refuses deliberately because their mutation, connection-state, transaction, Pub/Sub, or blocking behavior is unsafe for the v0.2 proxy contract.

## Local compatibility report

`slizenctl compatibility report` reads the catalog compiled into that `slizenctl` binary. It makes no network request and does not require a running Slizen, Redis, or Valkey process.

```sh
# Informational: print the complete catalog and always exit zero.
slizenctl compatibility report

# Machine-readable catalog with schema slizen.compatibility.v1.
slizenctl compatibility report --output json

# CI gate after manually reviewing SET argument shapes.
slizenctl compatibility report --output json --accept-limitations GET MGET SET TTL

# A rejected or unsupported command still fails even when limitations are accepted.
slizenctl compatibility report --output json --accept-limitations GET SET EVAL
```

Arguments are top-level Redis command names, compared case-insensitively. The
JSON includes `binary_version`, `binary_commit`, `scope`,
`unknown_command_status`, `gate_applied`, overall `compatible`,
`argument_review_required`, `limitations_accepted`,
`argument_review_commands`, and one row per reported command. A row's
`command_name_compatible` means only that the top-level command name is
supported; it does not approve every Redis argument form.
`unknown_command_status` is always `unsupported`. The compiled version and
commit tie the catalog to the exact `slizenctl` binary that produced it. In a
full-catalog report, `gate_applied` is `false` and `compatible` is `null`;
rejected rows are informational and do not make the command fail. With an
explicit selection, `compatible` is a boolean. A `rejected` or `unsupported`
row always fails the gate. `SET`, `SELECT`, `EXPIRE`, and `PEXPIRE` also fail
until their documented argument limitations have been reviewed and explicitly
acknowledged with `--accept-limitations`.

Published Slizen images stamp both fields. An ad hoc local binary built without
the release linker flags reports `binary_commit: "unknown"` and is not
immutable deployment evidence.

No standalone CLI archive is published today. For a v0.2.3-rc.1 trial, run the
CLI embedded in the verified prerelease image and retain its report:

```sh
export SLIZEN_IMAGE=ghcr.io/slizendb/slizen@sha256:e30ad22f4cb23462af9f05322ff97d6796fc521e2e80dc181c42107e4193b92a
docker run --rm --entrypoint /usr/local/bin/slizenctl \
  "$SLIZEN_IMAGE" version
docker run --rm --entrypoint /usr/local/bin/slizenctl \
  "$SLIZEN_IMAGE" compatibility report --output json \
  --accept-limitations GET MGET SET TTL
```

The published v0.2.2 image predates `compatibility report`; use its documented
table plus the exact application integration suite instead.

This command does **not** capture, inspect, or scan an application's workload.
Build the explicit command list from the application's Redis client usage,
existing telemetry, or a separately approved observation process. The report
does not validate command arguments, Lua contents, pipelines, client-library
handshakes, or deployment-specific behavior. Run an initialization test with
the exact client library and settings: downstream `AUTH`, TLS, `HELLO`, and
`CLIENT` are not supported. Origin credentials belong in Slizen's upstream
configuration, not in the application's Slizen-facing client profile.

Redis key bytes are forwarded unchanged for supported commands. To keep telemetry and memory bounded, keys longer than 1,024 bytes are not admitted to hotness tracking and cannot enter either local cache tier. This does not reject or rewrite the upstream command; it is an explicit Slizen caching limitation surfaced by audit completeness metadata and a Prometheus counter.

The upstream client connects to one standalone Redis/Valkey address. It is not
a Redis Cluster or Sentinel client: topology discovery, failover discovery,
`MOVED`/`ASK` handling, and cross-slot `MGET` behavior are outside v0.2.

Two-hit admission does not expand command compatibility or memory limits. An eligible first miss may retain a probationary value, and one later read can promote and serve it while preserving the original absolute local expiry. Protected and probationary partitions remain within the configured global cache budgets. Direct writes to Redis or Valkey bypass both invalidation and exact-`SET` refresh and may remain stale until local TTL expiration.

## Request admission limits

All parsed commands are subject to bounded proxy admission before conversion or upstream dispatch:

| Limit | Default | Hard configuration maximum |
| --- | ---: | ---: |
| Command bytes | 1 MiB | 16 MiB |
| RESP arguments, including the command name | 1,024 | 4,096 |
| Keys in one `MGET` | 512 | 2,048 |
| Concurrent accepted proxy connections | 1,024 | 10,000 |

An over-limit command receives a RESP error and Slizen closes that connection so its enlarged read buffer can be released. The byte and argument limits are not pre-allocation parser ceilings: redcon v1.6.2 reads one complete RESP command before invoking Slizen's handler. Upstream `GET` and `MGET` responses are fully materialized and have no separate response-byte heap cap in v0.2.3. redcon can also read several already-buffered pipelined commands and retain their replies until the pipeline flushes, so per-command limits do not bound aggregate pipeline response memory. Keep the proxy on a trusted internal network, retain a container or Pod memory limit, and test the application's maximum pipeline depth multiplied by representative worst-case value/response sizes. An OOM limit contains the process; it is not a graceful request-level bound.
