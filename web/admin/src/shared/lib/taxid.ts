/**
 * The identifiers a customer is taxed under, checked in the form: the Turkish TCKN (an individual) and VKN (a business) with their checksums, and the shape of a
 * European VAT id. The same algorithms as `pkg/taxid` in Go, with the same vectors (taxid.test.ts); the form only *hints*: whether a wrong id is refused is a product
 * decision (docs/plans/05-customer-locale-tax-documents.md), so saving is never blocked here.
 */

/** Removes what people type for readability (spaces, dots, dashes) and upper-cases. */
export function normalizeTaxId(id: string): string {
  return id.replace(/[\s.\- ]/g, "").toUpperCase();
}

const digits = (s: string) => /^\d+$/.test(s);

export function validTckn(raw: string): boolean {
  const id = normalizeTaxId(raw);
  if (id.length !== 11 || !digits(id) || id[0] === "0") return false;
  const d = [...id].map(Number) as number[];
  const at = (i: number) => d[i] ?? 0;
  const odd = at(0) + at(2) + at(4) + at(6) + at(8);
  const even = at(1) + at(3) + at(5) + at(7);
  if ((((odd * 7 - even) % 10) + 10) % 10 !== at(9)) return false;
  return d.slice(0, 10).reduce((a, b) => a + b, 0) % 10 === at(10);
}

export function validVkn(raw: string): boolean {
  const id = normalizeTaxId(raw);
  if (id.length !== 10 || !digits(id)) return false;
  let sum = 0;
  for (let i = 0; i < 9; i++) {
    const tmp = (Number(id[i]) + 9 - i) % 10;
    let v = (tmp * 2 ** (9 - i)) % 9;
    if (tmp !== 0 && v === 0) v = 9;
    sum += v;
  }
  return (10 - (sum % 10)) % 10 === Number(id[9]);
}

const EU_VAT: Record<string, RegExp> = {
  AT: /^ATU\d{8}$/, BE: /^BE[01]\d{9}$/, BG: /^BG\d{9,10}$/, CY: /^CY\d{8}[A-Z]$/, CZ: /^CZ\d{8,10}$/, DE: /^DE\d{9}$/, DK: /^DK\d{8}$/, EE: /^EE\d{9}$/, EL: /^EL\d{9}$/,
  ES: /^ES[A-Z0-9]\d{7}[A-Z0-9]$/, FI: /^FI\d{8}$/, FR: /^FR[A-Z0-9]{2}\d{9}$/, HR: /^HR\d{11}$/, HU: /^HU\d{8}$/, IE: /^IE(\d[A-Z0-9+*]\d{5}[A-Z]{1,2}|\d{7}[A-Z]{1,2})$/,
  IT: /^IT\d{11}$/, LT: /^LT(\d{9}|\d{12})$/, LU: /^LU\d{8}$/, LV: /^LV\d{11}$/, MT: /^MT\d{8}$/, NL: /^NL\d{9}B\d{2}$/, PL: /^PL\d{10}$/, PT: /^PT\d{9}$/, RO: /^RO\d{2,10}$/,
  SE: /^SE\d{10}01$/, SI: /^SI\d{8}$/, SK: /^SK\d{10}$/, XI: /^XI(\d{9}|\d{12}|GD\d{3}|HA\d{3})$/,
};

/** Whether `raw` has the shape of a VAT id of the country its two first letters name (the check digits are not verified: that is what VIES is for). */
export function validEuVatShape(raw: string): boolean {
  const id = normalizeTaxId(raw);
  return id.length >= 4 && (EU_VAT[id.slice(0, 2)]?.test(id) ?? false);
}

/** The ISO countries whose VAT ids are in the list above (Greece is EL in a VAT id and GR as a country). */
const EU_COUNTRIES = new Set(["AT", "BE", "BG", "CY", "CZ", "DE", "DK", "EE", "GR", "ES", "FI", "FR", "HR", "HU", "IE", "IT", "LT", "LU", "LV", "MT", "NL", "PL", "PT", "RO", "SE", "SI", "SK"]);

/** What the form says about an id: nothing (`null`), or the key of a hint under `taxid.hint.*`. Never a reason to refuse the save. */
export type TaxIdHint = "tr_individual" | "tr_business" | "tr_any" | "tr_is_tckn" | "tr_is_vkn" | "eu_vat";

export function taxIdHint(rawId: string, country: string | undefined, kind: "individual" | "business" | "" | undefined): TaxIdHint | null {
  const id = normalizeTaxId(rawId);
  if (id === "") return null;
  const c = (country ?? "").toUpperCase();
  if (c === "TR") {
    if (kind === "individual") return validTckn(id) ? null : validVkn(id) ? "tr_is_vkn" : "tr_individual";
    if (kind === "business") return validVkn(id) ? null : validTckn(id) ? "tr_is_tckn" : "tr_business";
    return validTckn(id) || validVkn(id) ? null : "tr_any";
  }
  if (EU_COUNTRIES.has(c) && kind === "business") return validEuVatShape(id) ? null : "eu_vat";
  return null;
}
