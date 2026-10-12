.PHONY: run build test test-fast lint migrate migrate-create web db db-stop db-destroy db-logs cli

BINARY  := bin/tindra
COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")
DEV_COMPOSE := $(COMPOSE) -p tindra-dev -f docker-compose.dev.yml
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

# Load .env if it exists; copy .env.example to .env for local dev
-include .env
export

run:
	go run -ldflags="-X main.Version=$(VERSION) -X main.Commit=$(COMMIT)" ./cmd/tindra serve

cli:
	go run ./cmd/tindra $(ARGS)

# The binary embeds the frontend, so build it first.
build: web
	@mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT)" -o $(BINARY) ./cmd/tindra

# Testcontainers' reaper (Ryuk) removes the Postgres containers even when a run
# is interrupted, so it stays enabled.
test:
	DOCKER_HOST=$(shell docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null) \
		go test -race -p 8 ./...

test-fast:
	TINDRA_TEST_DSN=postgres://tindra:tindra@localhost:5432/postgres?sslmode=disable \
		go test -race -p 8 ./...

lint:
	golangci-lint run ./...

migrate:
	go run ./cmd/tindra migrate

# Migrations only move forward, so there is no .down.sql. The number follows
# the highest existing one rather than the file count, which gaps would skew.
migrate-create:
	@test -n "$(name)" || { echo "Usage: make migrate-create name=<name>"; exit 1; }
	@n=$$(ls migrations/*.up.sql 2>/dev/null | sed 's#migrations/##' | sort | tail -1 | awk -F_ '{ print $$1 + 1 }'); \
	n=$${n:-1}; \
	up=$$(printf "migrations/%04d_%s.up.sql" $$n "$(name)"); \
	touch "$$up"; \
	echo "Created $$up"

web:
	cd web && bun run build

# Start only Postgres for local dev (use alongside `make run`)
db:
	$(DEV_COMPOSE) up -d postgres

db-stop:
	$(DEV_COMPOSE) stop postgres

# Permanently remove the local development database and its stored data.
db-destroy:
	$(DEV_COMPOSE) down --volumes --remove-orphans

db-logs:
	$(DEV_COMPOSE) logs -f postgres
