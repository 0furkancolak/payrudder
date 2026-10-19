"use client";

import { useState } from "react";
import { RequireToken } from "@/components/RequireToken";
import { api, errorMessage } from "@/lib/api";
import { useApi } from "@/lib/useApi";

export default function DeadLettersPage() {
  return (
    <RequireToken>
      <DeadLetters />
    </RequireToken>
  );
}

function DeadLetters() {
  const [queue, setQueue] = useState<string>("");
  const [note, setNote] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const list = useApi(() => api.GET("/admin/v1/dead-letters", { params: { query: queue ? { queue, limit: 20 } : {} } }), [queue]);

  async function redrive(q: string, count: number) {
    if (!window.confirm(`Move ${count} dead letter(s) of ${q} back to ${q}? Their retry counters are reset.`)) return;
    setError(null);
    setNote(null);
    const r = await api.POST("/admin/v1/dead-letters/redrive", { body: { queue: q, limit: 100 } });
    if (r.error !== undefined) setError(errorMessage(r.error));
    else setNote(`Redriven ${r.data?.redriven ?? 0} message(s) from ${q}.dlq.`);
    list.reload();
  }

  return (
    <>
      <h1>Dead letters</h1>
      <p className="muted">RabbitMQ dead-letter queues ({`{queue}.dlq`}). A message lands here after its delivery limit; redrive sends it back with the retry counter reset.</p>
      {list.error && <p className="error">{list.error}</p>}
      {error && <p className="error">{error}</p>}
      {note && <p className="ok">{note}</p>}
      <table>
        <thead>
          <tr>
            <th>Queue</th>
            <th className="num">Messages</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {(list.data?.queues ?? []).map((q) => {
            const n = Number(q.messages ?? 0);
            return (
              <tr key={q.queue} data-testid={`dlq-${q.queue}`}>
                <td>
                  <button className="link" style={{ marginLeft: 0 }} onClick={() => setQueue(queue === q.queue ? "" : (q.queue ?? ""))}>
                    {q.queue}.dlq
                  </button>
                </td>
                <td className="num" data-testid="dlq-count">
                  {n}
                </td>
                <td>
                  <button className="danger" disabled={n === 0} onClick={() => redrive(q.queue ?? "", n)}>
                    Redrive
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>

      {queue && (
        <>
          <h2>Messages of {queue}.dlq</h2>
          <table>
            <thead>
              <tr>
                <th>Message id</th>
                <th className="num">Deliveries</th>
                <th>Reason</th>
                <th className="num">Bytes</th>
              </tr>
            </thead>
            <tbody>
              {(list.data?.messages ?? []).map((m, i) => (
                <tr key={`${m.message_id}-${i}`}>
                  <td className="mono">{m.message_id}</td>
                  <td className="num">{m.delivery_count}</td>
                  <td>{m.reason}</td>
                  <td className="num">{m.body_bytes}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  );
}
