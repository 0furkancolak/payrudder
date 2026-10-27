/**
 * The text of an invoice line in the reader's language. The billing service writes a line's description in English when it drafts the invoice
 * ("Base fee (monthly) × 2 seats — pro — 2026-10-27 to 2026-11-27") and keeps that text with the invoice, so the console reads the shapes it knows back into
 * their parts and words them from `invoice.lineText.*`, with the dates in the reader's format. A description it does not recognise (a tax line, a fee
 * somebody typed into a payment link, a shape newer than the console) is shown as written.
 */
type Translate = (key: string, named?: Record<string, unknown>) => string;

const DAY = "(\\d{4}-\\d{2}-\\d{2})";
const BASE_FEE = new RegExp(`^Base fee \\((monthly|yearly)\\)(?: [×x] (\\d+) seats)? — (.+) — ${DAY} to ${DAY}$`);
const UNUSED = new RegExp(`^Unused time on (.+) — ${DAY} to ${DAY}$`);
const COUPON = /^Coupon (.+)$/;
const USAGE = /^Usage — ([^—]+?) — (.+)$/;
const LATE = /^Late usage — ([^—]+?) — ([^()]+?) \((.+)\)$/;

/** `day` turns a 2026-10-21 into the reader's text for that calendar day. */
export function lineText(description: string | undefined, t: Translate, day: (ymd: string) => string): string {
  const text = description ?? "";
  let m = BASE_FEE.exec(text);
  if (m) {
    const [, interval, seats, plan, from, to] = m;
    const parts = { interval: t(`invoice.lineText.${interval}`), plan, from: day(from!), to: day(to!) };
    return seats ? t("invoice.lineText.baseFeeSeats", { ...parts, seats }) : t("invoice.lineText.baseFee", parts);
  }
  if ((m = UNUSED.exec(text))) return t("invoice.lineText.unused", { plan: m[1], from: day(m[2]!), to: day(m[3]!) });
  if (text === "Base fee") return t("invoice.lineText.baseFeePlain");
  if (text === "Base fee: price change") return t("invoice.lineText.priceChange");
  if ((m = LATE.exec(text))) return t("invoice.lineText.late", { meter: m[1], origin: m[2], rest: m[3] });
  if ((m = USAGE.exec(text))) return t("invoice.lineText.usage", { meter: m[1], rest: m[2] });
  if ((m = COUPON.exec(text))) return t("invoice.lineText.coupon", { code: m[1] });
  return text;
}
