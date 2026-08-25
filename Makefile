.PHONY: build test testdb-leak-gate lint fmt generate migrate setup pr-watch vuln-policy vuln-policy-selftest

BINARY := factvault
# Migrations need superuser privileges (CREATE EXTENSION); FACTVAULT_MIGRATE_DATABASE_URL is the
# correct default here, not FACTVAULT_DATABASE_URL (app_user, used by api/workers/mcp at runtime).
DSN ?= $(FACTVAULT_MIGRATE_DATABASE_URL)

build:
	go build -o bin/$(BINARY) ./cmd/factvault

test:
	go test ./... -count=1
	$(MAKE) testdb-leak-gate

testdb-leak-gate:
	TESTDB_LEAK_GATE=1 go test ./internal/testdb -run '^TestNoTestDBLeaks$$' -count=1

# Standing govulncheck policy (B19 / #307): actionable runtime findings only.
# Scans the module whose directory is the current working directory.
vuln-policy:
	./scripts/govulncheck-policy.sh ./...

# Adversarial self-test: fixture MUST fail, repo root MUST pass, filter edge cases.
vuln-policy-selftest:
	./scripts/govulncheck-policy_test.sh

lint:
	go vet ./...

fmt:
	gofumpt -w .

generate:
	sqlc generate

migrate:
	go run ./cmd/factvault migrate --dsn "$(DSN)"

## setup: Start postgres+embedder, migrate (as superuser), and run init (keygen + health checks + example load).
setup:
	docker compose up -d postgres embedder
	@until docker compose exec -T postgres pg_isready -U factvault -d factvault -q; do \
		echo "waiting for postgres..."; sleep 2; \
	done
	go build -o bin/$(BINARY) ./cmd/factvault
	# Migrate runs as superuser — CREATE EXTENSION requires superuser privileges.
	FACTVAULT_DATABASE_URL="$${FACTVAULT_MIGRATE_DATABASE_URL:-postgres://factvault:factvault@localhost:5432/factvault?sslmode=disable}" \
		./bin/$(BINARY) migrate
	# Init runs as app_user (matching production access pattern; exercises GRANTs).
	# DSN is passed via env, not --dsn: password-bearing --dsn values are rejected
	# by design (see internal/config.ValidateDSNNoPassword).
	FACTVAULT_DATABASE_URL="$${FACTVAULT_DATABASE_URL:-postgres://app_user:$${POSTGRES_APP_USER_PASSWORD:-dev_only_local_password}@localhost:5432/factvault?sslmode=disable}" \
		./bin/$(BINARY) init \
		--skip-migrate \
		--tenant "$${FACTVAULT_DEV_TENANT_ID:-11111111-1111-1111-1111-111111111111}"

pr-watch:
	./scripts/pr-autopilot.sh $(PR)
