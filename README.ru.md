# Slizen

[![CI](https://github.com/slizendb/slizen/actions/workflows/ci.yml/badge.svg)](https://github.com/slizendb/slizen/actions/workflows/ci.yml)
![Go](https://img.shields.io/badge/go-1.26+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-Apache--2.0-blue)

**Developer Preview.** Hot-key autopilot for Redis and Valkey.

Slizen — экспериментальный адаптивный cache-proxy для read-heavy нагрузок. Он ставится перед Redis/Valkey, измеряет температуру ключей, кэширует только разогретые значения `GET`, объединяет одновременные cache miss и инвалидирует локальные копии, когда запись проходит через proxy.

Slizen v0.2 — single-node, не source of truth и поддерживает только ограниченный набор Redis-команд. Прямые записи в Redis/Valkey в обход Slizen могут оставаться stale до истечения local TTL. Admin API по умолчанию bind-ится локально и в v0.2 не имеет встроенной аутентификации.

**Evidence, а не обещание скорости.** В воспроизводимом image-bound synthetic-run v0.2.2 на 1 000 ключей с распределением 99/1 Slizen измерил **на 89,778% меньше origin GET** (`94 961` напрямую против `9 707` через Slizen), cache-hit ratio `73,628%` и ноль request failures, value mismatches, final-validation failures и final-validation mismatches. Атрибутированный read p99 был `2,137 ms` через Slizen против `1,460 ms` напрямую, поэтому это не утверждение «Slizen быстрее». Вот [сырой JSON релиза](https://github.com/slizendb/slizen/releases/download/v0.2.2/slizen-workload-result.json) и [методика](docs/BENCHMARKING.md); результат относится только к конкретному runner, конфигурации и workload.

**v0.2.3 пока release candidate в source tree.** Bounded two-hit admission и exact-SET refresh для уже admitted keys снизили origin GET с `94 961` напрямую до `798`–`803` в пяти неизменённых локальных cold 99/1 повторах (`99,154390%`–`99,159655%` снижения) при нуле failures и mismatches. Read p99 Slizen был `1,175`–`1,251 ms` против `0,986`–`1,042 ms` напрямую: это результат про снижение origin load, а не обещание скорости или универсальных 99%. Это локальный candidate evidence, не tag, опубликованный image, digest или image-bound release; стабильная установка ниже остаётся v0.2.2 до публикации. См. [candidate notes](docs/RELEASE_NOTES_v0.2.3.md).

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

Если origin недоступен как `host.docker.internal:6379`, подставь его приватный Docker/DNS-адрес. См. [последний релиз](https://github.com/slizendb/slizen/releases/latest), [release notes v0.2.2](docs/RELEASE_NOTES_v0.2.2.md) и [безопасную конфигурацию](docs/CONFIGURATION.md).

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

В candidate v0.2.3 режим `cache` делит те же глобальные лимиты: семь восьмых защищённому admitted tier и одну восьмую probationary candidates. Первый подходящий успешный miss может сохранить короткоживущий candidate; один следующий read повышает и отдаёт его без нового origin GET, сохраняя исходный абсолютный срок expiration. Это не добавляет память сверх `cache.max_bytes` и `cache.max_entries`.

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
- pre-write conservative invalidation и exact-SET refresh только для уже admitted cache-policy keys;
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

`/v1/status`, workload JSON и Prometheus отдельно показывают cache misses с фиксированными причинами `policy_bypass`, `not_admitted` и `not_present`. Оба cache tier входят в те же aggregate bytes/entries. Redis keys никогда не используются как labels.

`/v1/audit` и `slizenctl audit` выдают bounded JSON-отчёт с effective policy, recommendation и стабильными reason codes. Policy prefixes и Redis values в отчёт не попадают. `telemetry_complete=false` означает, что текущий набор обрезан лимитом отчёта, tracker уже вытеснял ключи, unseen observation был пропущен ради текущего HOT FIFO victim при полном tracker или ключ длиннее 1 024 байт не наблюдался. Capacity drops видны в `capacity_observations_dropped` и метрике `slizen_hotness_capacity_observations_dropped_total`; O(1) правило защищает текущий HOT victim, но не обещает unlimited scan resistance.

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
- [docs/RELEASE_NOTES_v0.2.2.md](docs/RELEASE_NOTES_v0.2.2.md)
- [docs/RELEASE_NOTES_v0.2.3.md](docs/RELEASE_NOTES_v0.2.3.md) — source-tree release candidate

## Roadmap

[Релиз v0.2.2 по производительности](https://github.com/slizendb/slizen/releases/tag/v0.2.2) опубликован 2026-07-22 вместе с checksummed image-bound bundle и отдельным 100 000-key evidence. v0.2.3 пока только source-tree release candidate: bounded two-hit admission, exact-SET refresh и fixed miss attribution реализованы и локально повторены, но clean-commit gate, tag, image, provenance и exact-image evidence ещё не закрыты. v0.3 — Redis/Valkey-assisted invalidation для прямых записей в origin с fail-safe поведением. Mesh, Operator и hosted control plane остаются более поздними гипотезами и начнутся только после подтверждённого спроса реальных пользователей.

Gossip не даёт write consensus. Slizen остаётся cache layer.
