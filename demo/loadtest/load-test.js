/**
 * k6 load test for the StatGate demo application.
 *
 * Usage:
 *   # Against local port-forward (kubectl port-forward svc/istio-ingressgateway -n istio-system 8080:80)
 *   k6 run load-test.js
 *
 *   # Override base URL and virtual users
 *   k6 run --env BASE_URL=http://192.168.49.2:30080 --env ERROR_SCENARIO=true load-test.js
 *
 * Scenarios:
 *   default      — steady mixed traffic, verifies rollout advances cleanly (error rate ≈ 0 %)
 *   high_error   — elevated error injection (set ERROR_SCENARIO=true), triggers SPRT rollback
 *
 * The script targets the Istio ingress gateway.  All requests carry the header
 *   Host: demo.statgate.local
 * which matches the Istio Gateway host rule.
 */

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Counter, Trend } from 'k6/metrics';

// ── Configuration ────────────────────────────────────────────────────────────

// Normalize BASE_URL: add http:// if no scheme is present so that passing
// just an IP (e.g. --env BASE_URL=127.0.0.1) still works.
const _raw           = __ENV.BASE_URL || 'http://localhost:8080';
const BASE_URL       = _raw.startsWith('http') ? _raw.replace(/\/$/, '') : 'http://' + _raw.replace(/\/$/, '');
const ERROR_SCENARIO = __ENV.ERROR_SCENARIO  === 'true';

const HOST_HEADER = { headers: { 'Host': 'demo.statgate.local', 'Content-Type': 'application/json' } };

// ── Custom metrics ────────────────────────────────────────────────────────────

const errorRate   = new Rate('http_error_rate');
const orderCreate = new Trend('order_create_duration_ms', true);
const orderList   = new Trend('order_list_duration_ms',   true);
const totalReqs   = new Counter('total_requests');

// ── Test stages ───────────────────────────────────────────────────────────────

//  Normal scenario — gradual ramp, sustained load, cool-down.
//  Simulates production traffic during a canary rollout.
const normalOptions = {
  stages: [
    { duration: '20s', target: 20  },  // warm-up
    { duration: '60s', target: 20  },  // step 1 (5 % canary)
    { duration: '60s', target: 20  },  // step 2 (25 % canary)
    { duration: '60s', target: 20  },  // step 3 (50 % canary)
    { duration: '20s', target: 0   },  // cool-down
  ],
  thresholds: {
    http_error_rate:          ['rate<0.02'],  // < 2 % errors overall
    order_create_duration_ms: ['p(95)<500'],  // p95 create < 500 ms
    order_list_duration_ms:   ['p(95)<200'],  // p95 list   < 200 ms
    http_req_failed:          ['rate<0.02'],
  },
};

// Error injection scenario — causes SPRT to cross the rollback boundary.
// Run v2 with ERROR_RATE=0.3 already baked into the canary deployment.
const errorOptions = {
  stages: [
    { duration: '20s', target: 20 },
    { duration: '90s', target: 20 },
    { duration: '20s', target: 0  },
  ],
  thresholds: {
    // We intentionally inject errors, so thresholds are permissive here.
    // The SPRT controller should detect the degradation and abort the rollout.
    http_error_rate: ['rate<0.5'],
  },
};

export const options = ERROR_SCENARIO ? errorOptions : normalOptions;

// ── Helpers ──────────────────────────────────────────────────────────────────

function randomTitle() {
  const nouns = ['Widget', 'Gadget', 'Doohickey', 'Thingamajig', 'Whatsit'];
  const adj   = ['Blue', 'Fast', 'Shiny', 'Heavy', 'Tiny'];
  return `${adj[Math.floor(Math.random() * adj.length)]} ${nouns[Math.floor(Math.random() * nouns.length)]}`;
}

// ── Main virtual user loop ────────────────────────────────────────────────────

export default function () {
  totalReqs.add(1);

  // 70 % of the time: create an order (write path — exercises canary logic).
  if (Math.random() < 0.7) {
    const payload = JSON.stringify({ title: randomTitle() });
    const res = http.post(`${BASE_URL}/orders`, payload, HOST_HEADER);

    const ok = check(res, {
      'create: status is 201': (r) => r.status === 201,
      'create: has id':        (r) => r.json('id') !== undefined,
    });

    errorRate.add(!ok);
    orderCreate.add(res.timings.duration);

    // Persist the created order id for a follow-up GET (realistic user flow).
    if (ok && res.json('id')) {
      const id  = res.json('id');
      const get = http.get(`${BASE_URL}/orders/${id}`, HOST_HEADER);
      check(get, { 'get order: status 200': (r) => r.status === 200 });
    }
  } else {
    // 30 % of the time: list orders (read path).
    const res = http.get(`${BASE_URL}/orders`, HOST_HEADER);
    const ok  = check(res, { 'list: status 200': (r) => r.status === 200 });
    errorRate.add(!ok);
    orderList.add(res.timings.duration);
  }

  sleep(0.5 + Math.random() * 0.5); // 0.5–1.0 s think time
}

// ── Lifecycle hooks ───────────────────────────────────────────────────────────

export function setup() {
  const res = http.get(`${BASE_URL}/healthz`, HOST_HEADER);
  if (res.status !== 200) {
    console.warn(`Health check failed (${res.status}) — is the gateway reachable at ${BASE_URL}?`);
  }
}
