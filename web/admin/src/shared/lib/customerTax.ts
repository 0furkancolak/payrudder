import { isEuCountry } from "./taxid";

/**
 * What a customer's tax part of the form asks for, by the customer's country and by whether it is an individual or a business (docs/plans/05-customer-locale-tax-documents.md).
 * The tax id is *required* only where Payrudder itself would refuse the document without it: an e-arşiv invoice to a business in Turkey needs its VKN
 * (payments-worker/internal/einvoice). Everywhere else it is optional, and for an individual outside Turkey it is not asked for at all (a personal identifier such as
 * a US social security number is not something to collect). A customer with no country gets everything, optional. What is not asked for stays visible while it
 * holds a value, so it can be seen and cleared.
 * Defaults, not agreed; the format check on top of this is `taxIdHint`, which only hints.
 */
export type FieldNeed = "hidden" | "optional" | "required";
/** The label of the tax id under `einvoice.customer.labels`; `any` is the generic one when the country is not known. */
export type TaxIdLabel = "tckn" | "vkn" | "tr" | "vat" | "ein" | "taxid" | "any";
/** The one-sentence explanation under `einvoice.customer.about`; null when there is nothing to add. */
export type TaxAbout = "tr_individual" | "tr_business" | "tr_unknown" | "eu_business" | "gb_business" | "us_business";

export interface TaxFormRules {
  taxId: FieldNeed;
  taxOffice: FieldNeed;
  /** Whether the "registered for e-fatura" box is shown (a Turkish scheme). */
  eInvoice: boolean;
  label: TaxIdLabel;
  about: TaxAbout | null;
}

/** `kind` is the customer type ("" when nobody entered one); `filled` says which of the three fields already hold something. */
export function taxFormRules(
  country: string | undefined,
  kind: string | undefined,
  filled: { taxId?: boolean; taxOffice?: boolean; eInvoice?: boolean } = {},
): TaxFormRules {
  const c = (country ?? "").trim().toUpperCase();
  const k = kind === "individual" || kind === "business" ? kind : "";
  let r: TaxFormRules;
  if (c === "TR") {
    if (k === "individual") r = { taxId: "optional", taxOffice: "hidden", eInvoice: true, label: "tckn", about: "tr_individual" };
    else if (k === "business") r = { taxId: "required", taxOffice: "optional", eInvoice: true, label: "vkn", about: "tr_business" };
    else r = { taxId: "optional", taxOffice: "optional", eInvoice: true, label: "tr", about: "tr_unknown" };
  } else if (c === "") {
    // Nothing is known about where the customer is, so nothing is taken away.
    r = { taxId: "optional", taxOffice: "optional", eInvoice: true, label: "any", about: null };
  } else if (k === "individual") {
    r = { taxId: "hidden", taxOffice: "hidden", eInvoice: false, label: "taxid", about: null };
  } else if (k === "business" && isEuCountry(c)) {
    r = { taxId: "optional", taxOffice: "hidden", eInvoice: false, label: "vat", about: "eu_business" };
  } else if (k === "business" && c === "GB") {
    r = { taxId: "optional", taxOffice: "hidden", eInvoice: false, label: "vat", about: "gb_business" };
  } else if (k === "business" && c === "US") {
    r = { taxId: "optional", taxOffice: "hidden", eInvoice: false, label: "ein", about: "us_business" };
  } else {
    r = { taxId: "optional", taxOffice: "hidden", eInvoice: false, label: "taxid", about: null };
  }
  if (r.taxId === "hidden" && filled.taxId) r = { ...r, taxId: "optional" };
  if (r.taxOffice === "hidden" && filled.taxOffice) r = { ...r, taxOffice: "optional" };
  if (!r.eInvoice && filled.eInvoice) r = { ...r, eInvoice: true };
  return r;
}
