import { describe, expect, it } from "vitest";
import { accountEntries, auditActor, eInvoiceAccounts, eInvoiceTaker } from "./accounts";

const nilvera = { account: "nilvera", provider: "nilvera", label: "", mode: "production", is_default: true };
const nilveraTr = { account: "nilvera~tr", provider: "nilvera", label: "tr", mode: "test", is_default: false };

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

describe("who an audit entry names", () => {
  it("is a member by name, support for the operator and a short id otherwise", () => {
    const names = new Map([["u-1", "Ada"]]);
    expect(auditActor("user:u-1", names, "Support")).toBe("Ada");
    expect(auditActor("operator", names, "Support")).toBe("Support");
    expect(auditActor("user:0f8fad5b-d9cb-469f-a165-70867728950e", names, "Support")).toBe("0f8fad5b…");
  });
});
