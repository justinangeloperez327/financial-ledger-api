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
	gofmt -w cmd internal db tests

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
	go run ./cmd/migrate

migrate-down:
	@test -n "$$DATABASE_URL" || (echo "DATABASE_URL must be exported" >&2; exit 1)
	goose -dir db/migrations postgres "$$DATABASE_URL" down

integration:
	@test -n "$$TEST_DATABASE_URL" || (echo "TEST_DATABASE_URL must point to a disposable database" >&2; exit 1)
	go test -race -count=1 ./tests/integration

token:
	go run ./cmd/token $(ARGS)
