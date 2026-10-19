"use client";

import Link from "next/link";
import createClient from "openapi-fetch";
import { useEffect, useState, type FormEvent } from "react";
import { RequireToken } from "@/components/RequireToken";
import { api, errorMessage, type Invoice } from "@/lib/api";
import type { paths } from "@/lib/schema";

const CURRENCIES = ["CHF", "EUR", "GBP", "JPY", "KWD", "TRY", "USD"] as const;
const SCENARIOS = ["success", "declined", "insufficient_funds_once", "error_then_success", "timeout_committed", "timeout_not_committed"] as const;
type Scenario = (typeof SCENARIOS)[number];
type Currency = (typeof CURRENCIES)[number];

const CUSTOMER = "cus_playground";

/** A client that speaks as the sandbox tenant (its qk_test_ key), not as the operator. */
const asTenant = (key: string) => createClient<paths>({ baseUrl: "/gw", headers: { Authorization: `Bearer ${key}` } });

export default function PlaygroundPage() {
  return (
    <RequireToken>
      <Playground />
    </RequireToken>
  );
}

function Playground() {
  const [name, setName] = useState("");
  const [currency, setCurrency] = useState<Currency | "">("");
  const [timezone, setTimezone] = useState("");
  const [graceHours, setGraceHours] = useState("");
  const [tenantId, setTenantId] = useState<string | null>(null);
  const [key, setKey] = useState<string | null>(null);
  const [ready, setReady] = useState(false);
  const [scenario, setScenario] = useState<Scenario>("success");
  const [quantity, setQuantity] = useState("42");
  const [sent, setSent] = useState(0);
  const [closed, setClosed] = useState(false);
  const [invoices, setInvoices] = useState<Invoice[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [log, setLog] = useState<string[]>([]);

  const note = (line: string) => setLog((l) => [...l, line]);

  async function step<T>(label: string, run: () => Promise<{ data?: T; error?: unknown }>): Promise<T | null> {
    setBusy(true);
    setError(null);
    try {
      const r = await run();
      if (r.error !== undefined) {
        setError(`${label}: ${errorMessage(r.error)}`);
        return null;
      }
      note(label);
      return (r.data ?? ({} as T)) as T;
    } catch (e) {
      setError(`${label}: ${errorMessage(e)}`);
      return null;
    } finally {
      setBusy(false);
    }
  }

  async function createAccount(e: FormEvent) {
    e.preventDefault();
    if (currency === "") return;
    const acct = await step("Account created", () =>
      api.POST("/admin/v1/accounts", { body: { name, currency, timezone, grace_period: `${Number(graceHours) * 3600}s` } }),
    );
    const sandbox = acct?.tenants?.find((t) => t.environment === "ENVIRONMENT_SANDBOX");
    if (!sandbox?.id) return;
    const k = await step("Sandbox API key issued", () => api.POST("/admin/v1/tenants/{id}/api-keys", { params: { path: { id: sandbox.id! } } }));
    if (k?.plaintext) {
      setTenantId(sandbox.id);
      setKey(k.plaintext);
    }
  }

  async function setUp() {
    if (!key) return;
    const t = asTenant(key);
    const meter = await step("Meter api_calls (sum of n)", () =>
      t.POST("/v1/meters", { body: { code: "api_calls", aggregation: "AGGREGATION_SUM", value_property: "n", quantity_scale: 0, unit_label: "calls" } }),
    );
    if (!meter) return;
    if (!(await step("Plan starter", () => t.POST("/v1/plans", { body: { code: "starter", name: "Starter" } })))) return;
    if (
      !(await step("Plan version: 10.00 base fee + 0.01 per call", () =>
        t.POST("/v1/plans/{code}/versions", {
          params: { path: { code: "starter" } },
          body: { base_fee_minor: "1000", prices: [{ meter_code: "api_calls", per_unit: { unit_price: "0.01" } }] },
        }),
      ))
    )
      return;
    if (!(await step("Customer " + CUSTOMER, () => t.POST("/v1/customers", { body: { external_id: CUSTOMER, name: "Playground customer", email: "playground@example.com", payment_method: "pm_sandbox_success" } })))) return;
    if (!(await step("Subscription to starter", () => t.POST("/v1/subscriptions", { body: { customer_external_id: CUSTOMER, plan_code: "starter" } })))) return;
    setReady(true);
  }

  async function chooseScenario(next: Scenario) {
    setScenario(next);
    if (!key) return;
    await step(`Payment outcome: ${next}`, () => asTenant(key).POST("/v1/sandbox/customers/{external_id}/payment-scenario", { params: { path: { external_id: CUSTOMER } }, body: { scenario: next } }));
  }

  async function sendUsage() {
    if (!key) return;
    const n = Number(quantity);
    const r = await step(`Sent usage: ${n} calls`, () =>
      asTenant(key).POST("/v1/events", { body: { events: [{ customer_external_id: CUSTOMER, meter_code: "api_calls", quantity: String(n), event_time: new Date(Date.now() - 60_000).toISOString() }] } }),
    );
    if (r) setSent((s) => s + n);
  }

  async function closePeriod() {
    if (!key) return;
    const r = await step("Period closed (sandbox): billing runs now", () => asTenant(key).POST("/v1/sandbox/close-period", { body: {} }));
    if (r) setClosed(true);
  }

  // After the period is closed, follow the invoice through its saga.
  useEffect(() => {
    if (!closed || !key) return;
    let alive = true;
    const poll = async () => {
      const r = await asTenant(key).GET("/v1/invoices", {});
      if (alive && r.data?.invoices) setInvoices(r.data.invoices);
    };
    void poll();
    const id = window.setInterval(poll, 2000);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [closed, key]);

  return (
    <>
      <h1>Playground</h1>
      <p className="muted">
        Walk through the whole product in a sandbox: create an account, define what is metered and priced, send usage, close the period and watch the invoice go
        through its payment saga with a mock payment provider. Nothing here touches a live tenant.
      </p>
      {error && <p className="error" role="alert">{error}</p>}

      <h2>1. Account</h2>
      {tenantId === null ? (
        <form onSubmit={createAccount} className="row" aria-label="new account">
          <input aria-label="Account name" placeholder="Account name" required value={name} onChange={(e) => setName(e.target.value)} />
          <select aria-label="Currency" required value={currency} onChange={(e) => setCurrency(e.target.value as Currency)}>
            <option value="" disabled>Currency (cannot change later)</option>
            {CURRENCIES.map((c) => <option key={c}>{c}</option>)}
          </select>
          <input aria-label="Time zone" placeholder="IANA time zone, e.g. Europe/Istanbul" required value={timezone} onChange={(e) => setTimezone(e.target.value)} />
          <input aria-label="Grace period (hours)" placeholder="Grace period, hours (0 to 168)" required type="number" min={0} max={168} value={graceHours} onChange={(e) => setGraceHours(e.target.value)} />
          <button disabled={busy}>Create account</button>
        </form>
      ) : (
        <p className="ok">
          Sandbox tenant <Link href={`/tenants/${tenantId}`} className="mono">{tenantId}</Link> · key <code>{key?.slice(0, 16)}…</code>
        </p>
      )}

      <h2>2. What is metered and priced</h2>
      <div className="row">
        <button disabled={busy || key === null || ready} onClick={setUp}>Create meter, plan, customer and subscription</button>
        <label>
          Payment outcome{" "}
          <select aria-label="Payment outcome" disabled={!ready || busy} value={scenario} onChange={(e) => void chooseScenario(e.target.value as Scenario)}>
            {SCENARIOS.map((s) => <option key={s}>{s}</option>)}
          </select>
        </label>
      </div>

      <h2>3. Usage</h2>
      <div className="row">
        <input aria-label="Calls" type="number" min={1} value={quantity} onChange={(e) => setQuantity(e.target.value)} style={{ minWidth: 120 }} />
        <button disabled={busy || !ready || closed} onClick={sendUsage}>Send usage</button>
        <span className="muted" data-testid="sent">{sent} calls sent</span>
      </div>

      <h2>4. Close the period</h2>
      <div className="row">
        <button disabled={busy || !ready || sent === 0 || closed} onClick={closePeriod}>Close period now</button>
        <span className="muted">Billing runs as at month end; the sandbox just moves the cutoff to now.</span>
      </div>

      {closed && (
        <>
          <h2>5. Invoice</h2>
          {invoices.length === 0 && <p className="muted">Waiting for the billing run (about 40 seconds)…</p>}
          <table>
            <tbody>
              {invoices.map((inv) => (
                <tr key={inv.id} data-testid="playground-invoice">
                  <td>{inv.number}</td>
                  <td><span className="badge">{inv.saga_state}</span></td>
                  <td><span className="badge">{inv.status}</span></td>
                  <td className="num">{inv.total_minor}</td>
                  <td>{tenantId && inv.id && <Link href={`/tenants/${tenantId}/invoices/${inv.id}`}>Open</Link>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      {log.length > 0 && (
        <>
          <h2>Done so far</h2>
          <ul>{log.map((l, i) => <li key={i}>{l}</li>)}</ul>
        </>
      )}
    </>
  );
}
