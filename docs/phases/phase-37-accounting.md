# Phase 37 — Accounting: income and expenses

## Scope

- [x] **Plan** (this file).
- [x] **Expenses** (billing-svc module `accounting`, ADR 0020 shape: `model/`, `command/`, `query/`, `handler.go`).
  - [x] Migration `00023_accounting.sql`: table `expenses` under RLS; an index on `saga_events` for the income queries.
  - [x] sqlc queries (`accounting.sql`), no SQL assembled at run time.
  - [x] Commands: create, update, void (with a reason). Queries: list (keyset pages), summary.
  - [x] `BillingService` RPCs `CreateExpense`, `UpdateExpense`, `VoidExpense`, `ListExpenses`, `GetAccountingSummary`; error keys `billing.accounting.*`.
- [x] **API.** `GET/POST /v1/expenses`, `PUT/DELETE /v1/expenses/{id}`, `GET /v1/accounting/summary`, `GET /v1/accounting/export.csv`; OpenAPI.
- [x] **Console.** An "Accounting" tab: period picker, a block of cards per currency, a donut of expenses by category, income-against-expense bars, the expense table with add, edit and void, an app filter, CSV export. Six languages.
- [x] **Tests.** Unit (recurrence, summary arithmetic, currency isolation, period rules), gateway handlers, integration against Postgres with RLS, vitest for the period and chart geometry, mocked Playwright.

## Decisions

- **Income is read, not copied.** Nothing about income is stored in the accounting tables. The summary reads the invoices of billing-svc, so a correction in the invoice is a correction in the books. The feeding tables are in the next section.
- **Per currency, never across.** A summary is a list of blocks, one per currency that has income or an expense in the period. Amounts of different currencies are never added, converted or compared: there is no exchange rate anywhere in the platform, and a total in a made-up unit would be worse than none. A currency with only expenses has a block with a negative profit.
- **Profit** is `net income − expenses`, where `net income = gross income − tax − refunds`. Tax is a pass-through (collected for the tax office), so it is not income. Provider fees are inside `expenses` (see below), not subtracted a second time.
- **Recurring expenses are one row.** `recurring` is `none`, `monthly` or `yearly`, with an optional `recurring_until` (inclusive). The occurrences inside a period are worked out when the summary is asked for (`model.Expense.Occurrences`, a pure function): no cron, no materialised rows, nothing to drift. Every occurrence is computed from the first day, so an expense that starts on the 31st falls on 28 February (29 in a leap year) and on 31 March again; it does not slide to the 28th for good. A yearly expense on 29 February falls on 28 February in other years. Editing a recurring expense changes all its occurrences, past ones included; voiding it voids the series.
- **Edit and void.** An expense is never deleted. `VoidExpense` needs a reason (1 to 200 characters), keeps the row and takes it out of every summary; a voided expense cannot be edited or voided again (`billing.accounting.expense_voided`). Edits are allowed and leave `updated_at` as the trace; the history of edits is not kept.
- **Days are UTC days.** An expense has a date, an invoice has a moment; the bucket of an invoice is the UTC day of its payment. A tenant with a time zone far from UTC will see an evening payment on the next day. The console sends the reader's calendar days for the period, which can differ from the UTC days by a day at the edges.
- **Series are dense.** Every day (or month) of the period has a point, zero when nothing happened, so a chart and a CSV need no filling. A day series is at most 366 days, a period at most five years.
- **App filter.** A tenant is one app environment, so its income is already an app's. `app_id` on an expense tags a cost to an app of the account (an expense entered under the live tenant of app A, or a shared cost booked to app B); the filter limits expenses only and the response and the console say so. Income across several tenants of an account is not summed.

## Where each line comes from

| Line | Source |
| --- | --- |
| Gross income | `invoices.total_minor` of every invoice whose saga reached `PAID` in the period (`saga_events.to_state = 'PAID'`, its `at` is the day). An invoice that was refunded or voided later still counts on the day it was paid, and its refund counts on the day it was refunded. Invoices without a saga (a draft, an invoice no saga took up) are not income. |
| Tax | The sum of `invoice_lines.amount_minor` where `kind = 'TAX'` of those invoices. |
| Refunds | Credit notes (`credit_notes.total_minor`, on `created_at`) against an invoice whose saga reached `PAID`, whatever their disposition (back to the card, or to the credit balance), plus whole invoices that were refunded (`saga_events.to_state = 'REFUNDED'`, the invoice total, on the day of the event). A credit note against an invoice nobody paid is not a refund of money and is left out. The refund is the gross amount; the tax part of it is not taken out of the tax line. |
| Provider fees | **Not recorded by Quotify.** `payments-worker`'s `payment_ledger` stores charges, refunds, disputes and payouts with their amounts but no commission, and provider webhooks do not carry it in a form every adapter reads. The line shows what the tenant entered as expenses of the category `payment_fees`. When there is none for the period the API says `provider_fees_recorded: false` and the console shows "Not recorded", which is not the same as zero. The CSV writes `not_recorded` in that cell. |
| Expenses | `expenses`, live rows, every occurrence in the period. |
| Expense by category | The same occurrences grouped by `category`, largest first. |

## Limits

- Provider commission is manual until a provider's report or webhook is read for it.
- No cash-basis versus accrual choice: income is by payment date, an expense by its date.
- The tax line is the tax of the invoices; a refunded tax part is not returned to it.
- Income of several tenants (apps) of one account is not combined.
- No receipts or attachments on an expense, no budget, no approval flow.
