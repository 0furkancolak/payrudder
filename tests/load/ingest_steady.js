// ingest_steady: EVENTS_PER_SEC (target 5000) in batches of BATCH (100) spread over TENANTS (10) tenants for DURATION (15m).
// Targets: p99 < 50 ms, 0 errors; aggregator lag p99 < 5 s is read from Prometheus (consumer_lag_seconds{group="aggregator"}).
import { createTenant, createMeter, createCustomers, eventsBody, postEvents, accepted, envNum } from './lib.js';

const EPS = envNum('EVENTS_PER_SEC', 5000);
const BATCH = envNum('BATCH', 100);
const TENANTS = envNum('TENANTS', 10);

export const options = {
  scenarios: {
    steady: {
      executor: 'constant-arrival-rate', rate: Math.max(1, Math.round(EPS / BATCH)), timeUnit: '1s',
      duration: __ENV.DURATION || '15m', preAllocatedVUs: envNum('VUS', 50), maxVUs: envNum('MAX_VUS', 400),
    },
  },
  thresholds: {
    'http_req_duration{name:POST /v1/events}': [{ threshold: 'p(99)<50', abortOnFail: false }],
    http_req_failed: ['rate==0'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const run = Date.now().toString(36);
  const tenants = [];
  for (let i = 0; i < TENANTS; i++) {
    const t = createTenant(`k6-steady-${run}-${i}`, 'TENANT_TIER_ENTERPRISE');
    createMeter(t, 'calls');
    t.customers = createCustomers(t, 20);
    tenants.push(t);
  }
  return { tenants };
}

export default function (data) {
  const t = data.tenants[__ITER % data.tenants.length];
  accepted(postEvents(t, eventsBody(t.customers, ['calls'], BATCH)));
}
