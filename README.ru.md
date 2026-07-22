# Slizen

[![CI](https://github.com/slizendb/slizen/actions/workflows/ci.yml/badge.svg)](https://github.com/slizendb/slizen/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-Apache--2.0-blue)

**Developer Preview.** Hot-key autopilot for Redis and Valkey.

Slizen — экспериментальный адаптивный cache-proxy для read-heavy нагрузок. Он ставится перед Redis/Valkey, измеряет температуру ключей, кэширует только разогретые значения `GET`, объединяет одновременные cache miss и инвалидирует локальные копии, когда запись проходит через proxy.

Slizen v0.2 — single-node, не source of truth и поддерживает только ограниченный набор Redis-команд. Прямые записи в Redis/Valkey в обход Slizen могут оставаться stale до истечения local TTL. Admin API по умолчанию bind-ится локально и в v0.2 не имеет встроенной аутентификации.

**Evidence, а не обещание скорости.** В одном воспроизводимом synthetic-run v0.2.0 с распределением ключей 99/1 Slizen измерил **на 91,376% меньше origin GET на успешное чтение**. При этом proxy p99 был `0,624 ms` против `0,377 ms` напрямую, поэтому это не утверждение «Slizen быстрее». Вот [сырой JSON релиза](https://github.com/slizendb/slizen/releases/download/v0.2.0/slizen-workload-result.json) и [методика](docs/BENCHMARKING.md); результат относится только к конкретной машине, конфигурации и workload.

Ищем трёх design partners, которые реально сталкивались с hot-key инцидентами в Redis или Valkey. Если можешь проверить single-node developer preview в изолированной среде, [опиши workload без чувствительных данных](https://github.com/slizendb/slizen/issues/new?template=design-partner.yml).

```text
Приложение ── RESP ──> Slizen ── RESP ──> Redis / Valkey
                         │
                         ├─ hot-key tracker
                         ├─ bounded TTL/LRU cache
                         ├─ request coalescing
                         └─ admin API + Prometheus metrics
```

## Установка

Публичный multi-architecture image лежит в GHCR. Тег `0.2` указывает на свежий patch-релиз ветки v0.2; для воспроизводимого deployment используй immutable digest из release evidence.

```sh
docker pull ghcr.io/slizendb/slizen:0.2
```

Запуск в observe-only режиме с Redis или Valkey на хосте:

```sh
docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  -p 127.0.0.1:6380:6380 \
  -p 127.0.0.1:9090:9090 \
  -e SLIZEN_MODE=observe \
  -e SLIZEN_PROXY_LISTEN=0.0.0.0:6380 \
  -e SLIZEN_ADMIN_LISTEN=0.0.0.0:9090 \
  -e SLIZEN_UPSTREAM_ADDRESS=host.docker.internal:6379 \
  ghcr.io/slizendb/slizen:0.2
```

Если origin недоступен как `host.docker.internal:6379`, подставь его приватный Docker/DNS-адрес. См. [последний релиз](https://github.com/slizendb/slizen/releases/latest), [release notes v0.2.1](docs/RELEASE_NOTES_v0.2.1.md) и [безопасную конфигурацию](docs/CONFIGURATION.md).

## Быстрый старт из исходников

Нужен Docker Compose.

```sh
git clone https://github.com/slizendb/slizen.git
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

Compose demo намеренно включает `cache` поверх одноразового Valkey-контейнера, хотя обычный safe default — `observe`.

## Режимы работы

По умолчанию Slizen работает в безопасном режиме:

```toml
mode = "observe"
```

В `observe` режиме Slizen проксирует команды в origin и считает hot-key telemetry, но никогда не отдаёт local cache hit, не coalesce-ит `GET` и никогда не сохраняет значения в локальном кэше. Это режим для ответа на вопрос: какие ключи реально создают перекос нагрузки.

### Cache policy по префиксам

Опциональные `[[cache.policies]]` выбираются по самому длинному буквальному префиксу ключа. Для selective promotion переключи global mode в `cache`, сохрани пустой `observe` catch-all и добавь более узкие cache-правила:

```toml
mode = "cache"

[[cache.policies]]
prefix = ""
mode = "observe"

[[cache.policies]]
prefix = "session:"
mode = "deny"

[[cache.policies]]
prefix = "catalog:"
mode = "cache"
max_item_bytes = 1048576
max_local_ttl = "10s"
```

`deny` отключает локальный cache и hotness tracking, но не запрещает доступ к Redis; это не ACL. `observe` только измеряет и всегда читает upstream. `cache` включает adaptive caching и требует явные лимиты размера записи и свежего local TTL. Пустой prefix работает как catch-all, unmatched keys наследуют global mode, а глобальный `mode = "observe"` всегда остаётся safety ceiling и выключает локальный cache для всех правил. Конфигурация ограничена 1 024 правилами, 1 024 байтами на prefix, 262 144 байтами префиксов суммарно и 100 000 tracked keys. Redis-ключи длиннее 1 024 байт продолжают проксироваться для поддержанных команд, но не участвуют в hotness telemetry и local cache. Полный контракт описан в [configuration guide](docs/CONFIGURATION.md) и [ADR 0004](docs/adr/0004-per-prefix-cache-policy.md).

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

## Kubernetes staging

В v0.2 есть observe-first [sidecar example](deploy/kubernetes/observe-sidecar.yaml), [standalone Helm chart](charts/slizen/README.md) без Operator и [пошаговый rollout/rollback guide](docs/STAGING_ROLLOUT.md).

```sh
make validate-k8s
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
make benchmark-workload
make demo-report
cat ./tmp/demo-report.md
make demo-down
```

## Честные ограничения

Slizen v0.2 не production-ready.

- Redis или Valkey остаётся source of truth.
- Slizen не durable database.
- v0.2 single-node only.
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
curl http://127.0.0.1:9090/v1/audit
curl http://127.0.0.1:9090/v1/cache
curl http://127.0.0.1:9090/metrics
```

Raw values никогда не попадают в логи, метрики или admin API. Hot-key output по умолчанию показывает HMAC-SHA256 identifier ключа. `privacy.key_visibility = "plain"` включай только на приватном admin listener для локальной отладки.

`/v1/audit` и `slizenctl audit` выдают bounded JSON-отчёт с effective policy, recommendation и стабильными reason codes. Policy prefixes и Redis values в отчёт не попадают. `telemetry_complete=false` означает, что текущий набор обрезан лимитом отчёта, tracker уже вытеснял ключи или наблюдение ключа длиннее 1 024 байт было пропущено.

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
- [docs/RELEASE_NOTES_v0.2.md](docs/RELEASE_NOTES_v0.2.md)
- [docs/RELEASE_NOTES_v0.2.1.md](docs/RELEASE_NOTES_v0.2.1.md)

## Roadmap

v0.2 уже выпущена как safe-staging developer preview. v0.2.1 launch hardening остаётся in progress до нового tag/image: scope не расширяется, но ужесточаются defaults, bounds, evidence и release hygiene. v0.3 — Redis/Valkey-assisted invalidation для прямых записей в origin с fail-safe поведением. Mesh, Operator и hosted control plane остаются более поздними гипотезами и начнутся только после подтверждённого спроса реальных пользователей.

Gossip не даёт write consensus. Slizen остаётся cache layer.
