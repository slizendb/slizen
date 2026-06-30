.PHONY: fmt vet test race build demo docker-up docker-down

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

demo:
	./scripts/demo.sh

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down
