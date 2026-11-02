/**
 * The identifiers a customer is taxed under, checked in the form: the Turkish TCKN (an individual) and VKN (a business) with their checksums, and a European VAT id
 * (its shape, and the check digits of the countries whose algorithm is public). The same algorithms as `pkg/taxid` in Go, with the same vectors (taxid.test.ts); the
 * form only *hints*: whether a wrong id is refused is a product decision (docs/plans/05-customer-locale-tax-documents.md), so saving is never blocked here.
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

/** Whether `raw` has the shape of a VAT id of the country its two first letters name (no check digit is looked at: that is `validEuVat`). */
export function validEuVatShape(raw: string): boolean {
  const id = normalizeTaxId(raw);
  return id.length >= 4 && (EU_VAT[id.slice(0, 2)]?.test(id) ?? false);
}

const digit = (n: string, i: number) => n.charCodeAt(i) - 48;
const num = (n: string) => Number(n);
const mod = (a: number, m: number) => ((a % m) + m) % m;
/** The sum of the digits of `n`, from the left, times the weights. */
const weighted = (n: string, ...w: number[]) => w.reduce((s, x, i) => s + x * digit(n, i), 0);

/** The Luhn sum of the digits of `n` modulo 10: 0 for a number whose last digit is its check digit. */
function luhnSum(n: string): number {
  let s = 0;
  for (let i = 0; i < n.length; i++) {
    let d = digit(n, n.length - 1 - i);
    if (i % 2 === 1) {
      d *= 2;
      d = Math.floor(d / 10) + (d % 10);
    }
    s += d;
  }
  return s % 10;
}

/** The ISO 7064 MOD 11,10 check (DE, HR): the last digit is the check digit. */
function mod11x10(n: string): boolean {
  let c = 5;
  for (let i = 0; i < n.length; i++) c = (((c || 10) * 2) % 11 + digit(n, i)) % 10;
  return c === 1;
}

/** The NIF: a DNI (digits and a letter), an NIE (X, Y, Z), a K, L or M number, or the CIF of a company (a Luhn digit or its letter). */
function esCheck(n: string): boolean {
  const letters = "TRWAGMYFPDXBNJZSQVHLCKE";
  const f = n.charAt(0);
  if (f >= "0" && f <= "9") return n.charAt(8) === letters.charAt(num(n.slice(0, 8)) % 23);
  if (f === "X" || f === "Y" || f === "Z") return n.charAt(8) === letters.charAt(num(`${"XYZ".indexOf(f)}${n.slice(1, 8)}`) % 23);
  if (f === "K" || f === "L" || f === "M") return n.charAt(8) === letters.charAt(num(n.slice(1, 8)) % 23);
  if (!"ABCDEFGHJNPQRSUVW".includes(f)) return false;
  const c = (10 - luhnSum(`${n.slice(1, 8)}0`)) % 10;
  return n.charAt(8) === String(c) || n.charAt(8) === "JABCDEFGHI".charAt(c);
}

/** The TVA number: a key (two digits, or letters but I and O for the numbers since 2013) and a SIREN, which is a Luhn number unless it starts with 000. */
function frCheck(n: string): boolean {
  const alphabet = "0123456789ABCDEFGHJKLMNPQRSTUVWXYZ";
  const i = alphabet.indexOf(n.charAt(0));
  const j = alphabet.indexOf(n.charAt(1));
  const siren = n.slice(2);
  if (i < 0 || j < 0 || (siren.slice(0, 3) !== "000" && luhnSum(siren) !== 0)) return false;
  if (i < 10 && j < 10) return i * 10 + j === num(`${siren}12`) % 97;
  const c = i < 10 ? i * 24 + j - 10 : i * 34 + j - 100;
  return (num(siren) + 1 + Math.floor(c / 11)) % 11 === c % 11;
}

/** The Irish number: seven digits and one or two letters, or the old form of a digit, a letter (or + or *), five digits and a letter. The letters run from W then A to V. */
function ieCheck(n: string): boolean {
  const alphabet = "WABCDEFGHIJKLMNOPQRSTUV";
  if ([...n.slice(7)].some((c) => !alphabet.includes(c))) return false;
  let body = n.slice(0, 7);
  let extra = n.length === 9 ? alphabet.indexOf(n.charAt(8)) : 0;
  if (!digits(body)) {
    // the old form: the first digit moves to the end
    body = `0${n.slice(2, 7)}${n.charAt(0)}`;
    extra = 0;
  }
  return n.charAt(7) === alphabet.charAt((weighted(body, 8, 7, 6, 5, 4, 3, 2) + 9 * extra) % 23);
}

const CY_ODD = [1, 0, 5, 7, 9, 13, 15, 17, 19, 21];

/**
 * The check-digit algorithm of the national part of a VAT id (the id without its two letters) that has the shape of EU_VAT, by prefix; the same as `vatCheck` in
 * pkg/taxid, ported from and tested against python-stdnum. Each returns true for a branch it does not check: a country that is not here (SK, XI) is shape-only; CZ is
 * checked for the 8 digits of a legal entity and the 9 starting with 6, BG for the 9 digits of a legal entity, LV for the codes of legal persons (the first digit
 * above 3), and the numbers made of a person's birth date (CZ 9 and 10 digits, BG 10 digits, LV personal codes) are shape-only.
 */
const VAT_CHECK: Record<string, (n: string) => boolean> = {
  AT: (n) => mod(6 - luhnSum(n.slice(1, 8)), 10) === digit(n, 8),
  BE: (n) => n !== "0000000000" && (num(n.slice(0, 8)) + num(n.slice(8))) % 97 === 0,
  BG: (n) => {
    if (n.length !== 9) return true;
    let c = weighted(n, 1, 2, 3, 4, 5, 6, 7, 8) % 11;
    if (c === 10) c = weighted(n, 3, 4, 5, 6, 7, 8, 9, 10) % 11;
    return c % 10 === digit(n, 8);
  },
  CY: (n) => {
    let s = 0;
    for (let i = 0; i < 8; i++) s += i % 2 === 0 ? (CY_ODD[digit(n, i)] ?? 0) : digit(n, i);
    return n.slice(0, 2) !== "12" && n.charAt(8) === String.fromCharCode(65 + (s % 26));
  },
  CZ: (n) => {
    if (n.length === 8) {
      const c = mod(11 - weighted(n, 8, 7, 6, 5, 4, 3, 2), 11) || 1;
      return n.charAt(0) !== "9" && c % 10 === digit(n, 7);
    }
    if (n.length === 9 && n.charAt(0) === "6") return mod(8 - mod(10 - (weighted(n.slice(1), 8, 7, 6, 5, 4, 3, 2) % 11), 11), 10) === digit(n, 8);
    return true;
  },
  DE: (n) => n.charAt(0) !== "0" && mod11x10(n),
  DK: (n) => n.charAt(0) !== "0" && weighted(n, 2, 7, 6, 5, 4, 3, 2, 1) % 11 === 0,
  EE: (n) => weighted(n, 3, 7, 1, 3, 7, 1, 3, 7, 1) % 10 === 0,
  EL: (n) => {
    let s = 0;
    for (let i = 0; i < 8; i++) s = s * 2 + digit(n, i);
    return ((s * 2) % 11) % 10 === digit(n, 8);
  },
  ES: esCheck,
  FI: (n) => weighted(n, 7, 9, 10, 5, 8, 4, 2, 1) % 11 === 0,
  FR: frCheck,
  HR: mod11x10,
  HU: (n) => weighted(n, 9, 7, 3, 1, 9, 7, 3, 1) % 10 === 0,
  IE: ieCheck,
  IT: (n) => {
    const office = num(n.slice(7, 10));
    return num(n.slice(0, 7)) !== 0 && ((office >= 1 && office <= 100) || office === 120 || office === 121 || office === 888 || office === 999) && luhnSum(n) === 0;
  },
  LT: (n) => {
    if ((n.length === 9 && n.charAt(7) !== "1") || (n.length === 12 && n.charAt(10) !== "1")) return false;
    let c = 0;
    let c2 = 0;
    for (let i = 0; i < n.length - 1; i++) {
      c += (1 + (i % 9)) * digit(n, i);
      c2 += (1 + ((i + 2) % 9)) * digit(n, i);
    }
    c %= 11;
    if (c === 10) c = c2;
    return (c % 11) % 10 === digit(n, n.length - 1);
  },
  LU: (n) => num(n.slice(0, 6)) % 89 === num(n.slice(6)),
  LV: (n) => n.charAt(0) <= "3" || mod(weighted(n, 9, 1, 4, 8, 3, 10, 2, 5, 7, 6, 1), 11) === 3,
  MT: (n) => n.charAt(0) !== "0" && weighted(n, 3, 4, 6, 7, 8, 9, 10, 1) % 37 === 0,
  NL: (n) => {
    if (num(n.slice(0, 9)) === 0 || num(n.slice(10)) === 0) return false;
    if (mod(weighted(n, 9, 8, 7, 6, 5, 4, 3, 2, -1), 11) === 0) return true; // the 11-proof of the BSN of a person
    let r = 0; // ISO 7064 mod 97-10 over "NL" + the number with its letters as numbers (N=23, L=21, B=11)
    for (const c of `2321${n.slice(0, 9)}11${n.slice(10)}`) r = (r * 10 + Number(c)) % 97;
    return r === 1;
  },
  PL: (n) => mod(weighted(n, 6, 5, 7, 2, 3, 4, 5, 6, 7, -1), 11) === 0,
  PT: (n) => n.charAt(0) !== "0" && mod(11 - weighted(n, 9, 8, 7, 6, 5, 4, 3, 2), 11) % 10 === digit(n, 8),
  RO: (n) => {
    if (n.charAt(0) === "0") return false;
    const body = "0".repeat(10 - n.length) + n.slice(0, -1);
    return ((10 * weighted(body, 7, 5, 3, 2, 1, 7, 5, 3, 2)) % 11) % 10 === digit(n, n.length - 1);
  },
  SE: (n) => luhnSum(n.slice(0, 10)) === 0,
  SI: (n) => {
    const c = 11 - (weighted(n, 8, 7, 6, 5, 4, 3, 2) % 11);
    return n.charAt(0) !== "0" && c !== 11 && c % 10 === digit(n, 7);
  },
};

/**
 * Whether `raw` is a VAT id: it has the shape of `validEuVatShape` and, for a country whose check digits are known (VAT_CHECK), they are right. A country without an
 * algorithm (SK, XI) is checked for its shape only. A local check: it says nothing about whether the number exists or belongs to the customer (that is VIES).
 */
export function validEuVat(raw: string): boolean {
  const id = normalizeTaxId(raw);
  if (!validEuVatShape(id)) return false;
  const check = VAT_CHECK[id.slice(0, 2)];
  return check === undefined || check(id.slice(2));
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
  if (EU_COUNTRIES.has(c) && kind === "business") return validEuVat(id) ? null : "eu_vat";
  return null;
}
