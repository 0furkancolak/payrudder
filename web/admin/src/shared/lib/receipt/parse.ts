import { CURRENCIES, CURRENCY_EXPONENTS, type Currency } from "../currencies";

/**
 * Reads the text of a receipt or an invoice (the text layer of a PDF, or what OCR made of a photo) and suggests the fields of an expense. Pure: no browser, no
 * network, so it is tested with the text of real documents (Turkish, English, German, French and Spanish samples in parse.test.ts).
 *
 * It only suggests. Every field carries a confidence from 0 to 1 (how sure the parser is, not how good the scan was): the review table marks what is below
 * `LOW_CONFIDENCE` and a person confirms every value before an expense is saved. A field that was not found is left out.
 */

export interface Field<T> {
  value: T;
  /** 0..1. */
  confidence: number;
}

export interface ParsedReceipt {
  vendor?: Field<string>;
  /** The day of the document, 2026-10-21. */
  date?: Field<string>;
  /** The total in minor units of `currency` (it needs a currency, which defaults to the caller's and is then low in confidence). */
  amountMinor?: Field<number>;
  currency?: Field<Currency>;
  /** The tax inside the total, minor units. */
  taxMinor?: Field<number>;
}

export interface ParseOptions {
  /** The file name; its words are the vendor of last resort. */
  filename?: string;
  /** The currency to use when the document shows none (the tenant's). */
  defaultCurrency?: Currency;
}

/** A field below this is shown as uncertain. */
export const LOW_CONFIDENCE = 0.6;

const CURRENCY_SET: ReadonlySet<string> = new Set(CURRENCIES);

// --- the text, line by line

function normalize(text: string): string[] {
  return text
    .replace(/[    ]/g, " ")
    .replace(/[‎‏‪-‮]/g, "")
    .replace(/\r/g, "\n")
    .split("\n")
    .map((l) => l.replace(/[ \t]+/g, " ").trim())
    .filter((l) => l !== "");
}

// --- currency

const SYMBOLS: [RegExp, Currency][] = [
  [/€/g, "EUR"],
  [/£/g, "GBP"],
  [/₺/g, "TRY"],
  [/(?<![A-Za-z])TL(?![A-Za-z])/g, "TRY"],
  [/₹/g, "INR"],
  [/(?<![A-Za-z])US\$|\bUSD\b|\$/g, "USD"],
  [/¥/g, "JPY"],
];

function detectCurrency(text: string): { currency: Currency; confidence: number } | undefined {
  const counts = new Map<Currency, number>();
  const bump = (c: Currency, n: number) => counts.set(c, (counts.get(c) ?? 0) + n);
  for (const m of text.matchAll(/\b[A-Z]{3}\b/g)) {
    if (CURRENCY_SET.has(m[0])) bump(m[0] as Currency, 3);
  }
  for (const [re, c] of SYMBOLS) bump(c, [...text.matchAll(re)].length);
  let best: Currency | undefined;
  let score = 0;
  for (const [c, n] of counts) {
    if (n > score) [best, score] = [c, n];
  }
  if (!best || score === 0) return undefined;
  const iso = new RegExp(`\\b${best}\\b`).test(text);
  return { currency: best, confidence: iso ? 0.9 : 0.7 };
}

// --- numbers

/** The numbers of a line, as written: "1.234,56", "49.00", "1 250". A percentage ("20%", "%18") and a part of a date are not amounts. */
function numberTokens(line: string): string[] {
  const cleaned = line.replace(DATE_ANY, " ");
  const out: string[] = [];
  for (const m of cleaned.matchAll(/(?<![\d.,'A-Za-z])(\d{1,3}(?:[ .,']\d{3})+(?:[.,]\d{1,3})?|\d+(?:[.,]\d{1,3})?)(?![\d])(?!\s*%)/g)) {
    const at = m.index ?? 0;
    if (cleaned[at - 1] === "%") continue;
    out.push(m[1] ?? "");
  }
  return out;
}

/** Reads "1.234,56" / "1,234.56" / "1234.5" / "1 250" into minor units of a currency, or undefined. Done on the digits: no float. */
export function amountToMinor(token: string, currency: Currency): number | undefined {
  const exp = CURRENCY_EXPONENTS[currency] ?? 2;
  const t = token.replace(/[ ']/g, "");
  const lastDot = t.lastIndexOf(".");
  const lastComma = t.lastIndexOf(",");
  let whole: string;
  let frac = "";
  const split = (at: number) => {
    whole = t.slice(0, at).replace(/[.,]/g, "");
    frac = t.slice(at + 1);
  };
  whole = t.replace(/[.,]/g, "");
  if (lastDot >= 0 && lastComma >= 0) {
    split(Math.max(lastDot, lastComma));
  } else if (lastDot >= 0 || lastComma >= 0) {
    const at = Math.max(lastDot, lastComma);
    const sepCount = (t.match(/[.,]/g) ?? []).length;
    const digitsAfter = t.length - at - 1;
    // "1.234" and "1,234" with three digits after a single separator are thousands (unless the currency has three decimals); "12.50" and "12,5" are decimals.
    const thousands = sepCount > 1 || (digitsAfter === 3 && exp !== 3);
    if (!thousands) split(at);
  }
  if (frac.length > exp) {
    if (/^0+$/.test(frac.slice(exp))) frac = frac.slice(0, exp);
    else return undefined;
  }
  const digits = (whole + frac.padEnd(exp, "0")).replace(/^0+(?=\d)/, "");
  const n = Number(digits);
  return Number.isSafeInteger(n) ? n : undefined;
}

function lineAmounts(line: string, currency: Currency): number[] {
  return numberTokens(line)
    .map((t) => amountToMinor(t, currency))
    .filter((n): n is number => n !== undefined);
}

// --- dates

const MONTHS: Record<string, number> = {
  // en
  jan: 1, january: 1, feb: 2, february: 2, mar: 3, march: 3, apr: 4, april: 4, may: 5, jun: 6, june: 6, jul: 7, july: 7, aug: 8, august: 8, sep: 9, sept: 9, september: 9,
  oct: 10, october: 10, nov: 11, november: 11, dec: 12, december: 12,
  // tr
  ocak: 1, şubat: 2, subat: 2, mart: 3, nisan: 4, mayıs: 5, mayis: 5, haziran: 6, temmuz: 7, ağustos: 8, agustos: 8, eylül: 9, eylul: 9, ekim: 10, kasım: 11, kasim: 11, aralık: 12, aralik: 12,
  // de
  januar: 1, februar: 2, märz: 3, maerz: 3, juni: 6, juli: 7, oktober: 10, dezember: 12, okt: 10, dez: 12,
  // fr
  janvier: 1, février: 2, fevrier: 2, mars: 3, avril: 4, mai: 5, juin: 6, juillet: 7, août: 8, aout: 8, septembre: 9, octobre: 10, novembre: 11, décembre: 12, decembre: 12,
  // es
  enero: 1, febrero: 2, marzo: 3, abril: 4, mayo: 5, junio: 6, julio: 7, agosto: 8, septiembre: 9, setiembre: 9, octubre: 10, noviembre: 11, diciembre: 12,
};
const MONTH_RE = Object.keys(MONTHS)
  .sort((a, b) => b.length - a.length)
  .join("|");

const DATE_ISO = /\b((?:19|20)\d{2})-(\d{1,2})-(\d{1,2})\b/g;
const DATE_NUM = /\b(\d{1,2})([./-])(\d{1,2})\2((?:19|20)\d{2}|\d{2})\b/g;
const DATE_DMY_TEXT = new RegExp(`\\b(\\d{1,2})(?:\\.|st|nd|rd|th)?\\s*(?:de\\s+)?(${MONTH_RE})\\.?,?\\s*(?:de\\s+)?((?:19|20)\\d{2})\\b`, "giu");
const DATE_MDY_TEXT = new RegExp(`\\b(${MONTH_RE})\\.?\\s+(\\d{1,2})(?:st|nd|rd|th)?,?\\s+((?:19|20)\\d{2})\\b`, "giu");
// Any date, to take it out of a line before its numbers are read as amounts (and to recognise a line that is only a date). The numeric form has no back reference here:
// group numbers shift when the patterns are joined.
const DATE_ANY_SOURCE = [DATE_ISO.source, "\\b\\d{1,2}[./-]\\d{1,2}[./-](?:(?:19|20)\\d{2}|\\d{2})\\b", DATE_DMY_TEXT.source, DATE_MDY_TEXT.source].join("|");
const DATE_ANY = new RegExp(DATE_ANY_SOURCE, "giu");
const hasDate = (line: string) => new RegExp(DATE_ANY_SOURCE, "iu").test(line);

interface DateHit {
  iso: string;
  ambiguous: boolean;
}

function validDay(y: number, m: number, d: number): string | undefined {
  if (m < 1 || m > 12 || d < 1 || d > 31 || y < 2000 || y > 2100) return undefined;
  const dt = new Date(Date.UTC(y, m - 1, d));
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== m - 1 || dt.getUTCDate() !== d) return undefined;
  return `${y}-${String(m).padStart(2, "0")}-${String(d).padStart(2, "0")}`;
}

function datesOf(line: string, usFirst: boolean): DateHit[] {
  const out: DateHit[] = [];
  const add = (iso: string | undefined, ambiguous = false) => iso && out.push({ iso, ambiguous });
  for (const m of line.matchAll(new RegExp(DATE_ISO.source, "g"))) add(validDay(+m[1]!, +m[2]!, +m[3]!));
  for (const m of line.matchAll(new RegExp(DATE_NUM.source, "g"))) {
    const [a, sep, b] = [+m[1]!, m[2]!, +m[3]!];
    const y = m[4]!.length === 2 ? 2000 + +m[4]! : +m[4]!;
    if (sep === ".") add(validDay(y, b, a));
    else if (a > 12) add(validDay(y, b, a));
    else if (b > 12) add(validDay(y, a, b));
    else add(usFirst ? validDay(y, a, b) : validDay(y, b, a), a !== b);
  }
  for (const m of line.matchAll(new RegExp(DATE_DMY_TEXT.source, "giu"))) add(validDay(+m[3]!, MONTHS[m[2]!.toLowerCase()] ?? 0, +m[1]!));
  for (const m of line.matchAll(new RegExp(DATE_MDY_TEXT.source, "giu"))) add(validDay(+m[3]!, MONTHS[m[1]!.toLowerCase()] ?? 0, +m[2]!));
  return out;
}

const DATE_KEYS = /(invoice date|date of issue|issue date|issued|billing date|date|tarih|düzenlenme|duzenlenme|fatura tarihi|datum|rechnungsdatum|fecha|émis|emission|émission)/i;
const DUE_KEYS = /(due|vade|son ödeme|son odeme|fällig|faellig|échéance|echeance|vencimiento|pay by)/i;

function findDate(lines: string[], usFirst: boolean): Field<string> | undefined {
  let best: { iso: string; score: number } | undefined;
  lines.forEach((line, i) => {
    for (const hit of datesOf(line, usFirst)) {
      let score = 0.6 - i * 0.002;
      if (DATE_KEYS.test(line) && !DUE_KEYS.test(line)) score = 0.9;
      else if (DUE_KEYS.test(line)) score = 0.3;
      if (hit.ambiguous) score -= 0.15;
      if (!best || score > best.score) best = { iso: hit.iso, score };
    }
  });
  return best && { value: best.iso, confidence: Math.max(0.1, Math.min(0.95, best.score)) };
}

// --- the amounts: total and tax

const TOTAL_STRONG =
  /(grand total|amount due|total due|balance due|total amount|amount paid|total paid|genel toplam|ödenecek tutar|odenecek tutar|ödenecek|toplam tutar|vergiler dahil|gesamtbetrag|zu zahlen|endbetrag|rechnungsbetrag|total à payer|total a payer|montant total|net à payer|importe total|total a pagar)/i;
const TOTAL_WEAK = /(total|toplam|summe|gesamt|montant|importe|tutar|betrag)/i;
const NOT_TOTAL = /(sub ?total|ara toplam|zwischensumme|sous-total|subtotal|excl|hariç|haric|exkl|hors|sin impuestos|net amount|tax total|total tax|toplam kdv|kdv toplam|toplam vergi|mwst|vat|kdv|tva|iva|discount|indirim)/i;
const TAX_KEYS = /(sales tax|tax|vat|kdv|mwst|ust\.?|steuer|tva|iva|gst|ötv|otv)/i;
const NOT_TAX_AMOUNT = /(excl|hariç|haric|exkl|hors|sin |net |matrah|base|taxable|ara toplam|subtotal|tax id|tax no|vergi no|vergi dairesi|vat id|vat no|vat number|ust-?id|tva intra|nif|cif)/i;

function findTotal(lines: string[], currency: Currency, hasCurrency: boolean): Field<number> | undefined {
  let best: { minor: number; score: number } | undefined;
  const consider = (minor: number, score: number) => {
    if (minor <= 0) return;
    if (!best || score > best.score || (score === best.score && minor > best.minor)) best = { minor, score };
  };
  lines.forEach((line, i) => {
    const strong = TOTAL_STRONG.test(line);
    const weak = TOTAL_WEAK.test(line);
    if (!strong && !weak) return;
    if (NOT_TOTAL.test(line) && !strong) return;
    const here = lineAmounts(line, currency);
    const amounts = here.length ? here : lineAmounts(lines[i + 1] ?? "", currency);
    const minor = amounts.at(-1);
    if (minor !== undefined) consider(minor, strong ? 0.9 : 0.7);
  });
  if (best) return { value: best.minor, confidence: hasCurrency ? best.score : best.score - 0.15 };
  // No total line: the biggest figure that sits next to a currency mark is the best guess.
  let max = 0;
  for (const line of lines) {
    if (!/[€$£₺₹¥]|\b[A-Z]{3}\b|\bTL\b/.test(line)) continue;
    for (const a of lineAmounts(line, currency)) max = Math.max(max, a);
  }
  return max > 0 ? { value: max, confidence: 0.4 } : undefined;
}

function findTax(lines: string[], currency: Currency, total: number | undefined): Field<number> | undefined {
  const found: number[] = [];
  for (const line of lines) {
    if (!TAX_KEYS.test(line) || NOT_TAX_AMOUNT.test(line) || /total (incl|excl)/i.test(line)) continue;
    const minor = lineAmounts(line, currency).at(-1);
    if (minor === undefined || minor <= 0) continue;
    if (total !== undefined && minor >= total) continue;
    found.push(minor);
  }
  if (found.length === 0) return undefined;
  // Several tax lines (two rates) add up; when they would add up to the whole total, one of them was not a tax amount, and the biggest one that fits is the guess.
  const sum = found.reduce((a, b) => a + b, 0);
  if (total === undefined || sum < total) return { value: sum, confidence: found.length > 1 ? 0.65 : 0.75 };
  return { value: Math.max(...found), confidence: 0.5 };
}

// --- the vendor

const COMPANY_SUFFIX = /\b(inc\.?|llc|ltd\.?|limited|gmbh|ag|corp\.?|corporation|co\.|s\.a\.|sas|b\.v\.|a\.ş\.|aş|a\.s\.|ltd\. ?şti\.?|şti\.?|san\. ?ve tic\.?|oy|ab|plc|pty|s\.r\.l\.|srl|sl|ug|kg)(?=\W|$)/i;
const NOT_VENDOR =
  /^(invoice|fatura|receipt|makbuz|fiş|fis|rechnung|facture|factura|bill|tax invoice|e-?arşiv|e-?fatura|page|sayfa|seite|date|tarih|datum|total|toplam|bill to|billed to|ship to|sayın|dear|customer|müşteri|amount|description|qty|quantity|no|number|numara)\b/i;
const VENDOR_LABEL = /^(?:from|seller|vendor|sold by|billed by|issued by|satıcı|satici|unvan|ünvan|firma|verkäufer|verkaeufer|vendeur|vendedor)\s*[:\-]\s*(.+)$/i;

function findVendor(lines: string[], filename?: string): Field<string> | undefined {
  for (const line of lines.slice(0, 40)) {
    const m = VENDOR_LABEL.exec(line);
    if (m?.[1] && m[1].length >= 2) return { value: m[1].trim().slice(0, 120), confidence: 0.8 };
  }
  const head = lines.slice(0, 12);
  const candidate = (l: string) =>
    l.length >= 2 && l.length <= 80 && /\p{L}{2}/u.test(l) && !NOT_VENDOR.test(l) && !/@|https?:|www\./i.test(l) && !hasDate(l) && !/^\W*\d[\d\s./,-]*\W*$/.test(l) && !/\d{5,}/.test(l);
  for (const l of head) {
    if (candidate(l) && COMPANY_SUFFIX.test(l)) return { value: l.slice(0, 120), confidence: 0.85 };
  }
  const first = head.find(candidate);
  if (first) return { value: first.slice(0, 120), confidence: 0.5 };
  const name = (filename ?? "")
    .replace(/\.[A-Za-z0-9]{2,4}$/, "")
    .replace(/[_-]+/g, " ")
    .replace(/\b\d[\d ]*\b/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  return name.length >= 2 ? { value: name.slice(0, 120), confidence: 0.2 } : undefined;
}

// --- the whole

/** Suggests the fields of an expense from the text of a document. */
export function parseReceipt(text: string, opts: ParseOptions = {}): ParsedReceipt {
  const lines = normalize(text.slice(0, 200_000));
  const out: ParsedReceipt = {};
  if (lines.length === 0) return out;
  const joined = lines.join("\n");
  const seen = detectCurrency(joined);
  const currency: Currency = seen?.currency ?? opts.defaultCurrency ?? "USD";
  if (seen) out.currency = { value: seen.currency, confidence: seen.confidence };
  else if (opts.defaultCurrency) out.currency = { value: opts.defaultCurrency, confidence: 0.3 };
  const vendor = findVendor(lines, opts.filename);
  if (vendor) out.vendor = vendor;
  const date = findDate(lines, currency === "USD");
  if (date) out.date = date;
  const total = findTotal(lines, currency, seen !== undefined);
  if (total) out.amountMinor = total;
  const tax = findTax(lines, currency, total?.value);
  if (tax) out.taxMinor = tax;
  return out;
}

/** The share of a field a vendor rule needs to be trusted: used by the inbox to decide what may be approved without a person. */
export function confident(f: Field<unknown> | undefined): boolean {
  return f !== undefined && f.confidence >= LOW_CONFIDENCE;
}
