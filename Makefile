.PHONY: build test lint fmt generate migrate

BINARY := factvault
DSN ?= $(FACTVAULT_DATABASE_URL)

build:
	go build -o bin/$(BINARY) ./cmd/factvault

test:
	go test ./... -count=1

lint:
	go vet ./...

fmt:
	gofumpt -w .

generate:
	sqlc generate

migrate:
	go run ./cmd/factvault migrate --dsn "$(DSN)"
