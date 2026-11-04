/**
 * The audit log: one JSON line per tool call, on stderr (stdout is the protocol in stdio mode, and a log collector reads stderr in HTTP mode). It answers "what did the
 * assistant do with this key, and when": the tool, whether it writes, which key (its public prefix only, never the secret), the arguments, the outcome and how long it
 * took. It does not hold the answer, and it does not hold what people wrote: arguments that are free text about a person (name, e-mail, notes) are replaced, lists are
 * counted, long strings are cut. Turn it off with PAYRUDDER_MCP_AUDIT=0.
 */
export interface AuditRecord {
  ts: string;
  event: "mcp.tool_call";
  tool: string;
  write: boolean;
  transport: "stdio" | "http";
  /** The key's non-secret part, qk_test_ab12cd34; empty when the server was given none. */
  key: string;
  /** OAuth mode: `key` is `oauth:<the person's id>` and this is the client they signed in through (its client_id address). Absent with a key. */
  client?: string;
  args: Record<string, unknown>;
  ok: boolean;
  /** The API's error key (billing.invoice.not_found) or the error's name; absent when the call worked. */
  error?: string;
  /** The API's HTTP status when it answered with an error. */
  status?: number;
  ms: number;
  /** Characters handed to the model, and whether the answer was cut. */
  chars: number;
  cut: boolean;
}

export type Audit = (record: AuditRecord) => void;

/** Arguments whose value is about a person and is not needed to know what was done. */
const PRIVATE = /^(name|email|phone|address|notes?|reason|description|properties)$/i;
const MAX_VALUE = 80;

/** What an audit line says of the arguments: scalars as they are (cut), people's text replaced, lists and objects by their size. */
export function describeArgs(args: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(args)) {
    if (PRIVATE.test(k)) out[k] = "[not logged]";
    else if (Array.isArray(v)) out[k] = { items: v.length };
    else if (v !== null && typeof v === "object") out[k] = { keys: Object.keys(v).length };
    else if (typeof v === "string") out[k] = v.length > MAX_VALUE ? `${v.slice(0, MAX_VALUE)}…` : v;
    else out[k] = v;
  }
  return out;
}

/** qk_test_ab12cd34_SECRET → qk_test_ab12cd34. A value that is not shaped like a key gives "": nothing of it is logged. */
export function keyPrefix(key: string | undefined): string {
  const m = /^(qk_(?:test|live)_[A-Za-z0-9]+)_/.exec(key ?? "");
  return m?.[1] ?? "";
}

/** The line written to stderr. A failure to write it never fails the tool call. */
export const stderrAudit: Audit = (r) => {
  try {
    process.stderr.write(`${JSON.stringify(r)}\n`);
  } catch {
    /* the log is best effort: a closed stderr must not turn a read into an error */
  }
};

/** stderrAudit unless PAYRUDDER_MCP_AUDIT=0. */
export const defaultAudit = (): Audit | undefined => (process.env.PAYRUDDER_MCP_AUDIT === "0" ? undefined : stderrAudit);
