"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useState, type FormEvent } from "react";
import { RequireToken } from "@/components/RequireToken";
import { api, errorMessage } from "@/lib/api";
import { useApi } from "@/lib/useApi";

export default function TenantPage() {
  return (
    <RequireToken>
      <Tenant />
    </RequireToken>
  );
}

function Tenant() {
  const { id } = useParams<{ id: string }>();
  const tenant = useApi(() => api.GET("/admin/v1/tenants/{id}", { params: { path: { id } } }), [id]);
  const invoices = useApi(
    () => api.GET("/admin/v1/tenants/{tenant_id}/invoices", { params: { path: { tenant_id: id }, query: { page_size: 100 } } }),
    [id],
  );
  const t = tenant.data;
  return (
    <>
      <h1>{t?.name ?? "Tenant"}</h1>
      {tenant.error && <p className="error">{tenant.error}</p>}
      {t && (
        <p className="muted">
          <span className="badge">{(t.environment ?? "").replace("ENVIRONMENT_", "").toLowerCase()}</span> {t.currency} · {t.timezone} · grace {t.grace_period ?? "?"} ·{" "}
          <span className="mono">{t.id}</span>
        </p>
      )}

      <h2>Invoices</h2>
      {invoices.error && <p className="error">{invoices.error}</p>}
      <table>
        <thead>
          <tr>
            <th>Number</th>
            <th>Kind</th>
            <th>Period</th>
            <th>Customer</th>
            <th className="num">Total (minor)</th>
            <th>Status</th>
            <th>Saga</th>
          </tr>
        </thead>
        <tbody>
          {(invoices.data?.invoices ?? []).map((inv) => (
            <tr key={inv.id}>
              <td>
                <Link href={`/tenants/${id}/invoices/${inv.id}`}>{inv.number || "(draft)"}</Link>
              </td>
              <td>{inv.kind}</td>
              <td>{inv.period_id}</td>
              <td>{inv.customer_external_id}</td>
              <td className="num">{inv.total_minor}</td>
              <td>{inv.status}</td>
              <td>{inv.saga_state}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {!invoices.loading && (invoices.data?.invoices ?? []).length === 0 && !invoices.error && <p className="muted">No invoices.</p>}

      <h2>Replay and re-rating jobs</h2>
      <p className="muted">Look a job up by id (start them with quotifyctl or the admin API).</p>
      <JobLookup tenantId={id} />
    </>
  );
}

function JobLookup({ tenantId }: { tenantId: string }) {
  const [id, setId] = useState("");
  const [out, setOut] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function look(kind: "replay" | "rerating", e: FormEvent) {
    e.preventDefault();
    setError(null);
    setOut(null);
    const path = kind === "replay" ? "/admin/v1/tenants/{tenant_id}/replays/{job_id}" : "/admin/v1/tenants/{tenant_id}/rerating-jobs/{job_id}";
    const r = await api.GET(path, { params: { path: { tenant_id: tenantId, job_id: id.trim() } } });
    if (r.error !== undefined) setError(errorMessage(r.error));
    else setOut(JSON.stringify(r.data, null, 2));
  }

  return (
    <>
      <form className="row" onSubmit={(e) => look("replay", e)}>
        <input aria-label="Job id" placeholder="Job id" value={id} onChange={(e) => setId(e.target.value)} />
        <button type="submit" disabled={id.trim() === ""}>
          Replay job
        </button>
        <button type="button" disabled={id.trim() === ""} onClick={(e) => look("rerating", e as unknown as FormEvent)}>
          Re-rating job
        </button>
      </form>
      {error && <p className="error">{error}</p>}
      {out && <pre className="mono">{out}</pre>}
    </>
  );
}
