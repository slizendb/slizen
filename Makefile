.PHONY: fmt vet test race build check demo-up demo demo-down smoke docker-up docker-down docker-compose-up docker-compose-down

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

build:
	go build ./...

check: fmt vet test race build

demo-up:
	docker compose up --build -d

demo:
	./scripts/demo.sh

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
