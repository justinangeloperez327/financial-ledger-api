.PHONY: run test race vet fmt build up down sqlc migrate-up migrate-down

run:
	go run ./cmd/api

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

build:
	go build -o bin/financial-ledger-api ./cmd/api

up:
	docker compose up --build -d

down:
	docker compose down

sqlc:
	sqlc generate

migrate-up:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL must be exported" >&2; exit 1)
	goose -dir db/migrations postgres "$$DATABASE_URL" up

migrate-down:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL must be exported" >&2; exit 1)
	goose -dir db/migrations postgres "$$DATABASE_URL" down
