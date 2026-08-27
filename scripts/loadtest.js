// k6 load test for go-hexagonal-starter.
//
// Drives the public API (register -> login -> users) to generate traffic that feeds
// all three observability pillars at once: metrics (/metrics scrape), logs (stdout),
// and traces (OTLP). Use it to verify that Prometheus, Loki, Tempo, and Grafana are
// wired end-to-end.
//
// Prerequisites:
//   - App + Postgres up: `make up`
//   - IaC observability stack up (Prometheus/Loki/Tempo/collector/Grafana)
//   - OTEL_EXPORTER_OTLP_ENDPOINT set to host:port in .env (e.g. otel-collector:4317)
//
// Run:
//   make loadtest
//   # or with overrides:
//   k6 run scripts/loadtest.js
//   K6_VUS=50 RUN_ID=2 BASE_URL=http://localhost:8085 k6 run scripts/loadtest.js
//
// Verify after the run:
//   - Prometheus: rate(http_requests_total[5m])
//   - Loki:       {service="go-hexagonal-starter"} | json | trace_id != ""
//   - Tempo:      { resource.service.name = "go-hexagonal-starter" }

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE = (__ENV.BASE_URL || 'http://localhost:8085').replace(/\/+$/, '');
const RUN_ID = __ENV.RUN_ID || '0';
const VUS = parseInt(__ENV.K6_VUS || '20', 10);

// Treat 401 (intentional unauth), 404, and 409 (re-run register conflict) as expected
// so they don't inflate http_req_failed. Genuine 5xx still register as failures.
http.setResponseCallback(http.expectedStatuses({ min: 200, max: 299 }, 401, 404, 409));

export const options = {
  stages: [
    { duration: '30s', target: VUS }, // ramp up
    { duration: '1m', target: VUS }, //  hold
    { duration: '30s', target: 0 }, //  ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed: ['rate<0.01'],
  },
};

export default function () {
  const email = `loadtest-${RUN_ID}-${__VU}-${__ITER}@example.com`;
  const password = 'password123';
  const jsonHeaders = { headers: { 'Content-Type': 'application/json' } };

  // Register -> 201 (user_operations_total). 409 on re-run is expected.
  http.post(
    `${BASE}/api/v1/auth/register`,
    JSON.stringify({ email, name: `User ${__VU}`, password }),
    jsonHeaders,
  );

  // Login -> 200 (auth_login_total{result=success}); read the JWT.
  const login = http.post(
    `${BASE}/api/v1/auth/login`,
    JSON.stringify({ email, password }),
    jsonHeaders,
  );
  const token = login.json('token');
  check(token, { 'login returned a token': (t) => !!t });

  const authHeaders = { headers: { Authorization: `Bearer ${token}` } };

  // Happy path -> 200 (populates http_requests_total + duration histogram).
  http.get(`${BASE}/api/v1/users`, authHeaders);

  // Error path -> 401 (populates error/latency panels).
  http.get(`${BASE}/api/v1/users`);

  sleep(0.1);
}
