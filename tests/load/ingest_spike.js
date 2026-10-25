// ingest_spike: 1k -> 20k events/s over 2 minutes, then back. Targets: no 5xx, only 429s above quota, recovery < 2 min.
import { Counter } from 'k6/metrics';
import { check } from 'k6';
import { createTenant, createMeter, createCustomers, eventsBody, postEvents, envNum } from './lib.js';

const BATCH = envNum('BATCH', 100);
const PEAK = envNum('PEAK_EVENTS_PER_SEC', 20000);
const BASE = envNum('BASE_EVENTS_PER_SEC', 1000);
const TENANTS = envNum('TENANTS', 10);
const rl = (eps) => Math.max(1, Math.round(eps / BATCH));
const status5xx = new Counter('payrudder_5xx');
const status429 = new Counter('payrudder_429');

export const options = {
  scenarios: {
    spike: {
      executor: 'ramping-arrival-rate', startRate: rl(BASE), timeUnit: '1s',
      preAllocatedVUs: envNum('VUS', 100), maxVUs: envNum('MAX_VUS', 1000),
      stages: [
        { target: rl(BASE), duration: '30s' }, { target: rl(PEAK), duration: '1m' }, { target: rl(PEAK), duration: '1m' },
        { target: rl(BASE), duration: '10s' }, { target: rl(BASE), duration: '2m' },
      ],
    },
  },
  thresholds: { payrudder_5xx: ['count==0'] },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const run = Date.now().toString(36);
  const tenants = [];
  for (let i = 0; i < TENANTS; i++) {
    const t = createTenant(`k6-spike-${run}-${i}`, 'TENANT_TIER_STANDARD');
    createMeter(t, 'calls');
    t.customers = createCustomers(t, 20);
    tenants.push(t);
  }
  return { tenants };
}

export default function (data) {
  const t = data.tenants[__ITER % data.tenants.length];
  const res = postEvents(t, eventsBody(t.customers, ['calls'], BATCH));
  if (res.status >= 500) status5xx.add(1);
  if (res.status === 429) status429.add(1);
  check(res, { 'only 202 or 429': (r) => r.status === 202 || r.status === 429 });
}
