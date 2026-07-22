# Dependencies

Slizen v0.2.1 keeps dependencies intentionally small.

| Dependency | Purpose |
| --- | --- |
| `github.com/tidwall/redcon` | RESP server for the Redis-compatible proxy listener. |
| `github.com/redis/go-redis/v9` | Redis and Valkey upstream client used by the daemon and demo CLI. |
| `github.com/prometheus/client_golang` | Prometheus registry, metrics, and HTTP exposition. |
| `github.com/pelletier/go-toml/v2` | TOML configuration decoding. |
| `golang.org/x/sync/singleflight` | Request coalescing for concurrent cache misses. |

No other direct third-party runtime dependencies are used in v0.2. Indirect module requirements in `go.mod` are transitive dependencies of the approved libraries and are tracked for reproducible builds.

The repository and OCI image include Slizen's canonical Apache-2.0 `LICENSE` and `NOTICE`. The notice retains attribution required by the Prometheus instrumentation library; module versions and transitive dependency identity remain reproducible through `go.mod`, `go.sum`, and the release SBOM.
