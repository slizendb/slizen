# First GitHub Issues

Create these as separate issues rather than one mega-ticket.

## 1. Add per-prefix cache policy

Labels: `milestone/v0.2`, `correctness`, `configuration`

Acceptance:

- support longest-prefix policy matching;
- support `deny`, `observe`, and `cache` modes per prefix;
- enforce per-prefix max item bytes and max TTL;
- add an ADR before implementation.

## 2. Add fuzz tests for command handling

Labels: `security`, `testing`, `protocol`

Acceptance:

- fuzz `ParseCommand` and response conversion helpers;
- no panic;
- seed corpus covers empty commands, mixed case, unsupported commands, huge arguments, and binary bulk data.

## 3. Expand invalidation coverage

Labels: `correctness`, `protocol`

Acceptance:

- table-driven tests for supported write commands;
- explicit rejection or support decision for `MSET`, `RENAME`, and common hash/list/set mutations;
- documentation update for the command table.

## 4. Measure cache-hit allocations

Labels: `performance`, `benchmark`

Acceptance:

- record benchmark output for cache hit, cache miss, hotness observe, and proxy GET integration;
- do not optimize until numbers are recorded.

## 5. Add admin pprof behind an explicit flag

Labels: `observability`, `security`

Acceptance:

- disabled by default;
- only on the private admin listener;
- documentation warning;
- no import-side registration on the default mux.

## 6. Add graceful connection drain accounting

Labels: `reliability`

Acceptance:

- track active proxy handlers during shutdown;
- wait with a bounded deadline;
- do not block forever on slow or malicious clients.
