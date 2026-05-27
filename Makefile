.PHONY: build test lint fmt generate migrate setup pr-watch

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

## setup: Start postgres+embedder, migrate, and run init (keygen + health checks + example load).
setup:
	docker compose up -d postgres embedder
	@until docker compose exec -T postgres pg_isready -U factvault -d factvault -q; do \
		echo "waiting for postgres..."; sleep 2; \
	done
	go build -o bin/$(BINARY) ./cmd/factvault
	./bin/$(BINARY) migrate
	./bin/$(BINARY) init \
		--dsn "$${FACTVAULT_DATABASE_URL:-postgres://factvault:factvault@localhost:5432/factvault?sslmode=disable}" \
		--tenant "$${FACTVAULT_DEV_TENANT_ID:-11111111-1111-1111-1111-111111111111}"

pr-watch:
	./scripts/pr-autopilot.sh $(PR)
