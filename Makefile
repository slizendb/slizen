VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -X github.com/slizendb/slizen/internal/buildinfo.Version=$(VERSION) -X github.com/slizendb/slizen/internal/buildinfo.Commit=$(COMMIT)

.PHONY: fmt vet test race build check release-check benchmark demo-up demo demo-report demo-down smoke docker-up docker-down docker-compose-up docker-compose-down

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

build:
	go build -ldflags "$(LDFLAGS)" ./...

check: fmt vet test race build

release-check:
	./scripts/release_check.sh

benchmark:
	go run -ldflags "$(LDFLAGS)" ./cmd/slizenctl benchmark hotkey

demo-up:
	docker compose up --build -d

demo:
	./scripts/demo.sh

demo-report:
	./scripts/demo_report.sh

demo-down:
	docker compose down --remove-orphans

smoke:
	./scripts/smoke.sh

docker-up: demo-up

docker-down: demo-down

docker-compose-up:
	docker compose up --build -d

docker-compose-down:
	docker compose down
