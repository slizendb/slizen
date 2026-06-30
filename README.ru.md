# Slizen

> Автопилот для горячих ключей Redis и Valkey.

Slizen — экспериментальный адаптивный cache-proxy для read-heavy нагрузок. Он ставится перед Redis/Valkey, измеряет температуру ключей, кэширует только разогретые значения `GET`, объединяет одновременные cache miss и инвалидирует локальные копии, когда запись проходит через proxy.

```text
Приложение ── RESP ──> Slizen ── RESP ──> Redis / Valkey
                         │
                         ├─ hot-key tracker
                         ├─ bounded TTL/LRU cache
                         ├─ request coalescing
                         └─ admin API + Prometheus metrics
```

## Быстрый старт

```sh
cp slizen.example.toml slizen.toml
go run ./cmd/slizend --config ./slizen.toml
```

Во втором терминале:

```sh
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
docker compose up --build -d
redis-cli -p 6380 SET product:iphone_17 '{"name":"iPhone 17","price":999}'
redis-cli -p 6380 GET product:iphone_17
go run ./cmd/slizenctl demo black-friday \
  --redis 127.0.0.1:6380 \
  --admin http://127.0.0.1:9090 \
  --key product:iphone_17 \
  --workers 100 \
  --duration 20s
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
- unit tests, race tests, benchmarks and CI.

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

Raw values никогда не попадают в логи, метрики или admin API. Hot-key output по умолчанию показывает salted hash ключа.

## Разработка

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build ./...
```

## Roadmap

Сначала observation mode и воспроизводимые benchmarks. Потом RESP3/server-assisted invalidation, sidecar deployment, static multi-node membership, и только после этого adaptive mesh.

Gossip не даёт write consensus. Slizen остаётся cache layer.
