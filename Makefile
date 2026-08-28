APP_NAME := ai-incident-triage
MODULE := github.com/ntttrang/ai-incident-triage
BIN := bin/api
GO ?= go

# k6 load testing
K6 ?= k6
BASE_URL ?= http://localhost:8085
LOADTEST_VUS ?= 20
LOADTEST_DURATION ?= 30s
SEED_URL ?= http://localhost:8085

.PHONY: help tidy build run test test-integration lint gosec govulncheck trivy migrate-up migrate-down up down logs docker-build loadtest loadtest-health seed

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

tidy: ## Download and tidy Go modules
	$(GO) mod tidy

build: ## Build the API binary
	$(GO) build -o $(BIN) ./cmd/api

run: ## Run the API locally
	$(GO) run ./cmd/api

test: ## Run unit tests
	$(GO) test ./... -count=1 -race -coverprofile=coverage.out -covermode=atomic

test-integration: ## Run integration tests (requires Postgres; set TEST_DATABASE_URL)
	$(GO) test -p 1 ./... -tags=integration -count=1 -race -timeout 5m

lint: ## Run golangci-lint
	golangci-lint run ./...

gosec: ## Run gosec security scanner
	gosec ./...

govulncheck: ## Run Go vulnerability check
	govulncheck ./...

trivy: ## Run Trivy filesystem scan
	trivy fs .

migrate-up: ## Apply DB migrations (DATABASE_URL required)
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down: ## Roll back last migration
	migrate -path migrations -database "$(DATABASE_URL)" down 1

up: ## Start app + Postgres + observability stack
	docker compose up --build -d

down: ## Stop the full stack and remove volumes
	docker compose down -v --remove-orphans

logs: ## Tail app logs
	docker compose logs -f app

docker-build: ## Build Docker image
	docker build -t $(APP_NAME):local .

loadtest: loadtest-health ## Run k6 load test (override BASE_URL, LOADTEST_VUS, LOADTEST_DURATION)

loadtest-health: ## Smoke load test against /healthz
	$(K6) run -e BASE_URL=$(BASE_URL) --vus $(LOADTEST_VUS) --duration $(LOADTEST_DURATION) loadtest/healthz.js

seed: ## POST Jira fixtures through the real webhook (stack must be up)
	$(GO) run ./cmd/seed -url $(SEED_URL)
