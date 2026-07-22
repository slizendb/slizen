VERSION ?= dev
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
GOVULNCHECK_VERSION ?= v1.5.0
LDFLAGS := -X github.com/slizendb/slizen/internal/buildinfo.Version=$(VERSION) -X github.com/slizendb/slizen/internal/buildinfo.Commit=$(COMMIT)

.PHONY: fmt vet test race build vulncheck check release-check release-evidence local-release-check version benchmark benchmark-workload demo-up demo demo-report demo-down smoke validate-k8s docker-up docker-down docker-compose-up docker-compose-down

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

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check: fmt vet test race build

release-check:
	./scripts/release_check.sh

release-evidence:
	./scripts/release_evidence.sh

local-release-check: release-check

version:
	@go run -ldflags "$(LDFLAGS)" ./cmd/slizend --version
	@go run -ldflags "$(LDFLAGS)" ./cmd/slizenctl version

benchmark:
	go run -ldflags "$(LDFLAGS)" ./cmd/slizenctl benchmark hotkey

benchmark-workload:
	mkdir -p ./tmp
	go run -ldflags "$(LDFLAGS)" ./cmd/slizenctl benchmark workload --key-prefix product:slizen:benchmark --json-file ./tmp/slizen-workload-result.json

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

validate-k8s:
	./scripts/validate_k8s.sh

docker-up: demo-up

docker-down: demo-down

docker-compose-up:
	docker compose up --build -d

docker-compose-down:
	docker compose down
