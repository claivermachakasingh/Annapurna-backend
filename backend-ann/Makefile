# ─── Food Waste Redistribution Platform – Makefile ────────────────────────────
SHELL       := /bin/bash
GO          := go
BINARY      := bin/server
MIGRATE_DIR := database/migrations
DB_URL      ?= $(DATABASE_URL)

.PHONY: up down migrate-up migrate-down migrate-version seed run worker test lint vet \
        gen gen-drift demo reset fmt

# ── Docker Compose lifecycle ───────────────────────────────────────────────────
up:
	docker compose up -d
	@echo "Waiting for postgres..."
	@until docker compose exec postgres pg_isready -q; do sleep 1; done
	$(MAKE) migrate-up

down:
	docker compose down -v --remove-orphans

# ── Database migrations ────────────────────────────────────────────────────────
# cmd/migrate is a dependency-free runner that keeps the same
# schema_migrations(version, dirty) table golang-migrate uses, so the two are
# interchangeable.
migrate-up:
	$(GO) run ./cmd/migrate -dir=$(MIGRATE_DIR) up

migrate-down:
	$(GO) run ./cmd/migrate -dir=$(MIGRATE_DIR) down

migrate-version:
	$(GO) run ./cmd/migrate -dir=$(MIGRATE_DIR) version

# ── Demo data (idempotent; regenerates QR chains through internal/qrchain) ─────
seed:
	$(GO) run ./cmd/seed

# ── Run the services ──────────────────────────────────────────────────────────
run:
	$(GO) run ./cmd/server

worker:
	$(GO) run ./cmd/worker

# ── Tests ──────────────────────────────────────────────────────────────────────
test:
	$(GO) test -race -coverprofile=coverage.out -covermode=atomic ./...
	$(GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

# ── Lint ───────────────────────────────────────────────────────────────────────
lint:
	@which golangci-lint > /dev/null 2>&1 || \
		(curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin)
	golangci-lint run ./...

# ── Code generation ────────────────────────────────────────────────────────────
gen:
	$(GO) generate ./...

# Ensures go generate produces no diff (CI gate)
gen-drift:
	$(MAKE) gen
	@if ! git diff --exit-code; then \
		echo "ERROR: go generate produced uncommitted changes"; \
		exit 1; \
	fi

# ── Demo data + full stack ────────────────────────────────────────────────────
demo: up seed
	@echo "Demo stack ready. API at http://localhost:$${PORT:-8000}"

# ── Full reset (nuke DB + redis, re-apply migrations, seed) ──────────────────
reset: down up seed
	@echo "Reset complete."
