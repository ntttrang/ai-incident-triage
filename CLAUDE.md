# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Ground rules

- Commit at each plan-phase boundary with conventional prefixes (`feat:`, `fix:`, `ci:`) and no AI attribution. Never push, create remote repos, or open PRs without explicit user approval — the user owns all remote operations.
- The module path `github.com/ntttrang/ai-incident-triage` matches the GitHub username exactly — use it verbatim in imports.
- Never commit secrets, `.env` files, or tokens. `.env.example` carries placeholder names only.

## Commands

- Unit tests: `make test`. Integration tests sit behind the `integration` build tag and need `TEST_DATABASE_URL` pointing at a live Postgres: `make test-integration`. Plain `go test ./...` silently skips them.
- `make lint` requires golangci-lint **v2.x** — `.golangci.yml` uses the v2 `formatters:` schema and fails to parse with a v1 binary.
- Local run: `cp .env.example .env` then `make run` (needs a local Postgres), or `make up` for the full Docker Compose stack (app + Postgres + observability). Env vars override `.env` values.
- Go **1.26** is required by `go.mod` (River queue dependency); a local 1.25 toolchain auto-downloads it via `GOTOOLCHAIN=auto`.
- Go 1.26 distributions ship a minimal tool set; `covdata` is built on demand into `GOROOT/pkg/tool`. Auto-downloaded toolchains live read-only in the module cache, so `make test` (`-coverprofile`) fails there with `no such tool "covdata"`. Fix: install a full toolchain in a writable location (`go install golang.org/dl/go1.26.5@latest && go1.26.5 download`), then run `PATH="$HOME/sdk/go1.26.5/bin:$PATH" make test`. CI is unaffected (setup-go installs into a writable prefix).

## Architecture — hexagonal (ports & adapters)

Bootstrapped from `go-hexagonal-starter` with the user/JWT slice removed; incident domain slices are added phase by phase.

- `internal/domain` — entities and port interfaces. Only stdlib + `google/uuid` imports allowed; framework types (Gin, pgx, OTel, LLM SDKs) never cross this boundary.
- `internal/service` — business logic; depends only on domain ports (+ metrics).
- `internal/adapter/http` — Gin handlers and middleware. Keep handlers thin: bind → call service → `mapError` → respond.
- `internal/adapter/postgres` — the only place with SQL. Map `pgx.ErrNoRows` → `domain.ErrNotFound` and PG error `23505` → `domain.ErrConflict`.
- Error convention: return `domain.Err*` sentinels for expected outcomes, wrap unexpected errors with `%w`; HTTP responses never leak internals (see `mapError` in `internal/adapter/http/errors.go`).

## Gotchas

- Migrations run automatically on startup (golang-migrate). Add new ones as `migrations/NNNNNN_name.up.sql` plus a matching `.down.sql`. Path differs by environment: `file://migrations` locally, `file:///app/migrations` in Docker. The runner tolerates zero migrations (`ErrNoChange`/`ErrNilVersion`), and `migrations/.gitkeep` exists so the Docker `COPY migrations` stays valid — keep the directory present.
- The observability stack (OTEL Collector, Tempo, Prometheus, Loki, Promtail, Grafana) is vendored in this repo under `deploy/observability/` and merged into `docker-compose.yml` — a fresh clone needs no external IaC repo and no manual `docker network create`.
- App serves on port **8085** (host + container). Grafana on :3000 (admin/admin, anonymous Viewer), Prometheus :9090, Tempo :3200, Loki :3100.
- Prometheus scrapes `app:8085`, `postgres-exporter:9187`, and the collector's self-metrics on `otel-collector:8888` (enabled via `telemetry.metrics.address: 0.0.0.0:8888` in the collector config).
