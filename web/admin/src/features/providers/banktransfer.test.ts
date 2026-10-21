import { describe, expect, it } from "vitest";
import { buildBankSettings, emptyBankForm, templateOk } from "./banktransfer";

const valid = () => ({ ...emptyBankForm(), bankName: " Akbank ", accountHolder: "Acme A.S.", iban: "tr33 0006 1005 1978 6457 8413 26" });

describe("buildBankSettings", () => {
  it("normalises the IBAN and leaves blank optional fields out", () => {
    expect(buildBankSettings(valid())).toEqual({
      ok: true,
      settings: { bank_name: "Akbank", account_holder: "Acme A.S.", iban: "TR330006100519786457841326" },
    });
  });

  it("sends the optional fields that were filled in, 0 days included", () => {
    const r = buildBankSettings({ ...valid(), bic: "akbktris", referenceTemplate: "ACME {invoice_number}", currencies: ["TRY", "EUR"], windowDays: "0" });
    expect(r).toEqual({
      ok: true,
      settings: {
        bank_name: "Akbank",
        account_holder: "Acme A.S.",
        iban: "TR330006100519786457841326",
        bic: "AKBKTRIS",
        reference_template: "ACME {invoice_number}",
        currencies: ["TRY", "EUR"],
        payment_window_days: 0,
      },
    });
  });

  it("names every field that is wrong", () => {
    const r = buildBankSettings({ ...emptyBankForm(), iban: "TR33 0006 1005 1978 6457 8413 62", bic: "AK", referenceTemplate: "{nope}", windowDays: "91" });
    expect(r).toEqual({
      ok: false,
      errors: { bankName: "required", accountHolder: "required", iban: "checksum", bic: "badBic", referenceTemplate: "badTemplate", windowDays: "badWindow" },
    });
  });

  it("refuses a window that is not a whole number of days", () => {
    for (const w of ["-1", "1.5", "abc", "100"]) {
      const r = buildBankSettings({ ...valid(), windowDays: w });
      expect(r.ok, w).toBe(false);
    }
  });
});

describe("templateOk", () => {
  it("allows the known placeholders and plain text only", () => {
    expect(templateOk("{invoice_number}")).toBe(true);
    expect(templateOk("QF-{invoice_id}/{invoice_number}")).toBe(true);
    expect(templateOk("{invoice}")).toBe(false);
    expect(templateOk("{invoice_number")).toBe(false);
    expect(templateOk("}")).toBe(false);
  });
});
