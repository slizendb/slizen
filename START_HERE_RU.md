# С чего начать

Этот файл — короткий маршрут для проверки Slizen после `git clone`.

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

Smoke-test поднимает Docker Compose, проверяет настоящий Valkey + настоящий `slizend`, делает SET/GET через Slizen proxy, проверяет HMAC hot-key output, Prometheus metrics, cache hits, write invalidation, unsupported command error и затем останавливает stack.

## 4. Режимы

По умолчанию:

```toml
mode = "cache"
```

Для безопасного наблюдения перед staging:

```sh
SLIZEN_MODE=observe go run ./cmd/slizend --config ./slizen.example.toml
```

В `observe` режиме Slizen не отдаёт cache hits, не coalesce-ит `GET` и не сохраняет значения.

## 5. Privacy

По умолчанию hot-key output использует HMAC identifiers:

```toml
[privacy]
key_visibility = "hash"
key_hash_secret = "change-me"
```

Перед публичной демонстрацией поменяй `key_hash_secret`.

`privacy.key_visibility = "plain"` используй только на приватном admin listener для локальной отладки.
