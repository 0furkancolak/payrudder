"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useState } from "react";
import { RequireToken } from "@/components/RequireToken";
import { api, errorMessage } from "@/lib/api";
import { useApi } from "@/lib/useApi";

export default function InvoicePage() {
  return (
    <RequireToken>
      <InvoiceDetail />
    </RequireToken>
  );
}

function InvoiceDetail() {
  const { id, invoiceId } = useParams<{ id: string; invoiceId: string }>();
  const invoice = useApi(
    () => api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}", { params: { path: { tenant_id: id, invoice_id: invoiceId } } }),
    [id, invoiceId],
  );
  const saga = useApi(
    () => api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/saga", { params: { path: { tenant_id: id, invoice_id: invoiceId } } }),
    [id, invoiceId],
  );
  const [pdfError, setPdfError] = useState<string | null>(null);

  async function openPdf() {
    setPdfError(null);
    const r = await api.GET("/admin/v1/tenants/{tenant_id}/invoices/{invoice_id}/pdf", { params: { path: { tenant_id: id, invoice_id: invoiceId } } });
    if (r.error !== undefined || !r.data?.url) setPdfError(errorMessage(r.error ?? "no link"));
    else window.open(r.data.url, "_blank", "noopener");
  }

  const inv = invoice.data;
  const events = saga.data?.events ?? [];
  return (
    <>
      <p>
        <Link href={`/tenants/${id}`}>← Tenant</Link>
      </p>
      <h1>{inv?.number || "Invoice"}</h1>
      {invoice.error && <p className="error">{invoice.error}</p>}
      {inv && (
        <p className="muted">
          {inv.kind} · period {inv.period_id} · {inv.customer_external_id} · {inv.currency} {inv.total_minor} minor · {inv.status} · saga {inv.saga_state}
        </p>
      )}
      <div className="row">
        <button onClick={openPdf} disabled={!inv?.number}>
          Open PDF
        </button>
        {pdfError && <span className="error">{pdfError}</span>}
      </div>

      <h2>Lines</h2>
      <table>
        <thead>
          <tr>
            <th>#</th>
            <th>Kind</th>
            <th>Description</th>
            <th>Meter</th>
            <th className="num">Quantity</th>
            <th className="num">Amount (minor)</th>
          </tr>
        </thead>
        <tbody>
          {(inv?.lines ?? []).map((l) => (
            <tr key={l.line_no}>
              <td>{l.line_no}</td>
              <td>{l.kind}</td>
              <td>{l.description}</td>
              <td>{l.meter_code}</td>
              <td className="num">{l.quantity}</td>
              <td className="num">{l.amount_minor}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2>Saga timeline</h2>
      {saga.error && <p className="error">{saga.error}</p>}
      <table aria-label="Saga timeline">
        <thead>
          <tr>
            <th className="num">Seq</th>
            <th>From</th>
            <th>To</th>
            <th>Cause</th>
            <th>At</th>
          </tr>
        </thead>
        <tbody>
          {events.map((e) => (
            <tr key={e.seq} data-testid="saga-event">
              <td className="num">{e.seq}</td>
              <td>{e.from_state}</td>
              <td>{e.to_state}</td>
              <td className="mono">{e.cause}</td>
              <td className="mono">{e.at}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
