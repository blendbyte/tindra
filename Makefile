.PHONY: run build test lint migrate migrate-create web db db-stop db-logs cli

BINARY  := bin/tindra
COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Load .env if it exists — copy .env.example to .env for local dev
-include .env
export

run:
	go run -ldflags="-X main.Version=$(VERSION) -X main.Commit=$(COMMIT)" ./cmd/tindra serve

cli:
	go run ./cmd/tindra $(ARGS)

build:
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)" -o $(BINARY) ./cmd/tindra

test:
	DOCKER_HOST=$(shell docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null) \
	TESTCONTAINERS_RYUK_DISABLED=true \
		go test -race -p 8 ./...

test-fast:
	TINDRA_TEST_DSN=postgres://tindra:tindra@localhost:5432/postgres?sslmode=disable \
		go test -race -p 8 ./...

lint:
	golangci-lint run ./...

migrate:
	go run ./cmd/tindra migrate

migrate-create:
	@test -n "$(name)" || { echo "Usage: make migrate-create name=<name>"; exit 1; }
	@n=$$(ls migrations/*.up.sql 2>/dev/null | wc -l | tr -d '[:space:]'); \
	n=$$((n + 1)); \
	up=$$(printf "migrations/%04d_%s.up.sql" $$n "$(name)"); \
	down=$$(printf "migrations/%04d_%s.down.sql" $$n "$(name)"); \
	touch "$$up" "$$down"; \
	echo "Created $$up"; \
	echo "Created $$down"

web:
	cd web && bun run build

# Start only Postgres for local dev (use alongside `make run`)
db:
	$(COMPOSE) up postgres -d

db-stop:
	$(COMPOSE) stop postgres

db-logs:
	$(COMPOSE) logs -f postgres

# Absorb extra words passed after `make cli ...` so Make doesn't error
%:
	@:
