// month_close: CUSTOMERS (target 10000) customers x METERS (5) meters with usage, then a sandbox close-period; the run is
// timed until every invoice saga is COMPLETED (mock-psp latency excluded: MOCKPSP_LATENCY_MS defaults to 0 locally).
// Target: run COMPLETED < 5 min. Needs a sandbox tenant (PAYRUDDER_ALLOWED_ENVIRONMENTS includes sandbox).
import http from 'k6/http';
import { sleep, fail } from 'k6';
import { Gauge } from 'k6/metrics';
import { GATEWAY, OPERATOR, batchRetry, createTenant, envNum } from './lib.js';


const CUSTOMERS = envNum('CUSTOMERS', 10000);
const METERS = envNum('METERS', 5);
const closeSeconds = new Gauge('month_close_seconds');
const H = (t) => ({ 'Content-Type': 'application/json', Authorization: `Bearer ${t.key}` });

export const options = {
  scenarios: { close: { executor: 'shared-iterations', vus: 1, iterations: 1, maxDuration: __ENV.MAX_DURATION || '60m' } },
  thresholds: { month_close_seconds: ['value<300'] },
  setupTimeout: '60m',
};

function batchPost(t, path, bodies, expect) {
  for (let i = 0; i < bodies.length; i += 50) {
    batchRetry(bodies.slice(i, i + 50).map((b) => ['POST', `${GATEWAY}${path}`, JSON.stringify(b), { headers: H(t), tags: { name: 'setup' } }]), expect);
  }
}

export function setup() {
  const t = createTenant(`k6-close-${Date.now().toString(36)}`, 'TENANT_TIER_ENTERPRISE', { sandbox: true });
  const meters = [];
  for (let m = 0; m < METERS; m++) {
    meters.push(`m${m}`);
    http.post(`${GATEWAY}/v1/meters`, JSON.stringify({ code: `m${m}`, aggregation: 'AGGREGATION_COUNT', quantity_scale: 0, unit_label: 'u' }), { headers: H(t) });
  }
  http.post(`${GATEWAY}/v1/plans`, JSON.stringify({ code: 'p', name: 'P' }), { headers: H(t) });
  http.post(`${GATEWAY}/v1/plans/p/versions`, JSON.stringify({
    base_fee_minor: 1000, prices: meters.map((m) => ({ meter_code: m, per_unit: { unit_price: '1' } })),
  }), { headers: H(t) });
  const custs = [];
  for (let i = 0; i < CUSTOMERS; i++) custs.push({ external_id: `c${i}`, name: `c${i}`, email: `c${i}@example.com`, payment_method: 'pm_sandbox_success' });
  batchPost(t, '/v1/customers', custs, 201);
  batchPost(t, '/v1/subscriptions', custs.map((c) => ({ customer_external_id: c.external_id, plan_code: 'p' })), 201);
  const at = new Date(Date.now() - 60000).toISOString();
  for (let i = 0; i < CUSTOMERS; i += 100) {
    const events = [];
    for (let c = i; c < Math.min(CUSTOMERS, i + 100); c++) for (const m of meters) events.push({ customer_external_id: `c${c}`, meter_code: m, event_time: at });
    for (let j = 0; j < events.length; j += 500) {
      const r = http.post(`${GATEWAY}/v1/events`, JSON.stringify({ events: events.slice(j, j + 500) }), { headers: H(t) });
      if (r.status !== 202) fail(`events: ${r.status} ${r.body}`);
    }
  }
  return { t };
}

export default function ({ t }) {
  const started = Date.now();
  const r = http.post(`${GATEWAY}/v1/sandbox/close-period`, null, { headers: H(t) });
  if (r.status !== 202) fail(`close-period: ${r.status} ${r.body}`);
  // The tenant's time zone is Europe/Istanbul (UTC+3, no DST): the period being closed is the current month there.
  const period = new Date(Date.now() + 3 * 3600 * 1000).toISOString().slice(0, 7);
  for (;;) {
    const run = http.get(`${GATEWAY}/admin/v1/tenants/${t.id}/billing-runs/${period}`,
      { headers: { Authorization: `Bearer ${OPERATOR}` }, tags: { name: 'poll' } });
    if (run.status === 200) {
      const status = run.json().status;
      if (status.endsWith('COMPLETED')) break;
      if (status.endsWith('FAILED')) fail(`billing run failed: ${run.body}`);
    }
    if (Date.now() - started > 3600000) fail('close did not finish within 60 minutes');
    sleep(2);
  }
  closeSeconds.add((Date.now() - started) / 1000);
}
