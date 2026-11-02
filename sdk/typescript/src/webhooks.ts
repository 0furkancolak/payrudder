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
  PaymentDisputed: "payment.disputed",
  CreditsPurchased: "credits.purchased",
  CreditsLow: "credits.low",
  CustomerBlocked: "customer.blocked",
  CustomerUnblocked: "customer.unblocked",
  PriceChangeScheduled: "price_change.scheduled",
  ContractAccepted: "contract.accepted",
  ContractVersionPublished: "contract.version_published",
  ContractWithdrawalRequested: "contract.withdrawal_requested",
  ContractWithdrawalDecided: "contract.withdrawal_decided",
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

/** `payment.disputed`: one step of a case a customer opened with their bank about a payment (a case that opens and is later won or lost is two or three events). */
export interface PaymentDisputedEventData {
  invoice_id: string;
  invoice_number: string;
  customer_external_id: string;
  provider: string;
  /** The provider's reference of the case, the same in every event about it. */
  dispute_id: string;
  payment_ref: string;
  amount_minor: string;
  currency: string;
  /** Where the case stands. */
  outcome: "open" | "won" | "lost";
  /** `true` for a chargeback, which takes the money back; `false` for an inquiry or a warning ahead of one. */
  formal: boolean;
  /** The cardholder's reason in the provider's words; may be empty. */
  reason: string;
  provider_status: string;
  /** RFC 3339; set when the provider gives a date. */
  evidence_due_time?: string;
}

/** `customer.blocked` and `customer.unblocked`. */
export interface CustomerBlockEventData {
  customer_id: string;
  customer_external_id?: string;
  /** `customer.blocked`: fraud, chargeback, abuse, request, other, or empty. */
  category?: string;
  /** `customer.blocked`: the seller's own note. */
  reason?: string;
  /** `customer.blocked`: when the block ends by itself (RFC 3339); absent: until lifted. */
  blocked_until?: string;
  /** `customer.blocked`: the block also suspended the customer's service (entitlement checks answer not allowed). Absent: false. */
  suspend_service?: boolean;
}

/** A price change in an event: as the API returns it, without the fields at their zero value. Times are RFC 3339. */
export interface WebhookPriceChange {
  id: string;
  plan_code: string;
  from_version: number;
  to_version: number;
  notice_days: number;
  /** The first date a customer's price changes. */
  effective_time: string;
  /** The date the seller chose the new price to start from; absent when each subscription moves on its next renewal. */
  effective_from?: string;
  state: "open" | "withdrawn";
  created_by?: string;
  create_time: string;
  withdraw_time?: string;
  withdrawn_by?: string;
  /** The subscriptions waiting, moved, withdrawn and skipped. */
  scheduled?: number;
  applied?: number;
  withdrawn?: number;
  skipped?: number;
}

/** `price_change.scheduled`: a run of a price change scheduled at least one subscription. `scheduled` and `skipped` are what that run did. */
export interface PriceChangeScheduledEventData {
  price_change: WebhookPriceChange;
  scheduled: number;
  skipped: number;
}

/** `contract.accepted`: a customer accepted one or more agreements together (one event for the group). */
export interface ContractAcceptedEventData {
  group_id: string;
  source: "hosted" | "api";
  accepted_at: string;
  snapshot_sha256: string;
  /** Absent for a guest payment. */
  customer_id?: string;
  customer_external_id?: string;
  immediate_start_consent: boolean;
  withdrawal_waiver_acknowledged: boolean;
  context: { kind: "hosted_invoice" | "api"; ref: string };
  items: {
    acceptance_id: string;
    kind: "distance_sales" | "pre_info" | "tos" | "privacy";
    audience: "consumer" | "business";
    version_id: string;
    version: number;
    sha256: string;
    locale: string;
  }[];
}

/** `contract.version_published`: a new version of an agreement is the current one. */
export interface ContractVersionPublishedEventData {
  version_id: string;
  kind: "distance_sales" | "pre_info" | "tos" | "privacy";
  audience: "consumer" | "business";
  number: number;
  material: boolean;
  sha256: string;
  locales: string[];
  published_at: string;
}

/** `contract.withdrawal_requested`: a customer's withdrawal statement was recorded. */
export interface ContractWithdrawalRequestedEventData {
  withdrawal_id: string;
  customer_id: string;
  customer_external_id: string;
  subscription_id: string;
  invoice_id: string;
  source: "api" | "console" | "hosted";
  received_at: string;
}

/** `contract.withdrawal_decided`: the seller accepted or rejected a withdrawal, or marked an accepted one refunded. */
export interface ContractWithdrawalDecidedEventData {
  withdrawal_id: string;
  customer_id: string;
  customer_external_id: string;
  subscription_id: string;
  invoice_id: string;
  status: "accepted" | "rejected" | "refunded";
  decided_by: string;
  decided_at: string;
  note: string;
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
type CustomerTypes = "customer.blocked" | "customer.unblocked";

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
  | Envelope<"payment.disputed", PaymentDisputedEventData>
  | Envelope<"credits.purchased", CreditsPurchasedEventData>
  | Envelope<"credits.low", CreditsLowEventData>
  | Distribute<CustomerTypes, CustomerBlockEventData>
  | Envelope<"price_change.scheduled", PriceChangeScheduledEventData>
  | Envelope<"contract.accepted", ContractAcceptedEventData>
  | Envelope<"contract.version_published", ContractVersionPublishedEventData>
  | Envelope<"contract.withdrawal_requested", ContractWithdrawalRequestedEventData>
  | Envelope<"contract.withdrawal_decided", ContractWithdrawalDecidedEventData>
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
