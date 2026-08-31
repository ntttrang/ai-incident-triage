# Observability Notes

How the stack's telemetry actually works, including the decisions that
deviate from textbook setups and why. Companion to the dashboards in
`deploy/observability/`.

## Metrics

Two metric paths exist side by side:

1. **Direct Prometheus scrape** (`app:8085`, `worker:8086`): request RED,
   DB pool, ingest/classification counters, LLM latency and tokens. These
   are hand-registered `prometheus/client_golang` metrics.
2. **OTLP → collector → Prometheus bridge** (`otel-collector:8889`): the
   `river.*` tier emitted by `otelriver` middleware. River exports no
   native Prometheus queue-depth metric, so queue telemetry rides the
   OpenTelemetry metric pipeline: Go `MeterProvider` (periodic OTLP export)
   → collector `prometheus` exporter → the `otel-collector-metrics` scrape
   job. That scrape job sets `honor_labels: true` so the bridge's
   service-name `job` label survives and `by (job)` splits per process.

   Bridge gotcha: a rebuilt service restarts its counters from zero, but the
   collector keeps serving the previous process's last values until the new
   process emits the same label set again — a series the new process never
   emits (e.g. `river_work_count_total{status="error"}` from an old incident)
   lingers as a phantom. Restart `otel-collector` (or ignore the stale
   series) after rebuilding a service during a demo.

Metric names worth knowing:

| Metric | Source | Meaning |
|---|---|---|
| `incidents_received_total{outcome}` | app | webhook outcomes (created/updated/duplicate) |
| `classification_total{severity,source}` | worker | verdicts by severity and classifier |
| `llm_request_duration_seconds{model,outcome}` | worker | LLM latency histogram, success/failed split |
| `llm_tokens_total{type}` | worker | prompt/completion tokens |
| `llm_fallback_trips_total` | worker | heuristic answers after LLM failure |
| `classify_jobs_failed_total` | worker | incidents marked failed after retries |
| `river_work_count_total` | bridge | jobs worked (via OTLP bridge) |
| `river_insert_count_total` | bridge | jobs enqueued (via OTLP bridge) |

## Queue depth comes from Postgres, not metrics

River does not expose queue depth as a metric, and depth is a point-in-time
gauge anyway. The dashboard's queue panels query `river_job` directly
through a read-only Grafana datasource:

- `deploy/observability/postgres-init/grafana-ro.sql` creates `grafana_ro`
  (SELECT-only) — mounted at `/docker-entrypoint-initdb.d`, so it runs
  **only on a fresh data volume**. Because the file executes before the app
  has applied River's migrations, the grant is an `ALTER DEFAULT
  PRIVILEGES`: tables the app creates afterwards — `river_job` included —
  become SELECT-able automatically. On an existing volume apply the same
  SQL by hand.
- The datasource is provisioned in
  `deploy/observability/grafana/provisioning/datasources/datasources.yaml`
  with compose-local demo credentials only.

## Trace story (webhook → worker)

The API creates a span for `/api/v1/webhooks/jira` and enqueues the job
inside that span. `otelriver`'s `EnableTracePropagation` injects the W3C
`traceparent` into the job's metadata at insert; the worker's `river.work`
span extracts it and links back to it.

The result is **two linked traces, not one nested trace** — the OpenTelemetry
convention for async work, since a job may run minutes after enqueue and a
direct parent would produce misleading waterfalls:

- Tempo search by `service.name=ai-incident-triage` finds the webhook trace
  (`/api/v1/webhooks/jira` → `river.insert_many`).
- Tempo search by `service.name=ai-incident-triage-worker` finds the work
  trace (`river.work`), whose span *link* points at the enqueue span.
- Worker log lines carry the work-span `trace_id`; the Loki datasource's
  derived field turns it into a "View Trace" link, so
  worker log → Tempo → linked webhook trace is one round trip.

## What is deliberately absent

- No metrics alerting rules yet — dashboards only for this phase.
- No `river.*` panels beyond work/insert rate: depth and age come from
  Postgres, which is authoritative.
