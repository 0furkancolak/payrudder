// Shared helpers of the k6 scenarios (docs/09-testing-strategy.md#load-tests-k6).
import http from 'k6/http';
import { check, fail, sleep } from 'k6';

export const GATEWAY = __ENV.GATEWAY_URL || 'http://localhost:42800';
export const OPERATOR = __ENV.QUOTIFY_OPERATOR_TOKEN || 'local-operator-token-change-me-0123456789';
const JSON_HEADERS = { 'Content-Type': 'application/json' };

function put(path, token, body, expect) {
  const res = http.put(`${GATEWAY}${path}`, JSON.stringify(body), {
    headers: { ...JSON_HEADERS, Authorization: `Bearer ${token}` }, tags: { name: 'setup' },
  });
  if (res.status !== expect) fail(`PUT ${path}: ${res.status} ${res.body}`);
  return res.json();
}

function post(path, token, body, expect) {
  const res = http.post(`${GATEWAY}${path}`, JSON.stringify(body), {
    headers: { ...JSON_HEADERS, Authorization: `Bearer ${token}` }, tags: { name: 'setup' },
  });
  if (res.status !== expect) fail(`POST ${path}: ${res.status} ${res.body}`);
  return res.json();
}

// createTenant makes an account and returns its live tenant's id and API key (the sandbox tenant is ignored).
export function createTenant(name, tier, { sandbox = false } = {}) {
  const acct = post('/admin/v1/accounts', OPERATOR, {
    name, currency: 'TRY', timezone: 'Europe/Istanbul', grace_period: '86400s', tier,
  }, 201);
  const want = sandbox ? 'ENVIRONMENT_SANDBOX' : 'ENVIRONMENT_LIVE';
  const tn = acct.tenants.find((t) => t.environment === want);
  // A live tenant is created inactive and has no keys: activate it the way support does, with a reason.
  if (!sandbox) put(`/admin/v1/tenants/${tn.id}/status`, OPERATOR, { status: 'active', reason: 'k6 setup' }, 200);
  const key = post(`/admin/v1/tenants/${tn.id}/api-keys`, OPERATOR, {}, 201).plaintext;
  return { id: tn.id, key, name };
}

export function createMeter(t, code, aggregation = 'AGGREGATION_COUNT') {
  const body = { code, aggregation, quantity_scale: 0, unit_label: 'u' };
  if (aggregation !== 'AGGREGATION_COUNT') body.value_property = 'v';
  return post('/v1/meters', t.key, body, 201);
}

// batchRetry sends requests in parallel and retries the ones refused with 429 after Retry-After (low tiers rate-limit
// writes: free 5/s). Anything else but expect is a failure.
export function batchRetry(reqs, expect) {
  for (let attempt = 0; reqs.length > 0; attempt++) {
    if (attempt > 120) fail('still rate limited after 120 attempts');
    const res = http.batch(reqs);
    const retry = [];
    let wait = 0;
    res.forEach((r, k) => {
      if (r.status === expect) return;
      if (r.status === 429) {
        retry.push(reqs[k]);
        wait = Math.max(wait, Number(r.headers['Retry-After'] || 1));
      } else fail(`${reqs[k][0]} ${reqs[k][1]}: ${r.status} ${r.body}`);
    });
    reqs = retry;
    if (wait > 0) sleep(wait);
  }
}

export function createCustomers(t, n, prefix = 'cus') {
  const ids = [];
  const per = 50;
  for (let i = 0; i < n; i += per) {
    const reqs = [];
    for (let j = i; j < Math.min(n, i + per); j++) {
      const ext = `${prefix}_${j}`;
      ids.push(ext);
      reqs.push(['POST', `${GATEWAY}/v1/customers`, JSON.stringify({ external_id: ext, name: ext, email: `${ext}@example.com` }),
        { headers: { ...JSON_HEADERS, Authorization: `Bearer ${t.key}` }, tags: { name: 'setup' } }]);
    }
    batchRetry(reqs, 201);
  }
  return ids;
}

let seq = 0;
// eventsBody builds a batch of n events for random customers of a tenant. Keys are unique per VU and iteration.
export function eventsBody(customers, meters, n) {
  const events = [];
  const now = new Date(Date.now() - 1000).toISOString();
  for (let i = 0; i < n; i++) {
    events.push({
      idempotency_key: `k6-${__VU}-${__ITER}-${seq++}-${i}`,
      customer_external_id: customers[Math.floor(Math.random() * customers.length)],
      meter_code: meters[Math.floor(Math.random() * meters.length)],
      quantity: '1',
      event_time: now,
    });
  }
  return JSON.stringify({ events });
}

export function postEvents(t, body, extraTags = {}) {
  const res = http.post(`${GATEWAY}/v1/events`, body, {
    headers: { ...JSON_HEADERS, Authorization: `Bearer ${t.key}` }, tags: { name: 'POST /v1/events', ...extraTags },
  });
  return res;
}

export function accepted(res) {
  return check(res, { 'ingest answered 202': (r) => r.status === 202 });
}

export function envNum(name, def) {
  const v = __ENV[name];
  return v === undefined || v === '' ? def : Number(v);
}
