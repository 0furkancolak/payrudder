"use client";

import Link from "next/link";
import { RequireToken } from "@/components/RequireToken";
import { api } from "@/lib/api";
import { useApi } from "@/lib/useApi";

export default function TenantsPage() {
  return (
    <RequireToken>
      <Tenants />
    </RequireToken>
  );
}

function Tenants() {
  const { data, error, loading } = useApi(() => api.GET("/admin/v1/tenants", { params: { query: { page_size: 200 } } }), []);
  const tenants = data?.tenants ?? [];
  return (
    <>
      <h1>Tenants</h1>
      {loading && <p className="muted">Loading…</p>}
      {error && <p className="error">{error}</p>}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Environment</th>
            <th>Tier</th>
            <th>Currency</th>
            <th>Time zone</th>
            <th>Id</th>
          </tr>
        </thead>
        <tbody>
          {tenants.map((t) => (
            <tr key={t.id}>
              <td>
                <Link href={`/tenants/${t.id}`}>{t.name}</Link>
              </td>
              <td>
                <span className="badge">{(t.environment ?? "").replace("ENVIRONMENT_", "").toLowerCase()}</span>
              </td>
              <td>{(t.tier ?? "").replace("TENANT_TIER_", "").toLowerCase()}</td>
              <td>{t.currency}</td>
              <td>{t.timezone}</td>
              <td className="mono">{t.id}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {!loading && tenants.length === 0 && !error && <p className="muted">No tenants yet.</p>}
    </>
  );
}
