# ADR 0028 — Four money rules: tax on credit notes, a negative total, the trial's first period, the year of a number

- Status: Accepted as the implementer's default (**not agreed with the owner**; [billing-svc-status](../code-quality/billing-svc-status.md) listed each of them as an owner decision and plan 22 as "known money-logic findings")
- Date: 2026-11-02

## Context

Four findings of the billing-svc review changed what a customer is charged or what a tenant reports, so each waited for a rule. The owner has since said not to ask: where a plan
lists a decision, the implementer writes the sensible default down as "default (not agreed)" and builds behind it. This is that record. Every rule is **additive**: an invoice or a
credit note already written is never rewritten (the database forbids it, migration `00003`), and the figures of a row written before the rule are what they were.

## 1. A credit note gives back the tax of what it credits

**Rule.** The tax of a credit note is the note's share of the invoice's total, taken from the invoice's tax (the sum of its `TAX` lines), rounded half to even like the tax itself
(`creditnotes/model.TaxShare`). The note that credits everything still left of the invoice gives back everything still left of the tax, so the notes of one invoice add up to its tax
exactly whatever their sizes; no note gives back more than it credits or than the invoice has left. A note carries it as `credit_notes.tax_minor` (migration `00006`, default 0)
and in the API (`CreditNote.tax_minor`) and the `credit_note.*` events (`tax_minor`).

- **The e-document of the note** shows exactly that tax: its `TAX` line is `tax_minor` and the other lines carry the rest of the credited amount (`ProrateLines`, each group split by
  largest remainder). Before, every line was scaled alike and the tax line was whatever that rounded to.
- **There is no credit note PDF.** `invoice-worker` renders invoices only. A credit note is shown as a record (API, console, e-document).
- **Accounting.** `AccountingRefunds` returns the tax part of each credit note; `Tax` in a period is the tax of the invoices paid in it **less the tax its credit notes gave back**
  (negative in a period that gave back more than it collected), so 120 paid with 20 tax and 120 credited with 20 tax is `Tax` 0 and `Net` 0, not -20. `Refunds` stays gross. The
  period of a reversal is the credit note's own date, as for the refund itself.
- **Not covered, still the owner's call:** the refund of a *voided* invoice and the credit balance a void of a hand-paid invoice makes still count their whole amount as a refund and
  leave the invoice's tax in `Tax` (their tax part is 0); a full refund of a taxed invoice by a void therefore still makes `Net` negative by the tax. Credit notes issued before
  `00006` carry no tax; the note that closes such an invoice takes all the tax not yet returned.

## 2. A negative total is carried to the credit balance

**Rule.** When the lines of a regular invoice add up to less than nothing (late-usage corrections that outweigh everything else, [ADR 0027](0027-event-corrections.md)), the invoice
charges 0 as before, and what they came to below zero becomes a credit balance of the customer in the invoice's currency, applied to their next invoices like any other credit
(`ApplyCredits`, oldest first). Details:

- **Tax is on the net, on both sides of zero.** A base below zero gives a **negative `TAX` line** of the size the tax of that amount would have been (`rating.NetTaxLine`; the same rule
  as a credit that returns a taxed charge, plan changes and re-rating already follow). A customer who paid 100 plus 20 tax and has the usage taken back is credited 120, not 100.
  A provider-run subscription has no tax line, so none.
- **One credit per invoice, append-only.** `credit_balances.source_surplus_invoice_id` (migration `00006`) with a unique index; the row is written in the transaction that writes the
  draft, and a retried run that finds the draft already there writes no second. `source_invoice_id` is not reused: it means "money received for an invoice that was voided" and the
  accounting refunds read it as that. A void of an invoice with a surplus does not take the credit back (the surplus was owed to the customer whatever happens to the invoice).
- **Visible.** `Invoice.credit_surplus_minor` and `UpcomingInvoice.credit_surplus_minor` (frozen in the invoice's snapshot as `credit_surplus_minor`), a sentence under the total in
  the console's invoice sheet and the customer's upcoming-invoice card, and a "Carried to your credit balance" row under the total of the invoice PDF in five languages.
- **Not changed:** the total stays `>= 0` (`invoices.total_minor` check); the lines keep their amounts, so a document whose lines sum below zero while its total is 0 is still what
  an e-invoice integrator receives for such an invoice (**UNVERIFIED**: whether an integrator accepts an invoice with negative lines and a negative TAX line; before this change the
  lines were the same except for the missing TAX line).

## 3. The year of a number is the year in the tenant's time zone

**Rule.** `INV-<year>-<n>` and `CN-<year>-<n>` take their year from the moment of numbering on the wall clock of the tenant's time zone (frozen into the invoice's snapshot as
`time_zone` when the draft is made; a credit note uses the zone of the invoice it corrects). A draft made before the zone was frozen, or a zone that does not load, is dated by UTC,
which is how every number was dated until now (`invoices/model.NumberYear`).

- **Sequences.** `invoice_number_seq` and `credit_note_seq` are already one row per `(tenant, year)`, so each year is a series of its own and stays **gap-free and unique** whichever
  zone dates it: an invoice finalized at 01:30 on 1 January in Istanbul (22:30 UTC on 31 December) is `INV-2027-000001`. The consequence at the boundary: for the hours between the
  tenant's new year and UTC's, new numbers open the new year's series while a draft made without a zone (or a tenant that moved its zone) may still close the old one, so numbers
  of two years can interleave for a few hours once. That is the price of dating by the tenant's calendar; the series themselves never skip or repeat.
- **Not decided here:** whether a country's rule asks for a numbering of its own (per series, per branch); the integrator's legal number is separate.

## 4. A trial that ends inside a period charges the first period from its end

**Rule.** A plan version has `prorate_after_trial` (migration plans-svc `00015`). When it is true and the subscription's trial ends inside a billing period, the fixed fee of that
period is `fee × (period end - trial end) / (period end - period start)` (`pkg/subscription.Portion`, half to even) instead of the whole fee; the line says the days it covers
(`Base fee - <plan> - 2026-10-12 to 2026-11-01`, with its service period). A trial that runs past the period still waives the fee, a trial that ended before it changes nothing, and
a trial ending exactly at the period's end charges nothing for that period.

- **Only new versions.** Every version that exists when the migration runs is `false` (it charged the whole period and keeps doing so); `PublishPlanVersionRequest.prorate_after_trial`
  left out means `true` for a new version. The flag belongs to the version, so an old subscription on an old version is billed as before and an invoice already written is never touched.
  The console's new-package form has the box (on by default, shown when the package has a trial) and the package sheet shows which rule a version has.
- **Re-rating** prices the period under the terms of the version it re-rates to, the trial rule included (`targetFor`): re-rating a period from a whole-period version to a
  prorating one therefore credits the difference. The correction is a new document, as always.
- **Not covered:** a fee invoiced in advance (kind `FEE`) is dated by plans-svc (`fee_due_at`), not by this rule; a yearly fee's first period after a trial is plans-svc's as before.

## Verification

Each rule has a test that fails without it (checked once by reverting the rule): `TestBilling/CreditNotesGiveBackTheTaxInProportion`, `TestBilling/ANegativeTotalBecomesACreditBalance`,
`TestBilling/ATrialEndingInsideThePeriodChargesFromItsEnd`, `TestBilling/InvoiceNumberYearIsTheTenantsYear` and `CreditNoteNumberYearIsTheTenantsYear`, with unit tests of `TaxShare`,
`ProrateLines`, `NetTaxLine`, `FeeFrom`, `NumberYear`, `accounting.Build` and the PDF row. **Not verified:** acceptance of any resulting document by a tax authority or an
integrator, an accountant's reading of the tax figures, a console screenshot.
