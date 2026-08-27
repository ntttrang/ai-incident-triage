# AI Incident Triage

AI-assisted incident management service: ingests incidents (Jira webhook + fixtures), classifies and prioritizes them with an LLM, stores everything in PostgreSQL, and processes classification asynchronously through a durable queue — with end-to-end observability (traces, metrics, logs) in a single `docker compose up`.

Built in Go with a hexagonal architecture (ports & adapters), forked from [go-hexagonal-starter](https://github.com/ntttrang/go-hexagonal-starter).

## Status

Phase 1 — repository bootstrap complete: API skeleton (health, metrics, middleware chain), CI, Docker Compose with the full observability stack. Incident ingest, LLM triage worker, and dashboards land in the next phases.

## Quick start (Docker Compose)

```bash
cp .env.example .env
docker compose up --build
```

Brings up the app, PostgreSQL, postgres-exporter, and the observability stack (OTEL Collector, Tempo, Prometheus, Loki, Promtail, Grafana) on one compose-defined network — no external dependencies.

| Service | URL |
|---------|-----|
| API | http://localhost:8085 |
| Grafana | http://localhost:3000 (admin/admin) |
| Prometheus | http://localhost:9090 |
| Tempo | http://localhost:3200 |
| Loki | http://localhost:3100 |

```bash
curl -s http://localhost:8085/healthz   # {"status":"ok"}
curl -s http://localhost:8085/readyz    # readiness (DB ping)
curl -s http://localhost:8085/metrics   # Prometheus metrics
```

## Architecture

```
cmd/api                 composition root
internal/domain         entities + ports (no framework deps)
internal/service        application use cases
internal/adapter/http   Gin handlers + middleware
internal/adapter/postgres  SQL repositories (only place with SQL)
internal/platform       config, db, logger, metrics, tracing
migrations              SQL migrations (run on startup)
deploy/observability    collector, tempo, prometheus, loki, promtail, grafana configs
```

## Observability

Three pillars wired end-to-end and correlated via `trace_id`:

| Pillar | Path |
|--------|------|
| **Traces** | App → OTEL Collector → Tempo; explore from Grafana |
| **Metrics** | Prometheus scrapes `app:8085`, `postgres-exporter:9187`, collector self-metrics |
| **Logs** | stdout (JSON slog with `request_id`, `trace_id`) → Promtail → Loki |

Datasources (Prometheus, Loki, Tempo) are auto-provisioned in Grafana; dashboards live under `deploy/observability/grafana/dashboards/`.

## Local development

Requirements: Go 1.26+, Docker, (optional) golangci-lint v2, migrate CLI.

```bash
cp .env.example .env
docker compose up -d postgres   # just the database

export $(grep -v '^#' .env | xargs)
make run
```

| Target | Description |
|--------|-------------|
| `make test` | Unit tests |
| `make test-integration` | Integration tests (needs `TEST_DATABASE_URL`) |
| `make lint` | golangci-lint |
| `make build` | Build binary to `bin/api` |
| `make up` / `make down` | Start / stop the full stack |

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness (DB ping) |
| GET | `/metrics` | Prometheus metrics |
| GET | `/debug/pprof/*` | Go pprof (non-production only) |

Incident endpoints land with the ingest phase.

## Configuration

See [`.env.example`](.env.example). Key variables: `DB_*` (PostgreSQL), `OTEL_EXPORTER_OTLP_ENDPOINT` (empty = tracing off), `OTEL_SERVICE_NAME` / `SERVICE_VERSION` (resource attributes), `OPENAI_API_KEY` (LLM classification), `WEBHOOK_SECRET` (Jira webhook verification). Real secret values live only in `.env`, which is never committed.

## CI/CD

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on PRs and pushes to `main`: `go vet`, golangci-lint, unit + integration tests (against a Postgres service), build, plus keyless security scans (gosec, govulncheck, Trivy SARIF). Pushes to `main` also publish the container image to GHCR.

## License

MIT
