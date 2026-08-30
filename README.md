# AI Incident Triage

AI-assisted incident management service: ingests incidents (Jira webhook + fixtures), classifies and prioritizes them with an LLM, stores everything in PostgreSQL, and processes classification asynchronously through a durable queue — with end-to-end observability (traces, metrics, logs) in a single `docker compose up`.

Built in Go with a hexagonal architecture (ports & adapters), forked from [go-hexagonal-starter](https://github.com/ntttrang/go-hexagonal-starter).

## Status

Phase 1 — repository bootstrap complete. Phase 2 — incident domain live: signed Jira webhook ingest with two-level idempotency, incident read API, `make seed`. Phase 3 — classification pipeline live: every ingest transactionally enqueues a River job; a worker process classifies incidents with OpenAI structured outputs, falls back to a deterministic heuristic when the LLM is unavailable (circuit breaker), and stores the verdict with `classification_source` + a suggested runbook. Dashboards and the eval harness land in the next phases.

## Quick start (Docker Compose)

```bash
cp .env.example .env
docker compose up --build
```

Brings up the app, the classification worker, PostgreSQL, postgres-exporter, and the observability stack (OTEL Collector, Tempo, Prometheus, Loki, Promtail, Grafana) on one compose-defined network — no external dependencies. With no `OPENAI_API_KEY` set, every incident still classifies via the heuristic fallback, so the demo works without credentials.

| Service | URL |
|---------|-----|
| API | http://localhost:8085 |
| Worker metrics | not published to the host; `docker compose exec worker wget -qO- http://127.0.0.1:8086/metrics` (Prometheus scrape config lands with the phase-4 dashboards) |
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
cmd/api                 composition root (API + transactional enqueue)
cmd/worker              composition root (River consumer + classifier chain)
internal/domain         entities + ports (no framework deps)
internal/service        application use cases (ingest, classify)
internal/adapter/http   Gin handlers + middleware
internal/adapter/postgres  SQL repositories + atomic ingest store (only place with SQL)
internal/adapter/queue  River client, enqueuer, classify worker
internal/adapter/llm    OpenAI structured-output classifier, heuristic fallback, circuit breaker
internal/adapter/kb     runbook knowledge base (static stub; Notion RAG later)
internal/platform       config, db, logger, metrics, tracing
migrations              SQL migrations (run on startup)
deploy/observability    collector, tempo, prometheus, loki, promtail, grafana configs
```

**Classification pipeline.** A webhook delivery writes the incident row and
enqueues a `classify_incident` River job in **one transaction** — an incident can
never exist without its job. The worker consumes the `classify` queue (4
attempts, 150s per-attempt budget) and runs the classifier chain:

```
OpenAI (strict JSON schema, 60s budget)
  └─ on failure → circuit breaker (3 strikes, 60s cooldown)
        └─ heuristic fallback (deterministic, never fails)
              └─ both fail → River retry → after 4 attempts: incident marked failed,
                             job retained forever as a dead letter
```

Every stored verdict records `classification_source` (`llm` | `heuristic`), so
degradation is observable in the API response and metrics
(`classification_total{source,severity}`, `classification_duration_seconds`).
A new delivery of the same issue re-queues it and resets the previous verdict.

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
| `make seed` | POST Jira fixtures through the webhook (stack must be up) |
| `make run-worker` | Run the classification worker locally against the compose DB |
| `make reset-seed` | Reset volumes, rebuild, start the stack, wait healthy, reseed |

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness (DB ping) |
| GET | `/metrics` | Prometheus metrics |
| GET | `/debug/pprof/*` | Go pprof (non-production only) |
| POST | `/api/v1/webhooks/jira` | Ingest a Jira issue webhook (HMAC-verified, idempotent) |
| GET | `/api/v1/incidents` | List incidents (`status`, `severity`, `limit`, `offset`) |
| GET | `/api/v1/incidents/{id}` | One incident |

**Webhook ingest.** The body must be a Jira issue event (`issue.key` and
`issue.fields.summary` required; anything issue-shaped is stored). Requests are
authenticated with `X-Hub-Signature: sha256=<hex>` — HMAC-SHA256 of the raw body
with `WEBHOOK_SECRET` (Jira Cloud's native scheme; the app refuses to boot
without the secret set). Delivery-level idempotency via the
`X-Atlassian-Webhook-Identifier` header:

| Delivery | Response |
|----------|----------|
| New issue | `202` `{"outcome":"created"}` |
| Same issue, new delivery (e.g. `jira:issue_updated`) | `200` `{"outcome":"updated"}` — raw payload and delivery id refreshed |
| Replayed delivery identifier (Jira redelivers up to 5×) | `200` `{"outcome":"duplicate"}` — no write |

The read API never returns the stored raw webhook body (`incidents.raw` stays in
Postgres for debugging). After ingest, the worker classifies each incident and
the response gains `category`, `severity`, `priority_score`, `confidence`,
`rationale`, `suggested_runbook`, and `classification_source`. Seed 12 sample
incidents (10 issues + one update + one replay) through the real HTTP path:

```bash
make seed
make reset-seed   # same, but from a clean database — the canonical demo command
```

**Degradation demo.** `CLASSIFY_FORCE_FAIL=llm` makes every verdict come from
the heuristic (visible as `"classification_source":"heuristic"`); `=all` drives
incidents to `status:"failed"` after 4 attempts with the jobs retained as dead
letters. Test/demo knob only.

## Configuration

See [`.env.example`](.env.example). Key variables: `DB_*` (PostgreSQL), `OTEL_EXPORTER_OTLP_ENDPOINT` (empty = tracing off), `OTEL_SERVICE_NAME` / `SERVICE_VERSION` (resource attributes), `OPENAI_API_KEY` (LLM classification — empty = heuristic-only demo mode), `OPENAI_MODEL` / `OPENAI_TIMEOUT_SECS` (LLM tuning), `CLASSIFY_FORCE_FAIL` (degradation demo knob), `WORKER_METRICS_PORT` (worker `/metrics` port), `WEBHOOK_SECRET` (Jira webhook verification). Real secret values live only in `.env`, which is never committed.

## CI/CD

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on PRs and pushes to `main`: `go vet`, golangci-lint, unit + integration tests (against a Postgres service), build, plus keyless security scans (gosec, govulncheck, Trivy SARIF). Pushes to `main` also publish the container image to GHCR.

## License

MIT
