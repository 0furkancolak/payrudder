/**
 * The two switches that keep an assistant away from what the person who starts the server did not decide to give it (both off by default):
 *   PAYRUDDER_MCP_ALLOW_PERSONAL_DATA=1  customers' names, e-mail addresses, phone numbers, tax ids and addresses reach the model; otherwise they are replaced by REDACTED
 *   PAYRUDDER_MCP_ALLOW_LIVE=1           a live key (qk_live_...) is accepted; otherwise it is refused, so an assistant can only touch the sandbox
 * Which keys hold personal data is classified in scripts/read-tools.config.mjs (PERSONAL), next to the untrusted ones.
 */
export const REDACTED = "[redacted: personal data]";
export const NOTICE =
  "Personal data (customers' names, e-mail addresses, phone numbers, tax ids and addresses) is redacted in this answer and shown as " +
  `"${REDACTED}". Work with the external ids. The person who runs this server can allow it with PAYRUDDER_MCP_ALLOW_PERSONAL_DATA=1.`;

/** Beside one of these, a `name` is a person's (a customer), not the seller's own (a plan, a meter, a coupon, a tax rate). */
const PERSON_BESIDE = ["email", "external_id"];

/**
 * The value with every string a person owns replaced: the PERSONAL keys wherever they are (an object or list under one, all the way down), a `name` beside an `email` or an
 * `external_id`, and everything under an `address` except its country. Empty strings and other types are left (nothing to hide, and a "redacted" field would then
 * mislead). `count` is how many were replaced.
 */
export function redactPersonal(value: unknown, keys: ReadonlySet<string>, hide = false, inAddress = false): { value: unknown; count: number } {
  if (typeof value === "string") return hide && value !== "" ? { value: REDACTED, count: 1 } : { value, count: 0 };
  const sum = (rs: { count: number }[]) => rs.reduce((n, r) => n + r.count, 0);
  if (Array.isArray(value)) {
    const rs = value.map((v) => redactPersonal(v, keys, hide, inAddress));
    return { value: rs.map((r) => r.value), count: sum(rs) };
  }
  if (value && typeof value === "object") {
    const o = value as Record<string, unknown>;
    const person = PERSON_BESIDE.some((k) => k in o);
    const rs = Object.entries(o).map(([k, v]) => [k, redactPersonal(v, keys, hide || keys.has(k) || (k === "name" && person) || (inAddress && k !== "country"), inAddress || k === "address")] as const);
    return { value: Object.fromEntries(rs.map(([k, r]) => [k, r.value])), count: sum(rs.map(([, r]) => r)) };
  }
  return { value, count: 0 };
}

/** Why a key may not be used, or undefined when it may. A live key is refused unless PAYRUDDER_MCP_ALLOW_LIVE=1; the shape is the gateway's (pkg/apikey: qk_{live|test}_...). */
export function liveRefusal(apiKey: string, allowLive: boolean): string | undefined {
  return !allowLive && apiKey.startsWith("qk_live_")
    ? "This is a live API key (qk_live_...) and live keys are refused by default. Use a sandbox key (qk_test_...), or start the server with PAYRUDDER_MCP_ALLOW_LIVE=1 to let an assistant act on live data."
    : undefined;
}
