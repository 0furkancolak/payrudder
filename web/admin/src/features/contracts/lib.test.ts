import { readdirSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import {
  AUDIENCES,
  KINDS,
  RETENTION,
  TEMPLATE_NOTICES,
  VARIABLES,
  agreementsAdvisory,
  currentVersions,
  diffLines,
  diffStats,
  evidenceValue,
  groupAcceptances,
  hasTemplateNotice,
  insertAtCursor,
  languagesOf,
  parseSeriesKey,
  previewDocument,
  publishFacts,
  requirable,
  retentionValid,
  renewalDaysValid,
  acceptanceQuery,
  canRequestRefund,
  emptyAcceptanceFilter,
  isAcceptanceFiltered,
  refundAmount,
  CANCEL_TEXT_MAX,
  REFUNDS,
  RENEWAL,
  seriesKey,
  settingsChanged,
  settingsDraft,
  versionsOf,
  withRequired,
  withdrawalMoves,
  witnessOf,
  type ContractAcceptance,
  type ContractSettings,
  type ContractVersion,
  type ContractWithdrawal,
} from "./lib";

const go = readFileSync(fileURLToPath(new URL("../../../../../services/plans-svc/internal/domain/contracts.go", import.meta.url)), "utf8");
const quoted = (list: string): string[] => [...list.matchAll(/"([^"]+)"/g)].map((m) => m[1] as string);

describe("what the console mirrors of the backend", () => {
  it("has the variables, kinds and retention limits that plans-svc has", () => {
    expect([...VARIABLES]).toEqual(quoted(/var ContractVariables = \[\]string\{([^}]*)\}/.exec(go)?.[1] ?? ""));
    const named = new Map([...go.matchAll(/^\s*(Contract[A-Za-z]+)\s*=\s*"([a-z_]+)"/gm)].map((m) => [m[1], m[2]]));
    const order = (/var ContractKinds = \[\]string\{([^}]*)\}/.exec(go)?.[1] ?? "").split(",").map((n) => n.trim()).filter(Boolean);
    expect([...KINDS]).toEqual(order.map((n) => named.get(n)));
    expect(RETENTION.min).toBe(Number(/MinRetentionMths = (\d+)/.exec(go)?.[1]));
    expect(RETENTION.max).toBe(Number(/MaxRetentionMths = (\d+)/.exec(go)?.[1]));
    expect(RETENTION.default).toBe(Number(/DefaultRetention = (\d+)/.exec(go)?.[1]));
  });

  it("has the renewal notice and cancel-text limits, and the refund settings, that plans-svc has", () => {
    expect(RENEWAL.default).toBe(Number(/DefaultRenewalNoticeDays = (\d+)/.exec(go)?.[1]));
    expect(RENEWAL.max).toBe(Number(/MaxRenewalNoticeDays\s*= (\d+)/.exec(go)?.[1]));
    expect(CANCEL_TEXT_MAX).toBe(Number(/MaxCancelText\s*= (\d+)/.exec(go)?.[1]));
    expect([...REFUNDS]).toEqual([/WithdrawalRefundFull = "([a-z]+)"/.exec(go)?.[1], /WithdrawalRefundNone = "([a-z]+)"/.exec(go)?.[1]]);
  });

  it("recognises the starter notices the backend writes", () => {
    expect([...TEMPLATE_NOTICES]).toEqual([/NoticeEN = "([^"]+)"/.exec(go)?.[1], /NoticeTR = "([^"]+)"/.exec(go)?.[1]]);
    expect(hasTemplateNotice(`**${TEMPLATE_NOTICES[0]}**\n\n# Terms`)).toBe(true);
    expect(hasTemplateNotice(`**${TEMPLATE_NOTICES[1]}**`)).toBe(true);
    expect(hasTemplateNotice("# Terms of service")).toBe(false);
  });
});

describe("putting a variable in a text", () => {
  it("inserts at the cursor and says where the cursor goes", () => {
    expect(insertAtCursor("Hello , bye", 6, 6, "customer_name")).toEqual({ text: "Hello {{customer_name}}, bye", caret: 6 + "{{customer_name}}".length });
  });

  it("replaces a selection, and a cursor outside the text is put at its ends", () => {
    expect(insertAtCursor("Dear XXX,", 5, 8, "customer_name").text).toBe("Dear {{customer_name}},");
    expect(insertAtCursor("abc", 99, 120, "date")).toEqual({ text: "abc{{date}}", caret: 11 });
    expect(insertAtCursor("abc", -4, -1, "date").text).toBe("{{date}}abc");
  });

  it("puts it in an empty text", () => {
    expect(insertAtCursor("", 0, 0, "seller_name")).toEqual({ text: "{{seller_name}}", caret: 15 });
  });
});

describe("the diff between two versions", () => {
  const ops = (a: string, b: string) => diffLines(a, b).map((l) => `${l.op === "same" ? " " : l.op === "add" ? "+" : "-"}${l.text}`);

  it("keeps what both have and marks the rest", () => {
    expect(ops("a\nb\nc", "a\nB\nc")).toEqual([" a", "-b", "+B", " c"]);
    expect(ops("a\nb", "a\nb\nc")).toEqual([" a", " b", "+c"]);
    expect(ops("a\nb\nc", "a\nc")).toEqual([" a", "-b", " c"]);
  });

  it("is empty-handed for equal texts, and all one side when the other is empty", () => {
    expect(ops("same\ntext", "same\ntext")).toEqual([" same", " text"]);
    expect(ops("", "x\ny")).toEqual(["+x", "+y"]);
    expect(ops("x\ny", "")).toEqual(["-x", "-y"]);
    expect(ops("", "")).toEqual([]);
  });

  it("shows a moved paragraph as removed and added, and finds the common lines in between", () => {
    expect(ops("one\ntwo\nthree\nfour", "two\nthree\nfour\none")).toEqual(["-one", " two", " three", " four", "+one"]);
    expect(ops("a\nx\nb\ny\nc", "a\nb\nz\nc")).toEqual([" a", "-x", " b", "-y", "+z", " c"]);
  });

  it("counts the lines it adds and removes", () => {
    expect(diffStats(diffLines("a\nb\nc", "a\nB\nc\nd"))).toEqual({ added: 2, removed: 1 });
  });

  it("shows two texts too long for its table as wholly replaced instead of building the table", () => {
    const a = Array.from({ length: 2500 }, (_, i) => `a${i}`).join("\n");
    const b = Array.from({ length: 2500 }, (_, i) => `b${i}`).join("\n");
    expect(diffStats(diffLines(a, b))).toEqual({ added: 2500, removed: 2500 });
  });
});

describe("which agreements can be required", () => {
  const settings = (required: ContractSettings["required_kinds"], published: ContractSettings["published_kinds"]) => ({ required_kinds: required, published_kinds: published });

  it("disables a kind with no published consumer version, unless it is already required (so it can be switched off)", () => {
    expect(requirable("tos", settings([], ["distance_sales"]))).toBe(false);
    expect(requirable("distance_sales", settings([], ["distance_sales"]))).toBe(true);
    expect(requirable("tos", settings(["tos"], []))).toBe(true);
    expect(requirable("tos", undefined)).toBe(false);
  });

  it("switches a kind on and off keeping the order of the kinds", () => {
    expect(withRequired(["privacy"], "distance_sales", true)).toEqual(["distance_sales", "privacy"]);
    expect(withRequired(["distance_sales", "privacy"], "privacy", false)).toEqual(["distance_sales"]);
    expect(withRequired([], "tos", false)).toEqual([]);
  });

  it("knows a form that differs from the stored settings, and a retention that the API would refuse", () => {
    const stored: ContractSettings = { required_kinds: ["tos"], immediate_start: false, retention_months: 36, published_kinds: ["tos"], update_time: null, withdrawal_refund: "full", renewal_notice_days: 7, renewal_cancel_texts: {}, withdrawal_function: true };
    expect(settingsChanged(settingsDraft(stored), settingsDraft(stored))).toBe(false);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), immediate_start: true })).toBe(true);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), required_kinds: [] })).toBe(true);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), retention_months: 60 })).toBe(true);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), withdrawal_refund: "none" })).toBe(true);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), renewal_notice_days: 0 })).toBe(true);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), withdrawal_function: false })).toBe(true);
    expect(settingsChanged(settingsDraft(stored), { ...settingsDraft(stored), renewal_cancel_texts: { ...settingsDraft(stored).renewal_cancel_texts, tr: "Panelden iptal edin" } })).toBe(true);
    // A seller that never saved the new settings has the API's defaults: a full refund, a notice a week ahead, the function on, no words of its own.
    expect(settingsDraft(undefined)).toEqual({
      required_kinds: [],
      immediate_start: false,
      retention_months: 36,
      withdrawal_refund: "full",
      renewal_notice_days: 7,
      renewal_cancel_texts: { en: "", tr: "", de: "", fr: "", es: "", ar: "" },
      withdrawal_function: true,
    });
    for (const [days, ok] of [[-1, false], [0, true], [7, true], [60, true], [61, false], [2.5, false], [Number.NaN, false]] as const) expect(renewalDaysValid(days), String(days)).toBe(ok);
    for (const [months, ok] of [[11, false], [12, true], [36, true], [120, true], [121, false], [24.5, false], [Number.NaN, false]] as const) expect(retentionValid(months), String(months)).toBe(ok);
  });
});

const version = (kind: ContractVersion["kind"], audience: ContractVersion["audience"], number: number, extra: Partial<ContractVersion> = {}): ContractVersion => ({
  id: `${kind}-${audience}-${number}`,
  kind,
  audience,
  number,
  material: true,
  bodies: { en: "text" },
  sha256: "h",
  note: "",
  author: "ada",
  create_time: "2026-10-01T10:00:00Z",
  variables: [],
  current: false,
  has_template_notice: false,
  ...extra,
});

describe("series", () => {
  const all = [version("tos", "consumer", 2, { current: true }), version("tos", "consumer", 1), version("privacy", "business", 1, { current: true, bodies: { en: "x", ar: " ", tr: "y" } })];

  it("finds the newest of each series and the history of one, newest first", () => {
    expect([...currentVersions(all).keys()].sort()).toEqual(["privacy~business", "tos~consumer"]);
    expect(currentVersions(all).get(seriesKey("tos", "consumer"))?.number).toBe(2);
    expect(versionsOf(all, "tos", "consumer").map((v) => v.number)).toEqual([2, 1]);
    expect(versionsOf(undefined, "tos", "consumer")).toEqual([]);
  });

  it("lists the languages that have a text, in the console's order", () => {
    expect(languagesOf(all[2] as ContractVersion)).toEqual(["en", "tr"]);
  });

  it("round-trips a series through the address and refuses a name it does not know", () => {
    for (const kind of KINDS) for (const audience of AUDIENCES) expect(parseSeriesKey(seriesKey(kind, audience))).toEqual({ kind, audience });
    expect(parseSeriesKey("nonsense~consumer")).toBeUndefined();
    expect(parseSeriesKey("tos")).toBeUndefined();
  });
});

describe("what publishing says will happen", () => {
  it("states the going live, the re-acceptance, whether the kind is required, and the starter notice, in that order", () => {
    expect(publishFacts({ first: false, material: true, required: true, template: false })).toEqual(["live", "material", "required"]);
    expect(publishFacts({ first: false, material: false, required: false, template: true })).toEqual(["live", "minor", "notRequired", "template"]);
    expect(publishFacts({ first: true, material: true, required: false, template: false })).toEqual(["live", "first", "notRequired"]);
  });
});

describe("withdrawal decisions", () => {
  it("lets a received request be decided once and an accepted one be marked refunded", () => {
    expect(withdrawalMoves("received")).toEqual(["accepted", "rejected"]);
    expect(withdrawalMoves("accepted")).toEqual(["refunded"]);
    expect(withdrawalMoves("rejected")).toEqual([]);
    expect(withdrawalMoves("refunded")).toEqual([]);
  });
});

describe("the go-live advice", () => {
  const settings = (required: ContractSettings["required_kinds"]) => ({ required_kinds: required });

  it("is optional for a seller that requires nothing, whatever it has published", () => {
    expect(agreementsAdvisory(settings([]), [])).toBe("optional");
    expect(agreementsAdvisory(undefined, [version("tos", "consumer", 1, { current: true, has_template_notice: true })])).toBe("optional");
  });

  it("is ready when every required kind has a current consumer text without the starter notice", () => {
    const published = [version("tos", "consumer", 1, { current: true }), version("privacy", "consumer", 1, { current: true })];
    expect(agreementsAdvisory(settings(["tos", "privacy"]), published)).toBe("ready");
  });

  it("warns while a required kind's current text carries the starter notice, and not for an old version or a kind that is not required", () => {
    expect(agreementsAdvisory(settings(["tos"]), [version("tos", "consumer", 1, { current: true, has_template_notice: true })])).toBe("template");
    expect(agreementsAdvisory(settings(["tos"]), [version("tos", "consumer", 2, { current: true }), version("tos", "consumer", 1, { has_template_notice: true })])).toBe("ready");
    expect(agreementsAdvisory(settings(["tos"]), [version("tos", "consumer", 1, { current: true }), version("privacy", "consumer", 1, { current: true, has_template_notice: true })])).toBe("ready");
  });

  it("warns that a required kind has no consumer text (a business-only series leaves consumers nothing to accept)", () => {
    expect(agreementsAdvisory(settings(["tos"]), [])).toBe("missing");
    expect(agreementsAdvisory(settings(["tos"]), [version("tos", "business", 1, { current: true })])).toBe("missing");
  });
});

const acceptance = (id: string, group: string, kind: ContractAcceptance["kind"], at: string, extra: Partial<ContractAcceptance> = {}): ContractAcceptance => ({
  id,
  group_id: group,
  customer_id: "c1",
  version_id: `v-${id}`,
  kind,
  audience: "consumer",
  version_number: 1,
  version_sha256: "h",
  locale_requested: "tr",
  locale_served: "tr",
  body_sha256: "b",
  context_kind: "hosted_invoice",
  context_ref: "inv_1",
  source: "hosted",
  observed: { ip: true, user_agent: true, time: true, text: true },
  checkbox_text: "I accept",
  immediate_start_consent: false,
  immediate_start_text: "",
  withdrawal_waiver_acknowledged: false,
  withdrawal_waiver_text: "",
  variables: {},
  snapshot_sha256: "s",
  ip: "203.0.113.7",
  user_agent: "Mozilla/5.0",
  accept_time: at,
  customer_external_id: "cus_1",
  ...extra,
});

describe("a customer's log of acceptances", () => {
  const rows = [
    acceptance("a1", "g-old", "tos", "2026-09-01T10:00:00Z"),
    acceptance("a2", "g-new", "privacy", "2026-10-05T10:00:00Z"),
    acceptance("a3", "g-new", "distance_sales", "2026-10-05T10:00:00Z"),
    acceptance("a4", "g-mid", "tos", "2026-09-20T10:00:00Z", { source: "api" }),
  ];

  it("groups the versions accepted together, the newest group first, each group's versions in the order of the kinds", () => {
    const groups = groupAcceptances(rows);
    expect(groups.map((g) => g.id)).toEqual(["g-new", "g-mid", "g-old"]);
    expect(groups[0]?.items.map((a) => a.kind)).toEqual(["distance_sales", "privacy"]);
    expect(groups[0]?.anchor.id).toBe("a3");
    expect(groups[1]?.source).toBe("api");
    expect(groupAcceptances(undefined)).toEqual([]);
  });

  it("says who vouches for a piece of evidence", () => {
    const sellerSaid = acceptance("a5", "g", "tos", "2026-10-01T10:00:00Z", { source: "api", observed: { ip: false, user_agent: false, time: false, text: false } });
    expect(witnessOf(rows[0] as ContractAcceptance, "ip")).toBe("observed");
    expect(witnessOf(sellerSaid, "time")).toBe("reported");
    expect(witnessOf({ observed: {} }, "ip")).toBe("reported");
  });

  it("tells an address that was erased after the retention from one that was never given", () => {
    expect(evidenceValue(rows[0] as ContractAcceptance, "ip")).toEqual({ value: "203.0.113.7", state: "present" });
    expect(evidenceValue(acceptance("a6", "g", "tos", "2026-10-01T10:00:00Z", { ip: "", user_agent: "" }), "ip").state).toBe("erased");
    expect(evidenceValue(acceptance("a7", "g", "tos", "2026-10-01T10:00:00Z", { ip: "", observed: { ip: false } }), "ip").state).toBe("none");
  });
});

describe("the document a preview is shown in", () => {
  it("sets the language and direction of the text, and a policy that lets nothing load", () => {
    const ar = previewDocument("<p>مرحبا</p>", "ar");
    expect(ar).toContain('lang="ar" dir="rtl"');
    expect(previewDocument("<p>x</p>", "tr")).toContain('lang="tr" dir="ltr"');
    expect(previewDocument("<p>x</p>", "xx")).toContain('lang="en" dir="ltr"');
    expect(ar).toContain("default-src 'none'");
    expect(ar).toContain("<body><p>مرحبا</p></body>");
  });
});

describe("text that is not ours", () => {
  it("is never put into this page as markup: the stored page and the preview go only through the sandboxed frame", () => {
    const dir = fileURLToPath(new URL(".", import.meta.url));
    const components = readdirSync(dir).filter((f) => f.endsWith(".vue"));
    expect(components.length).toBeGreaterThan(5);
    for (const f of components) expect(readFileSync(`${dir}${f}`, "utf8"), f).not.toContain("v-html");
    const frame = readFileSync(`${dir}SandboxedHtml.vue`, "utf8");
    expect(frame).toContain('sandbox=""');
    expect(frame).toContain(":srcdoc");
  });
});

describe("asking for the refund of an accepted withdrawal", () => {
  const w = (extra: Partial<ContractWithdrawal>): ContractWithdrawal => ({ id: "w", customer_id: "c", customer_external_id: "cus", subscription_id: "", invoice_id: "inv-1", statement: "", source: "hosted", status: "accepted", receive_time: "2026-10-18T09:00:00Z", decide_time: null,
    decided_by: "", decision_note: "", credit_note_id: "", refund_state: "none", refund_note: "", refund_time: null, ...extra });
  it("is offered for an accepted withdrawal that names an invoice and has no refund, or a failed one, and for nothing else", () => {
    expect(canRequestRefund(w({}))).toBe(true);
    expect(canRequestRefund(w({ refund_state: "failed" }))).toBe(true);
    // A credit note that exists, whatever became of its refund, is never asked for twice: a failed card refund is retried on the credit note.
    for (const state of ["requesting", "requested", "refunded", "refund_failed", "outside"] as const) expect(canRequestRefund(w({ refund_state: state })), state).toBe(false);
    expect(canRequestRefund(w({ invoice_id: "" }))).toBe(false);
    for (const status of ["received", "rejected", "refunded"] as const) expect(canRequestRefund(w({ status })), status).toBe(false);
  });
});

describe("the amount of a refund", () => {
  it("is the whole of what is left when nothing is typed, and a typed amount in the invoice's currency otherwise", () => {
    expect(refundAmount("", "USD", "4900")).toEqual({ minor: "0" });
    expect(refundAmount("  ", "USD", "4900")).toEqual({ minor: "0" });
    expect(refundAmount("12.5", "USD", "4900")).toEqual({ minor: "1250" });
    expect(refundAmount("12,50", "USD", "4900")).toEqual({ minor: "1250" });
    expect(refundAmount("500", "JPY", "4900")).toEqual({ minor: "500" });
    expect(refundAmount("49", "USD", "4900")).toEqual({ minor: "4900" });
  });
  it("refuses zero, text, too many decimals for the currency, and more than the invoice", () => {
    for (const bad of ["0", "0.00", "abc", "-5", "1.234"]) expect(refundAmount(bad, "USD", "4900"), bad).toEqual({ error: "amount" });
    expect(refundAmount("0.5", "JPY", "4900")).toEqual({ error: "amount" });
    expect(refundAmount("49.01", "USD", "4900")).toEqual({ error: "tooMuch" });
    expect(refundAmount("1", "USD", "not a number")).toEqual({ error: "tooMuch" });
  });
});

describe("the filter of a customer's acceptance log", () => {
  it("narrows nothing when empty", () => {
    expect(isAcceptanceFiltered(emptyAcceptanceFilter())).toBe(false);
    expect(acceptanceQuery(emptyAcceptanceFilter())).toEqual({});
  });
  it("sends the kind and the source, and the last day of the range as the start of the day after it", () => {
    const q = acceptanceQuery({ kind: "tos", source: "hosted", range: { from: "2026-10-01", to: "2026-10-31" } });
    expect(q.kind).toBe("tos");
    expect(q.source).toBe("hosted");
    expect(Date.parse(q.from ?? "")).toBe(new Date(2026, 9, 1).getTime());
    expect(Date.parse(q.to ?? "")).toBe(new Date(2026, 10, 1).getTime());
    expect(isAcceptanceFiltered({ kind: "", source: "api", range: { from: "", to: "" } })).toBe(true);
  });
});
