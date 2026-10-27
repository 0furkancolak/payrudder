import { EventsResourceBase } from "./generated/resources";
import type { EventResult, IncomingEvent } from "./generated/types";
import type { RequestOptions, Transport } from "./transport";
import { stringifyBody } from "./transport";

/** Server limits of one `POST /v1/events`. */
export const MAX_EVENTS_PER_BATCH = 500;
export const MAX_BATCH_BYTES = 1024 * 1024;

/** An event as the helper accepts it: a `Date` for the time, a `bigint` for the quantity, and an optional idempotency key. */
export type IngestEvent = Omit<IncomingEvent, "quantity" | "event_time" | "idempotency_key"> & {
  /** Generated when missing (live keys require one). Give your own to make a re-submission after a crash a no-op. */
  idempotency_key?: string;
  /** Exact decimal as a string (`"1.250"`) or a bigint. Never a float. */
  quantity: string | bigint;
  event_time: string | Date;
};

export class EventsResource extends EventsResourceBase {
  constructor(private readonly transport: Transport) {
    super(transport);
  }

  /**
   * Sends any number of events, split into valid batches (at most 500 events and 1 MiB each), and returns one result per
   * event in input order. A rejected event is a result with `status: "rejected"` and an `error_code`, not an exception;
   * only a failure of a whole batch (auth, 413, 429 after the retries, 5xx, network) throws.
   *
   * Events without an `idempotency_key` get a generated one before the first attempt, so the automatic retries of a call
   * can never double-count. If a later batch fails after earlier ones were accepted, the thrown error does not carry those
   * results: supply your own keys when you need to re-submit the whole input safely.
   */
  async ingest(events: readonly IngestEvent[], options?: RequestOptions): Promise<EventResult[]> {
    const encoder = new TextEncoder();
    const prepared = events.map((e) => {
      const wire = {
        ...e,
        quantity: typeof e.quantity === "bigint" ? e.quantity.toString() : e.quantity,
        event_time: e.event_time instanceof Date ? e.event_time.toISOString() : e.event_time,
        idempotency_key: e.idempotency_key ?? `sdk_${globalThis.crypto.randomUUID()}`,
      } satisfies IncomingEvent;
      return { wire, bytes: encoder.encode(stringifyBody(wire)).length };
    });

    const batches: IncomingEvent[][] = [];
    let current: IncomingEvent[] = [];
    let size = 0;
    const envelope = '{"events":[]}'.length;
    for (const { wire, bytes } of prepared) {
      const added = bytes + (current.length > 0 ? 1 : 0); // the comma
      if (current.length > 0 && (current.length >= MAX_EVENTS_PER_BATCH || envelope + size + added > MAX_BATCH_BYTES)) {
        batches.push(current);
        current = [];
        size = 0;
      }
      size += bytes + (current.length > 0 ? 1 : 0);
      current.push(wire);
    }
    if (current.length > 0) batches.push(current);

    const results: EventResult[] = [];
    for (const batch of batches) {
      // Every event carries a key, so replaying the batch after a 500 cannot double-count.
      const res = await this.transport.request<{ results?: EventResult[] }>(
        { method: "POST", path: "/v1/events", body: { events: batch }, bodyKind: "json", response: "json", idempotent: true },
        options,
      );
      results.push(...(res.results ?? []));
    }
    return results;
  }
}
