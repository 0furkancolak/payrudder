import { describe, expect, it } from "vitest";
import { taxFormRules } from "./customerTax";

describe("taxFormRules", () => {
  it("asks a Turkish individual for the TCKN, optionally, and no tax office", () => {
    expect(taxFormRules("TR", "individual")).toEqual({ taxId: "optional", taxOffice: "hidden", eInvoice: true, label: "tckn", about: "tr_individual" });
  });
  it("requires the VKN of a Turkish business and offers its tax office", () => {
    expect(taxFormRules("tr", "business")).toEqual({ taxId: "required", taxOffice: "optional", eInvoice: true, label: "vkn", about: "tr_business" });
  });
  it("does not know which of the two a Turkish customer of no type has", () => {
    expect(taxFormRules("TR", "")).toMatchObject({ taxId: "optional", taxOffice: "optional", label: "tr", about: "tr_unknown" });
    expect(taxFormRules("TR", undefined)).toMatchObject({ label: "tr" });
  });
  it("asks a German business for a VAT id, optionally, and never for a tax office or an e-fatura box", () => {
    expect(taxFormRules("DE", "business")).toEqual({ taxId: "optional", taxOffice: "hidden", eInvoice: false, label: "vat", about: "eu_business" });
    expect(taxFormRules("GR", "business")).toMatchObject({ label: "vat" }); // Greece is GR as a country and EL in its VAT id
  });
  it("asks a British business for a VAT number and an American one for an EIN, optionally", () => {
    expect(taxFormRules("GB", "business")).toMatchObject({ taxId: "optional", label: "vat", about: "gb_business" });
    expect(taxFormRules("US", "business")).toMatchObject({ taxId: "optional", label: "ein", about: "us_business" });
  });
  it("does not ask an individual outside Turkey for a tax id", () => {
    for (const c of ["DE", "US", "GB", "FR", "JP"]) expect(taxFormRules(c, "individual")).toMatchObject({ taxId: "hidden", taxOffice: "hidden", eInvoice: false });
  });
  it("takes nothing away from a customer whose country is not known", () => {
    for (const k of ["", "individual", "business"]) expect(taxFormRules("", k)).toEqual({ taxId: "optional", taxOffice: "optional", eInvoice: true, label: "any", about: null });
  });
  it("asks anyone else, and a customer of no known country, in general terms", () => {
    expect(taxFormRules("JP", "business")).toMatchObject({ taxId: "optional", label: "taxid", about: null });
    expect(taxFormRules("DE", "")).toMatchObject({ taxId: "optional", label: "taxid" });
  });
  it("keeps a field visible while it holds a value, so it can be seen and cleared", () => {
    expect(taxFormRules("US", "individual", { taxId: true })).toMatchObject({ taxId: "optional" });
    expect(taxFormRules("DE", "business", { taxOffice: true })).toMatchObject({ taxOffice: "optional" });
    expect(taxFormRules("DE", "business", { eInvoice: true })).toMatchObject({ eInvoice: true });
  });
});
