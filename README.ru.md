# Slizen

[![CI](https://github.com/gazakov/slizen/actions/workflows/ci.yml/badge.svg)](https://github.com/gazakov/slizen/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-Apache--2.0-blue)

**Developer Preview.** Hot-key autopilot for Redis and Valkey.

Slizen — экспериментальный адаптивный cache-proxy для read-heavy нагрузок. Он ставится перед Redis/Valkey, измеряет температуру ключей, кэширует только разогретые значения `GET`, объединяет одновременные cache miss и инвалидирует локальные копии, когда запись проходит через proxy.

Slizen v0.1 — single-node, не source of truth и поддерживает только ограниченный набор Redis-команд. Прямые записи в Redis/Valkey в обход Slizen могут оставаться stale до истечения local TTL. Admin API по умолчанию bind-ится локально и в v0.1 не имеет встроенной аутентификации.

```text
Приложение ── RESP ──> Slizen ── RESP ──> Redis / Valkey
                         │
                         ├─ hot-key tracker
                         ├─ bounded TTL/LRU cache
                         ├─ request coalescing
                         └─ admin API + Prometheus metrics
```

## Быстрый старт

Нужен Docker Compose.

```sh
git clone https://github.com/gazakov/slizen.git
cd slizen
make demo-up
make demo
curl http://127.0.0.1:9090/v1/status
make demo-down
```

Для локальной Go-разработки с уже запущенным Redis/Valkey на `127.0.0.1:6379`:

```sh
cp slizen.example.toml slizen.toml
go run ./cmd/slizend --config ./slizen.toml
redis-cli -p 6380 SET product:iphone_17 '{"name":"iPhone 17","price":999}'
redis-cli -p 6380 GET product:iphone_17
go run ./cmd/slizenctl status --admin http://127.0.0.1:9090
```

## Режимы работы

По умолчанию Slizen работает в режиме:

```toml
mode = "cache"
```

Для первой безопасной проверки перед staging можно включить:

```sh
SLIZEN_MODE=observe go run ./cmd/slizend --config ./slizen.toml
```

В `observe` режиме Slizen проксирует команды в origin и считает hot-key telemetry, но никогда не отдаёт local cache hit, не coalesce-ит `GET` и никогда не сохраняет значения в локальном кэше. Это режим для ответа на вопрос: какие ключи реально создают перекос нагрузки.

## Docker Compose demo

```sh
make demo-up
redis-cli -p 6380 SET product:iphone_17 '{"name":"iPhone 17","price":999}'
redis-cli -p 6380 GET product:iphone_17
make demo
curl http://127.0.0.1:9090/v1/status
make demo-down
```

Или:

```sh
./scripts/demo.sh
```

## Что уже есть

- `slizend` RESP proxy на `:6380`;
- admin API на `:9090`;
- bounded local cache;
- hot-key tracker с hysteresis и cooldown;
- request coalescing через `singleflight`;
- write-driven invalidation;
- Prometheus metrics;
- CLI `slizenctl`;
- Docker Compose demo с Valkey;
- unit tests, race tests, integration/smoke checks, benchmark and CI.

## Compatibility и benchmark

- [docs/REDIS_COMPATIBILITY.md](docs/REDIS_COMPATIBILITY.md) — какие Redis-команды реально поддержаны, проксируются или отклоняются.
- [docs/BENCHMARKING.md](docs/BENCHMARKING.md) — как воспроизвести hot-key benchmark и как читать результат.

```sh
make demo-up
make benchmark
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

## Честные ограничения

Slizen v0.1 не production-ready.

- Redis или Valkey остаётся source of truth.
- Slizen не durable database.
- v0.1 single-node only.
- Mesh и репликации между Slizen-нодами пока нет.
- Прямые записи в Redis/Valkey в обход Slizen могут оставаться stale до истечения local TTL.
- Поддерживается только ограниченный набор Redis-команд.
- Admin API без встроенной аутентификации и должен оставаться приватным.

## Наблюдаемость

```sh
curl http://127.0.0.1:9090/healthz
curl http://127.0.0.1:9090/readyz
curl http://127.0.0.1:9090/v1/status
curl http://127.0.0.1:9090/v1/hotkeys
curl http://127.0.0.1:9090/v1/cache
curl http://127.0.0.1:9090/metrics
```

Raw values никогда не попадают в логи, метрики или admin API. Hot-key output по умолчанию показывает HMAC-SHA256 identifier ключа. `privacy.key_visibility = "plain"` включай только на приватном admin listener для локальной отладки.

## Разработка

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build ./...
make check
make release-check
make smoke
```

Release материалы:

- [CHANGELOG.md](CHANGELOG.md)
- [docs/DEMO.md](docs/DEMO.md)
- [docs/RELEASE_CHECKLIST.md](docs/RELEASE_CHECKLIST.md)
- [docs/PUBLIC_RELEASE_CHECKLIST.md](docs/PUBLIC_RELEASE_CHECKLIST.md)
- [docs/RELEASE_NOTES_v0.1.md](docs/RELEASE_NOTES_v0.1.md)

## Roadmap

Сначала observation mode и воспроизводимые benchmarks. Потом RESP3/server-assisted invalidation, sidecar deployment, static multi-node membership, и только после этого adaptive mesh.

Gossip не даёт write consensus. Slizen остаётся cache layer.
