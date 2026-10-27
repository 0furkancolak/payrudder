import { afterEach, describe, expect, it } from "vitest";
import { MAX_BATCH_BYTES, MAX_EVENTS_PER_BATCH, PermissionError, type IngestEvent } from "../src/index";
import { apiError, clientFor, json, startServer, type FakeServer, type Recorded } from "./helpers";

let server: FakeServer | undefined;
afterEach(async () => {
  await server?.close();
  server = undefined;
});

interface Sent {
  events: Array<{ idempotency_key: string; quantity: string; event_time: string; properties?: Record<string, string> }>;
}
const sent = (r: Recorded | undefined): Sent => JSON.parse(r?.body ?? "{}") as Sent;

function accept(req: Recorded, res: Parameters<typeof json>[0]) {
  const { events } = sent(req);
  json(res, 202, { results: events.map((e, i) => ({ status: "accepted", event_id: `ev_${e.idempotency_key}_${i}` })) });
}

const ev = (i: number, extra: Partial<IngestEvent> = {}): IngestEvent => ({
  customer_external_id: "cus_1",
  meter_code: "tokens",
  quantity: String(i),
  event_time: "2026-11-03T10:15:00Z",
  ...extra,
});

describe("events.ingest", () => {
  it("sends one valid batch and returns results in order", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    const res = await client.events.ingest([ev(1), ev(2), ev(3)]);
    expect(res).toHaveLength(3);
    expect(server.requests).toHaveLength(1);
    expect(res.map((r) => r.status)).toEqual(["accepted", "accepted", "accepted"]);
  });

  it("returns [] without a request for no events", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    expect(await client.events.ingest([])).toEqual([]);
    expect(server.requests).toHaveLength(0);
  });

  it("splits more than 500 events into batches of at most 500, keeping order", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    const input = Array.from({ length: 1203 }, (_, i) => ev(i, { idempotency_key: `k${i}` }));
    const res = await client.events.ingest(input);
    expect(res).toHaveLength(1203);
    expect(server.requests.map((r) => sent(r).events.length)).toEqual([MAX_EVENTS_PER_BATCH, MAX_EVENTS_PER_BATCH, 203]);
    const order = server.requests.flatMap((r) => sent(r).events.map((e) => e.idempotency_key));
    expect(order).toEqual(input.map((e) => e.idempotency_key));
    expect(res[1202]?.event_id).toBe("ev_k1202_202");
  });

  it("splits by size so no batch exceeds 1 MiB", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    const pad = "x".repeat(30_000); // 30 events ~ 0.9 MB, 40 ~ 1.2 MB
    const input = Array.from({ length: 100 }, (_, i) => ev(i, { idempotency_key: `k${i}`, properties: { pad } }));
    const res = await client.events.ingest(input);
    expect(res).toHaveLength(100);
    expect(server.requests.length).toBeGreaterThan(2);
    for (const r of server.requests) expect(Buffer.byteLength(r.body)).toBeLessThanOrEqual(MAX_BATCH_BYTES);
    expect(server.requests.reduce((n, r) => n + sent(r).events.length, 0)).toBe(100);
  });

  it("fills missing idempotency keys, uniquely, and keeps given ones", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    await client.events.ingest([ev(1), ev(2), ev(3, { idempotency_key: "mine" })]);
    const keys = sent(server.requests[0]).events.map((e) => e.idempotency_key);
    expect(keys[2]).toBe("mine");
    expect(keys[0]).toMatch(/^sdk_[0-9a-f-]{36}$/);
    expect(new Set(keys).size).toBe(3);
  });

  it("keeps generated keys stable across the automatic retries of a batch", async () => {
    server = await startServer((req, res, n) => {
      if (n < 2) apiError(res, 500, "INTERNAL", "common.internal"); // a 500 is retried because every event is keyed
      else accept(req, res);
    });
    const { client } = clientFor(server);
    await client.events.ingest([ev(1), ev(2)]);
    expect(server.requests).toHaveLength(3);
    const bodies = server.requests.map((r) => r.body);
    expect(new Set(bodies).size).toBe(1);
  });

  it("returns rejected and duplicate events as results without throwing", async () => {
    server = await startServer((_req, res) =>
      json(res, 202, {
        results: [
          { status: "accepted", event_id: "e1" },
          { status: "rejected", error_code: "UNKNOWN_METER", error_message: "no such meter" },
          { status: "duplicate", event_id: "e0" },
        ],
      }),
    );
    const { client } = clientFor(server);
    const res = await client.events.ingest([ev(1), ev(2), ev(3)]);
    expect(res.map((r) => r.status)).toEqual(["accepted", "rejected", "duplicate"]);
    expect(res[1]?.error_code).toBe("UNKNOWN_METER");
  });

  it("serialises Date times and bigint quantities exactly", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    await client.events.ingest([ev(1, { quantity: 9007199254740993n, event_time: new Date("2026-11-03T10:15:00Z") })]);
    const e = sent(server.requests[0]).events[0];
    expect(e?.quantity).toBe("9007199254740993");
    expect(e?.event_time).toBe("2026-11-03T10:15:00.000Z");
  });

  it("throws when a whole batch fails", async () => {
    server = await startServer((_req, res) => apiError(res, 403, "PERMISSION_DENIED", "gateway.auth.permission_denied"));
    const { client } = clientFor(server);
    await expect(client.events.ingest([ev(1)])).rejects.toBeInstanceOf(PermissionError);
  });

  it("still exposes the raw operation", async () => {
    server = await startServer(accept);
    const { client } = clientFor(server);
    const res = await client.events.ingestEvents({ events: [{ customer_external_id: "c", meter_code: "m", quantity: "1", event_time: "2026-11-03T10:15:00Z" }] });
    expect(res.results).toHaveLength(1);
  });
});
