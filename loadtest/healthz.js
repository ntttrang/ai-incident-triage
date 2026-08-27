import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate } from 'k6/metrics';

// Endpoint: GET /healthz
// Use this as a smoke/baseline load test against the liveness probe.

const errorRate = new Rate('healthz_errors');

export const options = {
  vus: 20,          // 20 virtual users
  duration: '30s',  // for 30 seconds
  thresholds: {
    http_req_duration: ['p(95)<200'],   // 95% of requests < 200ms
    healthz_errors: ['rate<0.01'],      // < 1% errors
  },
};

const BASE = __ENV.BASE_URL || 'http://localhost:8085';

export default function () {
  const res = http.get(`${BASE}/healthz`);
  const ok = check(res, {
    'status is 200': (r) => r.status === 200,
  });
  errorRate.add(!ok);
  sleep(1); // 1s pause between iterations per VU
}
