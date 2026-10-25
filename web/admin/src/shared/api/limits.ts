/**
 * How many rows the console asks the API for when a page shows a list. A list longer than this is cut at the server's page, so the number is a decision to
 * make in one place (and to raise together), not a literal in each API file.
 */
export const PAGE = {
  /** A list shown whole on one page (customers, tenants, expenses, ledgers, reports). */
  whole: 200,
  /** A long table (invoices, subscriptions, credit notes, a customer's payments, a dead-letter batch). */
  long: 100,
  /** A history (webhook deliveries, a credit ledger). */
  history: 50,
  /** A list the page pages through (payment links). */
  paged: 25,
  /** The few documents or messages of one thing (the e-documents of an invoice, the messages of one queue). */
  few: 20,
} as const;

/** How often (ms) a page looks again at something that changes while it is open. */
export const POLL = {
  /** Something the person just caused and waits for (an invoice moving through its saga). */
  quick: 2_000,
  /** A document or a delivery that is still pending. */
  pending: 5_000,
  /** Figures that drift on their own (a dashboard, an overview). */
  figures: 60_000,
} as const;
