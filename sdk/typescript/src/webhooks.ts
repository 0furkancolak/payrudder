import type { ChangeQuote, Subscription } from "./generated/types";
import type { Int64 } from "./int64";

/** Event type names (docs/16-webhooks.md). `ping` is what `sendTestWebhook` delivers. */
export const WebhookEventTypes = {
  InvoiceFinalized: "invoice.finalized",
  InvoicePaid: "invoice.paid",
  InvoiceVoided: "invoice.voided",
  InvoiceUncollectible: "invoice.uncollectible",
  CreditNoteCreated: "credit_note.created",
  CreditNoteRefunded: "credit_note.refunded",
  CreditNoteRefundFailed: "credit_note.refund_failed",
  SubscriptionCreated: "subscription.created",
  SubscriptionChanged: "subscription.changed",
  SubscriptionChangeScheduled: "subscription.change_scheduled",
  SubscriptionCancelled: "subscription.cancelled",
  SubscriptionCancellationScheduled: "subscription.cancellation_scheduled",
  SubscriptionCancellationWithdrawn: "subscription.cancellation_withdrawn",
  SubscriptionPaused: "subscription.paused",
  SubscriptionResumed: "subscription.resumed",
  SubscriptionMovedToProvider: "subscription.moved_to_provider",
  SubscriptionTrialWillEnd: "subscription.trial_will_end",
  PaymentLinkCreated: "payment_link.created",
  PaymentLinkPaid: "payment_link.paid",
  PaymentLinkExpired: "payment_link.expired",
  CreditsPurchased: "credits.purchased",
  CreditsLow: "credits.low",
  Ping: "ping",
} as const;

export type WebhookEventType = (typeof WebhookEventTypes)[keyof typeof WebhookEventTypes];

export interface InvoiceEventData {
  invoice_id: string;
  number?: string;
  customer_id?: string;
  customer_external_id?: string;
  period_id?: string;
  currency?: string;
  total_minor: Int64;
  status?: string;
  environment?: string;
  /** `invoice.paid` only. */
  tax_minor?: Int64;
  /** `invoice.paid` only: the total less its TAX lines. */
  net_minor?: Int64;
}

export interface CreditNoteEventData {
  credit_note_id: string;
  number?: string;
  invoice_id?: string;
  invoice_number?: string;
  customer_id?: string;
  customer_external_id?: string;
  currency?: string;
  total_minor: Int64;
  reason?: string;
  disposition?: "credit" | "refund";
  refund_state?: string;
  refund_error?: string;
}

export interface SubscriptionEventData {
  subscription: Subscription;
  previous_subscription_id?: string;
  quote?: ChangeQuote;
  to_provider?: string;
  credited_minor?: Int64;
}

export interface PaymentLinkEventData {
  link_id: string;
  kind?: string;
  invoice_id?: string;
  invoice_number?: string;
  amount_minor?: Int64;
  total_minor?: Int64;
  currency?: string;
  description?: string;
  customer_external_id?: string;
  single_use?: boolean;
  expires_at?: string;
  paid_count?: number;
}

export interface CreditsPurchasedEventData {
  customer_id: string;
  customer_external_id?: string;
  credits: string;
  bonus_credits?: string;
  quantity?: number;
  balance: string;
  pack_id?: string;
  pack_name?: string;
  link_id?: string;
  invoice_id?: string;
  invoice_number?: string;
  currency?: string;
  amount_minor?: Int64;
  expires_at?: string;
}

export interface CreditsLowEventData {
  customer_id: string;
  customer_external_id?: string;
  balance: string;
  threshold: string;
}

interface Envelope<T extends WebhookEventType, D> {
  id: string;
  type: T;
  /** RFC 3339 time of the occurrence. */
  created: string;
  tenant_id: string;
  data: D;
}

type InvoiceTypes = "invoice.finalized" | "invoice.paid" | "invoice.voided" | "invoice.uncollectible";
type CreditNoteTypes = "credit_note.created" | "credit_note.refunded" | "credit_note.refund_failed";
type SubscriptionTypes =
  | "subscription.created"
  | "subscription.changed"
  | "subscription.change_scheduled"
  | "subscription.cancelled"
  | "subscription.cancellation_scheduled"
  | "subscription.cancellation_withdrawn"
  | "subscription.paused"
  | "subscription.resumed"
  | "subscription.moved_to_provider"
  | "subscription.trial_will_end";
type PaymentLinkTypes = "payment_link.created" | "payment_link.paid" | "payment_link.expired";

type Distribute<T extends WebhookEventType, D> = T extends WebhookEventType ? Envelope<T, D> : never;

/**
 * A verified delivery. Narrow on `type`. Events are delivered at least once and not in order: dedupe on `id`, and fetch the
 * object from the API when you need its present state. New event types may appear before this SDK knows them.
 */
export type WebhookEvent =
  | Distribute<InvoiceTypes, InvoiceEventData>
  | Distribute<CreditNoteTypes, CreditNoteEventData>
  | Distribute<SubscriptionTypes, SubscriptionEventData>
  | Distribute<PaymentLinkTypes, PaymentLinkEventData>
  | Envelope<"credits.purchased", CreditsPurchasedEventData>
  | Envelope<"credits.low", CreditsLowEventData>
  | Envelope<"ping", Record<string, unknown>>;

export type WebhookFailureReason = "invalid_header" | "timestamp_outside_tolerance" | "no_matching_signature" | "invalid_payload";

export class WebhookVerificationError extends Error {
  readonly reason: WebhookFailureReason;
  constructor(reason: WebhookFailureReason, message: string) {
    super(message);
    this.name = "WebhookVerificationError";
    this.reason = reason;
  }
}

export const SIGNATURE_HEADER = "Payrudder-Signature";
export const DEFAULT_TOLERANCE_SECONDS = 300;

/** Raw bytes of the request body. Pass the exact bytes received: a re-serialised JSON body will not verify. */
export type WebhookPayload = string | Uint8Array | ArrayBuffer;

function toBytes(payload: WebhookPayload): Uint8Array {
  if (typeof payload === "string") return new TextEncoder().encode(payload);
  if (payload instanceof Uint8Array) return payload;
  return new Uint8Array(payload);
}

function hexToBytes(hex: string): Uint8Array<ArrayBuffer> | undefined {
  if (hex.length === 0 || hex.length % 2 !== 0 || !/^[0-9a-fA-F]+$/.test(hex)) return undefined;
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}

interface ParsedHeader {
  timestamp: number;
  signatures: string[];
}

function parseHeader(header: string): ParsedHeader {
  let timestamp: number | undefined;
  const signatures: string[] = [];
  for (const part of header.split(",")) {
    const eq = part.indexOf("=");
    if (eq < 0) continue;
    const k = part.slice(0, eq).trim();
    const v = part.slice(eq + 1).trim();
    if (k === "t" && /^\d+$/.test(v)) timestamp = Number(v);
    else if (k === "v1") signatures.push(v);
  }
  if (timestamp === undefined || signatures.length === 0) {
    throw new WebhookVerificationError("invalid_header", `Malformed ${SIGNATURE_HEADER} header`);
  }
  return { timestamp, signatures };
}

/**
 * Verifies a delivery with WebCrypto (`crypto.subtle`), so it runs unchanged in Node 20+, browsers and edge runtimes.
 * Resolves when one of the header's `v1` signatures (two while a rotated secret is still accepted) matches
 * `HMAC-SHA256(secret, "<t>.<payload>")` and `t` is within `tolerance` seconds of now; rejects with
 * {@link WebhookVerificationError} otherwise. The comparison is done by `subtle.verify`, which is constant-time.
 *
 * @param now Test hook: the current time in seconds (default: the real clock).
 */
export async function verifyWebhook(
  payload: WebhookPayload,
  header: string,
  secret: string,
  tolerance: number = DEFAULT_TOLERANCE_SECONDS,
  now: number = Date.now() / 1000,
): Promise<void> {
  const { timestamp, signatures } = parseHeader(header);
  if (Math.abs(now - timestamp) > tolerance) {
    throw new WebhookVerificationError("timestamp_outside_tolerance", "Timestamp outside the tolerance zone");
  }
  const body = toBytes(payload);
  const prefix = new TextEncoder().encode(`${timestamp}.`);
  const signed = new Uint8Array(prefix.length + body.length);
  signed.set(prefix);
  signed.set(body, prefix.length);

  const key = await globalThis.crypto.subtle.importKey("raw", new TextEncoder().encode(secret), { name: "HMAC", hash: "SHA-256" }, false, [
    "verify",
  ]);
  for (const sig of signatures) {
    const bytes = hexToBytes(sig);
    if (bytes && (await globalThis.crypto.subtle.verify("HMAC", key, bytes, signed))) return;
  }
  throw new WebhookVerificationError("no_matching_signature", "No signature matches the payload");
}

/** Verifies like {@link verifyWebhook}, then parses the body into the typed envelope. */
export async function constructEvent(
  payload: WebhookPayload,
  header: string,
  secret: string,
  tolerance: number = DEFAULT_TOLERANCE_SECONDS,
  now: number = Date.now() / 1000,
): Promise<WebhookEvent> {
  await verifyWebhook(payload, header, secret, tolerance, now);
  let parsed: unknown;
  try {
    parsed = JSON.parse(typeof payload === "string" ? payload : new TextDecoder().decode(toBytes(payload)));
  } catch {
    throw new WebhookVerificationError("invalid_payload", "Payload is not valid JSON");
  }
  const e = parsed as Partial<WebhookEvent> | null;
  if (typeof e !== "object" || e === null || typeof e.id !== "string" || typeof e.type !== "string") {
    throw new WebhookVerificationError("invalid_payload", "Payload is not a webhook event");
  }
  return e as WebhookEvent;
}
