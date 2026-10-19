// noisy_neighbor: tenant A (free tier, 100 events/s quota) sends at 10x its quota while tenants B..J (standard) send a
// nominal load. Pass: A receives 429s, and B..J's p99 stays within 20 % of their own baseline (the baseline phase runs the
// same nominal load alone for BASELINE before A starts; both p99 values are reported, the comparison is in docs/benchmarks.md).
import { Counter, Trend } from 'k6/metrics';
import { createTenant, createMeter, createCustomers, eventsBody, postEvents, envNum } from './lib.js';

const BATCH = envNum('BATCH', 100);
const NOMINAL_EPS = envNum('NOMINAL_EVENTS_PER_SEC', 500); // per neighbour tenant
const A_EPS = envNum('NOISY_EVENTS_PER_SEC', 1000); // 10x the free tier's 100 events/s
const NEIGHBOURS = envNum('NEIGHBOURS', 9);
const BASELINE = __ENV.BASELINE || '2m';
const NOISE = __ENV.NOISE || '3m';
const rl = (eps) => Math.max(1, Math.round(eps / BATCH));
const noisy429 = new Counter('noisy_tenant_429');
const noisy202 = new Counter('noisy_tenant_202');
const neighbourBaseline = new Trend('neighbour_baseline_ms', true);
const neighbourUnderNoise = new Trend('neighbour_under_noise_ms', true);

export const options = {
  scenarios: {
    baseline: {
      executor: 'constant-arrival-rate', rate: rl(NOMINAL_EPS * NEIGHBOURS), timeUnit: '1s', duration: BASELINE,
      preAllocatedVUs: 50, maxVUs: 300, exec: 'neighbour', env: { PHASE: 'baseline' },
    },
    neighbours: {
      executor: 'constant-arrival-rate', rate: rl(NOMINAL_EPS * NEIGHBOURS), timeUnit: '1s', duration: NOISE,
      startTime: BASELINE, preAllocatedVUs: 50, maxVUs: 300, exec: 'neighbour', env: { PHASE: 'noise' },
    },
    noisy: {
      executor: 'constant-arrival-rate', rate: rl(A_EPS), timeUnit: '1s', duration: NOISE, startTime: BASELINE,
      preAllocatedVUs: 20, maxVUs: 200, exec: 'noisy',
    },
  },
  thresholds: { noisy_tenant_429: ['count>0'] },
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
};

export function setup() {
  const run = Date.now().toString(36);
  const a = createTenant(`k6-noisy-a-${run}`, 'TENANT_TIER_FREE');
  createMeter(a, 'calls');
  a.customers = createCustomers(a, 10);
  const neighbours = [];
  for (let i = 0; i < NEIGHBOURS; i++) {
    const t = createTenant(`k6-noisy-n${i}-${run}`, 'TENANT_TIER_STANDARD');
    createMeter(t, 'calls');
    t.customers = createCustomers(t, 20);
    neighbours.push(t);
  }
  return { a, neighbours };
}

export function noisy(data) {
  const res = postEvents(data.a, eventsBody(data.a.customers, ['calls'], BATCH), { tenant_group: 'noisy' });
  if (res.status === 429) noisy429.add(1);
  if (res.status === 202) noisy202.add(1);
}

export function neighbour(data) {
  const t = data.neighbours[__ITER % data.neighbours.length];
  const res = postEvents(t, eventsBody(t.customers, ['calls'], BATCH), { tenant_group: 'neighbour', phase: __ENV.PHASE });
  (__ENV.PHASE === 'baseline' ? neighbourBaseline : neighbourUnderNoise).add(res.timings.duration);
}
