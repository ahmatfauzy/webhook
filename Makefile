.PHONY: dev build test lint fmt migrate generate docker-build docker-up docker-down

dev:
	go run ./cmd/webhooker server

build:
	go build -o bin/webhooker ./cmd/webhooker

test:
	go test ./... -count=1

lint:
	go vet ./...

fmt:
	go fmt ./...

migrate:
	go run ./cmd/webhooker migrate

migrate-new:
	@read -p "name: " n; \
	mkdir -p migrations; \
	ts=$$(date +%Y%m%d%H%M%S); \
	echo "-- +goose Up" > migrations/$${ts}_$$n.up.sql; \
	echo "-- +goose Down" > migrations/$${ts}_$$n.down.sql; \
	echo "created migrations/$${ts}_$$n.*.sql"

generate:
	@echo "sqlc generate (if installed)"
	@which sqlc >/dev/null 2>&1 && sqlc generate || echo "sqlc not installed, skipping"

docker-build:
	docker compose build

docker-up:
	docker compose up -d

docker-down:
	docker compose down -v
