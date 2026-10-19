// entitlement_reads: RATE (target 2000) GET /v1/customers/{id}/entitlements/{meter} per second. Target: p99 < 10 ms.
import http from 'k6/http';
import { check } from 'k6';
import { GATEWAY, createTenant, createMeter, createCustomers, envNum } from './lib.js';

const TENANTS = envNum('TENANTS', 5);

export const options = {
  scenarios: {
    reads: {
      executor: 'constant-arrival-rate', rate: envNum('RATE', 2000), timeUnit: '1s', duration: __ENV.DURATION || '3m',
      preAllocatedVUs: envNum('VUS', 50), maxVUs: envNum('MAX_VUS', 500),
    },
  },
  thresholds: {
    'http_req_duration{name:GET entitlement}': [{ threshold: 'p(99)<10', abortOnFail: false }],
    http_req_failed: ['rate==0'],
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const run = Date.now().toString(36);
  const tenants = [];
  for (let i = 0; i < TENANTS; i++) {
    const t = createTenant(`k6-reads-${run}-${i}`, 'TENANT_TIER_ENTERPRISE');
    createMeter(t, 'calls');
    t.customers = createCustomers(t, 50);
    tenants.push(t);
  }
  return { tenants };
}

export default function (data) {
  const t = data.tenants[__ITER % data.tenants.length];
  const c = t.customers[Math.floor(Math.random() * t.customers.length)];
  const res = http.get(`${GATEWAY}/v1/customers/${c}/entitlements/calls`, {
    headers: { Authorization: `Bearer ${t.key}` }, tags: { name: 'GET entitlement' },
  });
  check(res, { 'entitlement answered 200': (r) => r.status === 200 });
}
