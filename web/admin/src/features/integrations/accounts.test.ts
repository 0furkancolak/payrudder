import { describe, expect, it } from "vitest";
import { accountEntries, auditActor, canFailover, eInvoiceAccounts, eInvoiceTaker, failoverTargets, healthState, healthTone, mailAccounts, mailTaker } from "./accounts";

const nilvera = { account: "nilvera", provider: "nilvera", label: "", mode: "production", is_default: true };
const nilveraTr = { account: "nilvera~tr", provider: "nilvera", label: "tr", mode: "test", is_default: false };
const nafru = { account: "nafru", provider: "nafru", label: "", is_default: true };
const nafruEu = { account: "nafru~eu", provider: "nafru", label: "eu", is_default: false };

describe("the accounts of an app as one list", () => {
  it("holds payment accounts, e-invoice accounts and the e-mail sender in use, in that order", () => {
    const rows = accountEntries({
      payments: [{ provider: "paddle~eu", working: true, default: false }, { provider: "paddle", working: true, default: true }],
      eInvoice: [nilveraTr, nilvera],
      mail: { connected: false },
    });
    expect(rows.map((r) => `${r.kind}:${r.id}`)).toEqual(["payment:paddle", "payment:paddle~eu", "einvoice:nilvera", "einvoice:nilvera~tr", "mail:mail:builtin"]);
    expect(rows.find((r) => r.id === "paddle~eu")).toMatchObject({ provider: "paddle", label: "eu", isDefault: false });
    expect(rows.find((r) => r.id === "nilvera~tr")).toMatchObject({ provider: "nilvera", label: "tr", mode: "test", isDefault: false });
  });
  it("names the sender in use as the default mail account", () => {
    const builtIn = accountEntries({ payments: [], eInvoice: [], mail: { connected: false } });
    expect(builtIn).toHaveLength(1);
    expect(builtIn[0]).toMatchObject({ id: "mail:builtin", state: "always", isDefault: true });
    const own = accountEntries({ payments: [], eInvoice: [], mail: { connected: true, integration: { check_problem: "refused" } } });
    expect(own.find((r) => r.id === "mail:builtin")).toMatchObject({ state: "unused", isDefault: false });
    expect(own.find((r) => r.id === "mail:nafru")).toMatchObject({ state: "problem", isDefault: true });
  });
  it("lists every own e-mail account with its label and default, and the built-in sender is in use only while there is none", () => {
    const rows = accountEntries({ payments: [], eInvoice: [], mail: { connected: true, integration: nafru, accounts: [nafru, { ...nafruEu, check_problem: "refused" }] } });
    expect(rows.map((r) => r.id)).toEqual(["mail:nafru", "mail:nafru~eu", "mail:builtin"]);
    expect(rows.find((r) => r.id === "mail:nafru~eu")).toMatchObject({ kind: "mail", key: "nafru~eu", provider: "nafru", label: "eu", isDefault: false, state: "problem" });
    expect(rows.find((r) => r.id === "mail:nafru")).toMatchObject({ key: "nafru", isDefault: true, state: "working" });
    expect(rows.find((r) => r.id === "mail:builtin")).toMatchObject({ key: "", state: "unused", isDefault: false });
  });
  it("reads a gateway that only knows the default e-mail account as one account", () => {
    expect(mailAccounts({ connected: true, integration: { provider: "nafru", from: "a@b.c" } })).toEqual([{ provider: "nafru", from: "a@b.c", account: "nafru", is_default: true }]);
    expect(mailAccounts({ connected: false, integration: null })).toEqual([]);
    expect(mailAccounts(undefined)).toEqual([]);
    expect(mailAccounts({ connected: true, integration: nafru, accounts: [nafru, nafruEu] })).toHaveLength(2);
  });
  it("reads a gateway that only knows the default as one account", () => {
    expect(eInvoiceAccounts({ account: { provider: "nilvera", mode: "production" } })).toEqual([{ provider: "nilvera", mode: "production", account: "nilvera", is_default: true }]);
    expect(eInvoiceAccounts({ account: null })).toEqual([]);
    expect(eInvoiceAccounts(undefined)).toEqual([]);
    expect(eInvoiceAccounts({ account: nilvera, accounts: [nilvera, nilveraTr] })).toHaveLength(2);
  });
});

describe("which e-invoice account takes a buyer", () => {
  const rules = [{ country: "DE", account: "nilvera~tr" }];
  it("is the account a rule names for the country, else the default", () => {
    expect(eInvoiceTaker([nilvera, nilveraTr], rules, "DE")).toEqual({ account: "nilvera~tr", via: "rule" });
    expect(eInvoiceTaker([nilvera, nilveraTr], rules, " de ")).toEqual({ account: "nilvera~tr", via: "rule" });
    expect(eInvoiceTaker([nilvera, nilveraTr], rules, "TR")).toEqual({ account: "nilvera", via: "default" });
    expect(eInvoiceTaker([nilvera, nilveraTr], rules, "")).toEqual({ account: "nilvera", via: "default" });
  });
  it("ignores a rule whose account is gone, and says so when nothing takes the buyer", () => {
    expect(eInvoiceTaker([nilvera], rules, "DE")).toEqual({ account: "nilvera", via: "default" });
    expect(eInvoiceTaker([{ ...nilveraTr }], [], "FR")).toEqual({ account: null, via: "none" });
    expect(eInvoiceTaker([], rules, "DE")).toEqual({ account: null, via: "none" });
  });
});

describe("which e-mail account sends a mail", () => {
  const rules = [{ language: "de", account: "nafru~eu" }];
  it("is the account a rule names for the language of the mail, else the default", () => {
    expect(mailTaker([nafru, nafruEu], rules, "de")).toEqual({ account: "nafru~eu", via: "rule" });
    expect(mailTaker([nafru, nafruEu], rules, "tr")).toEqual({ account: "nafru", via: "default" });
  });
  it("takes a mail with no language as English, like the notifier", () => {
    expect(mailTaker([nafru, nafruEu], [{ language: "en", account: "nafru~eu" }], "")).toEqual({ account: "nafru~eu", via: "rule" });
    expect(mailTaker([nafru, nafruEu], rules, "")).toEqual({ account: "nafru", via: "default" });
  });
  it("ignores a rule whose account is gone, and is the built-in sender with no account", () => {
    expect(mailTaker([nafru], rules, "de")).toEqual({ account: "nafru", via: "default" });
    expect(mailTaker([], rules, "de")).toEqual({ account: null, via: "builtin" });
    expect(mailTaker([{ ...nafruEu }], [], "fr")).toEqual({ account: "nafru~eu", via: "default" }); // no account is marked, the first stands in
  });
});

describe("who an audit entry names", () => {
  it("is a member by name, support for the operator and a short id otherwise", () => {
    const names = new Map([["u-1", "Ada"]]);
    expect(auditActor("user:u-1", names, "Support")).toBe("Ada");
    expect(auditActor("operator", names, "Support")).toBe("Support");
    expect(auditActor("user:0f8fad5b-d9cb-469f-a165-70867728950e", names, "Support")).toBe("0f8fad5b…");
  });
});

describe("how a payment account is doing", () => {
  it("is what the last check found, unless the circuit breaker says more", () => {
    expect(healthState({ status: "healthy", breaker: "closed" })).toBe("healthy");
    expect(healthState({ status: "down", breaker: "closed" })).toBe("down");
    expect(healthState({ status: "bad_credentials", breaker: "closed" })).toBe("bad_credentials");
    expect(healthState({ status: "healthy", breaker: "open" })).toBe("circuit_open"); // charges are being refused whatever the last check was
    expect(healthState({ status: "down", breaker: "half_open" })).toBe("recovering");
  });
  it("is unknown when nothing was checked, there is no row or the status is one the console does not know", () => {
    expect(healthState(undefined)).toBe("unknown");
    expect(healthState({ status: "unknown", breaker: "closed" })).toBe("unknown");
    expect(healthState({ status: "something_new" })).toBe("unknown");
  });
  it("is painted by how much it matters", () => {
    expect(healthTone("healthy")).toBe("success");
    expect(healthTone("recovering")).toBe("warning");
    expect(healthTone("unknown")).toBe("neutral");
    for (const s of ["down", "bad_credentials", "circuit_open"] as const) expect(healthTone(s)).toBe("danger");
  });
});

describe("failover", () => {
  const entries = accountEntries({
    payments: [{ provider: "stripe", working: true, default: true }, { provider: "stripe~eu", working: true, default: false }, { provider: "banktransfer", working: true, default: false }, { provider: "paddle", working: true, default: false }],
    eInvoice: [nilvera],
    mail: { connected: false },
  });
  it("is for payment accounts that are charged, not a bank transfer and not an e-invoice or mail row", () => {
    expect(entries.filter(canFailover).map((e) => e.key)).toEqual(["paddle", "stripe", "stripe~eu"]);
  });
  it("offers the app's other charged accounts and never the account itself", () => {
    expect(failoverTargets(entries, "stripe").map((e) => e.key)).toEqual(["paddle", "stripe~eu"]);
    expect(failoverTargets(entries, "paddle").map((e) => e.key)).toEqual(["stripe", "stripe~eu"]);
    expect(failoverTargets(accountEntries({ payments: [{ provider: "stripe", working: true, default: true }], eInvoice: [], mail: { connected: false } }), "stripe")).toEqual([]);
  });
});
