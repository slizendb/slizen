# С чего начать

Этот файл — короткий маршрут для проверки Slizen после `git clone`. Текущий source tree — release candidate v0.2.3, а не уже опубликованный релиз: стабильный tag/image/evidence пока остаётся v0.2.2.

## 1. Локальная проверка Go

```sh
make check
```

Это запускает:

```sh
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go build ./...
```

## 2. Docker demo

Нужен Docker Compose.

```sh
make demo-up
make demo
curl http://127.0.0.1:9090/v1/status
make demo-down
```

`make demo` поднимает stack при необходимости, ждёт `/readyz`, проверяет `/healthz`, `/readyz`, `/v1/status`, `/metrics`, пишет и читает тестовый ключ через Slizen, запускает короткий Black Friday demo и оставляет stack запущенным для осмотра.

## 3. CI/local smoke

```sh
make smoke
```

Smoke-test поднимает Docker Compose, проверяет настоящий Valkey + настоящий `slizend`, делает SET/GET через Slizen proxy, проверяет bounded two-hit admission, privacy-safe `/v1/audit`, HMAC hot-key output, fixed-reason cache-miss metrics, cache hits, exact-SET refresh для admitted key, unsupported command error и затем останавливает stack.

## 4. Release check и demo evidence

```sh
make release-check
make demo-report
cat ./tmp/demo-report.md
```

`make release-check` — строгий release gate: он требует Helm, Docker Compose, работающий Docker daemon и `jq`, запускает Go checks, shell/docs checks, Kubernetes validation, `make smoke`, а затем четыре workload-сценария на отдельном Docker Compose stack. Gate использует 1 000 keys, concurrency 32, ровно 100 000 generated operations и 30 секунд только как safety cap на phase. Он проходит только при уникальном isolated key prefix, совпадающих version/commit Slizen, известной версии origin, нулевых `value_mismatches`, согласованной сумме `policy_bypass`/`not_admitted`/`not_present` misses и валидном изолированном evidence для всех четырёх сценариев. Стабильный 99/1 skew обязан показать реальные cache hits и доказанное снижение origin GET; uniform или быстро движущийся flash вправе честно показать отсутствие выигрыша. Stack всегда останавливается после проверки.

Workload evidence сохраняется в `./tmp/slizen-workload-result.json`.

Release workflow сначала пропускает tagged source через этот gate, затем публикует multi-architecture image и повторно собирает evidence уже из `ghcr.io/slizendb/slizen@sha256:...`. Manifest связывает exact image digest, full commit и version; GitHub-native provenance можно проверить через `gh attestation verify`.

В пяти локальных candidate-повторах неизменённого cold request-bound `skew-99-1` (seed 42, 1 000 keys, 100 000 operations, 95/5, 128 B, concurrency 32) direct сделал 94 961 origin GET каждый раз, Slizen — 798–803: снижение 99,154390–99,159655% при нуле failures/mismatches. Read p99 Slizen 1,175–1,251 ms против 0,986–1,042 ms direct, поэтому это evidence снижения нагрузки на origin, не speed claim. До tag и exact-image run это остаётся локальным RC evidence.

`make demo-report` требует Docker Compose, запускает benchmark и сохраняет:

- `./tmp/slizen-benchmark-result.json`
- `./tmp/status-before.json`
- `./tmp/status-after.json`
- `./tmp/hotkeys.json`
- `./tmp/audit.json`
- `./tmp/demo-report.md`

## 5. Режимы

Безопасный режим по умолчанию:

```toml
mode = "observe"
```

Для безопасного наблюдения перед staging достаточно:

```sh
SLIZEN_MODE=observe go run ./cmd/slizend --config ./slizen.example.toml
```

В `observe` режиме Slizen не отдаёт cache hits, не coalesce-ит `GET` и не сохраняет значения.

В `cache` режиме v0.2.3 делит прежние глобальные cache budgets: 7/8 для protected values и 1/8 для probationary candidates. Первый подходящий miss может сохранить candidate, а один последующий read — повысить и отдать его, сохранив исходный absolute expiry. Успешный exact option-free `SET` обновляет local value только для уже admitted cache-policy key; остальные writes и ambiguous errors консервативно инвалидируют обе части cache. Прямые writes в origin всё ещё могут оставаться stale до local TTL.

После окна наблюдения получи bounded audit без Redis values и policy prefixes:

```sh
go run ./cmd/slizenctl audit --admin http://127.0.0.1:9090
```

Если tracker заполнен и текущий FIFO victim уже `HOT`, unseen observation
пропускается за O(1), а не вытесняет его и не запускает scan. Проверяй
`capacity_observations_dropped`,
`slizen_hotness_capacity_observations_dropped_total` и
`telemetry_complete=false`: это честный сигнал неполной telemetry, а не
обещание unlimited scan resistance.

## 6. Privacy

По умолчанию hot-key output использует HMAC identifiers. Если secret не задан, Slizen генерирует криптографически случайный process-local secret: он не логируется, но identifiers меняются после restart.

```toml
[privacy]
key_visibility = "hash"
# key_hash_secret = "load-from-your-secret-manager"
```

Задавай стабильный high-entropy `key_hash_secret` через secret manager только если identifiers должны сравниваться между restart.

`privacy.key_visibility = "plain"` используй только на приватном admin listener для локальной отладки.

## 7. Compatibility

Таблица поддержанных, pass-through и отклонённых Redis-команд лежит в [docs/REDIS_COMPATIBILITY.md](docs/REDIS_COMPATIBILITY.md).
