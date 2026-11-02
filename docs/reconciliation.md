# Reconciliation: a queue of differences, and "paid" is not "settled"

Plan 22 G7. Built 2026-11-02 on `feat/reconciliation-health`. Everything below ran against fake providers and a Postgres in Docker; **nothing ran against a real Stripe, Paddle or iyzico account** (see "Not verified").

## What it compares

Three things that should agree and do not always:

1. **Billing and the ledger.** An invoice billing calls paid should have one successful charge of its total in the payment ledger. Five kinds: `paid_without_charge`, `amount_mismatch`, `duplicate_charge`, `charge_without_paid`, `conflicting_outcomes` (as before: `reconciliation/query`).
2. **The ledger and the provider's settlement.** A payment that succeeded is the ledger's word. The money is the tenant's once the provider settles it, minus its fee. Three kinds, when settlement lines are known (below): `settlement_missing`, `settlement_mismatch`, `fee_mismatch`.
3. **The fee and what the tenant's contract says it should be**, when an operator entered a fee rule.

`GET /admin/v1/tenants/{id}/reconciliation?period=2026-10` is still the comparison alone: it computes and keeps nothing.

## The queue

A **run** compares one tenant and period and keeps what it finds in `reconciliation_discrepancies` (payments_db, migration `00003`; tenant twin `tenant/00004`):

| Status | Meaning |
|---|---|
| `open` | found, nobody has looked |
| `explained` | known, with a note and an owner; stays in the queue |
| `resolved` | ended: by an operator (`ledger_fact`, `credit_note`, `refunded_at_provider`, `corrected_in_billing`, `not_an_error`, `other`) or by a later run that no longer finds it (`cleared`) |

A discrepancy is named by a **fingerprint** (kind, invoice, provider, provider reference), so the same difference found again is the same row: a repeated run writes no row and no event. Rules a run follows, all tested (`TestReconciliationRunKeepsEachDiscrepancyOnce`):

- found for the first time: opened, event `detected`;
- still found: refreshed (`last_seen_at`, amounts), no event;
- **resolved and found again: reopened** (event `reopened`), because resolving a difference does not make it go away;
- open or explained and **no longer found: resolved as `cleared`** (event `cleared`, actor `system`), but only when the run saw everything. A run that found more than it keeps (5,000) is `truncated` and clears nothing.

Every change is a row in `reconciliation_events` (append-only: no UPDATE is granted, a DELETE is refused by trigger, as for the ledger; only a sandbox reset of a sandbox tenant or a tenant move deletes). `reconciliation_runs` keeps one row per run (who started it, what it saw and changed, why it failed).

### Resolving, and what a resolution may do

Resolving **records** what was done elsewhere; it changes nothing at a provider and nothing in an invoice. The only side effect any resolution has is the one below.

- `ledger_fact` (one discrepancy at a time, `paid_without_charge` only): the operator names the provider (an account key) and the provider's reference of the payment, checked at the provider first. **One fact** is appended to the payment ledger through the existing append: a successful charge, source `reconciliation`, of the invoice's own total and currency (never the request's), the discrepancy and the actor in its detail. The same fact twice is one row (`ledger_fact_once`). The next run then finds the invoice and the ledger agree. Not offered in bulk: it needs a reference per payment.
- `credit_note`: the operator issued a credit note through the credit note API (`POST /admin/v1/invoices/{id}/credit-notes`, which asks the provider through its own saga) and names it in the note. **Reconciliation does not request credit notes itself**: the existing command is safe because billing owns it; a second path to it from here would not be. (Default, not agreed; the plan said "may".)
- the others write nothing.

A note is required for every explanation and resolution. The actor is the gateway's `X-Payrudder-Actor` (the signed-in operator's address; `operator` without one).

### API (operator routes, `/admin/v1/tenants/{tenant_id}/...`)

| Route | Does |
|---|---|
| `POST reconciliation/runs?environment=` `{period}` | compare and keep (`runReconciliation`) |
| `GET reconciliation/discrepancies?period=&status=&kind=&owner=&invoice_id=&limit=&page_token=` | the queue, newest first, filtered on the server; also the counts per status and kind and the last run |
| `GET reconciliation/discrepancies/{id}` | one with its events |
| `POST .../{id}/assign` `{owner}`, `/explain` `{note, owner}`, `/resolve` `{resolution, note, owner, ledger_fact}` | the operator's actions |
| `POST reconciliation/discrepancies/resolve` `{ids (1-100), resolution, note, owner}` | many with one resolution; each its own change and event; the answer says `resolved`, `unchanged`, `not_found` or `failed` per id |
| `GET settlement/payments?period=` | the period's successful payments with their settlement state |
| `POST settlement/import?environment=&provider=` (CSV) | import a report |
| `POST settlement/sync?environment=` `{provider, since}` | read a provider's own settlement (Stripe) |
| `GET/PUT/DELETE settlement/fee-rules` | the expected fee per provider and currency (the console's fee rules card) |

The console's Payments page (operator) has the queue (filters, sort, search, bulk resolve that asks first, a sheet with the history), the "Paid and settled" card and the **fee rules** card (list, filters, a detail sheet, add, edit, remove; docs/13-admin-ui.md).

### The schedule

`payments-worker` runs the reconciliation itself every `PAYMENTS_RECONCILE_INTERVAL` (**6 h, default, not agreed**; `0` turns it off) for every tenant that took a live payment (a successful live charge in the ledger; in cells mode each serving tenant of each cell, a moving one is skipped and found next pass), for **the current and the previous period** (a payment of the last days of a month is settled and an invoice paid after its period closed). It needs `PAYRUDDER_BILLING_ADDR` and the secrets key, like the operator endpoints. A tenant whose run fails is logged and recorded in `reconciliation_runs`; the others go on. Two replicas, or an operator beside the schedule, only repeat work: a run takes an advisory lock per tenant and period and is idempotent. Sandbox tenants are not scheduled (an operator can run one by hand). Counters: `reconciliation_runs{started_by,result}`, `reconciliation_discrepancies_opened{kind}`, `reconciliation_discrepancies_resolved{resolution}`. The existing `payment_discrepancies` counter (a charge's outcomes disagree at notification time) and its alert `PaymentDiscrepancy` are unchanged; **no alert uses the new counters yet** (a rule such as "opened > 0 over 1 d" belongs with the alert rules of plan 22 G6, and was not added without running promtool).

## Paid is not settled

Per successful provider payment (bank transfers are settled by hand and have no line to wait for), with the lines known:

| State | Meaning |
|---|---|
| `not_reported` | paid; no settlement line names it |
| `pending_payout` | the provider reported it (gross, fee, net, `available_on`); not paid out |
| `settled` | the provider paid it out: `settled_at`, `settlement_batch` |

`expected_fee` is `gross * rate_bps / 10000` (half up) `+ fixed_minor` from the tenant's fee rule for the provider and currency. **Payrudder knows no provider's prices**: with no rule there is no expected fee and no `fee_mismatch`. A difference of more than 1 minor unit is a finding.

Findings, only when the lines can support them:

- `settlement_missing`: paid more than the **lag** ago (`PAYMENTS_SETTLEMENT_LAG`, **7 days, default, not agreed**) **and** inside the period the provider's lines cover (between the first and the last payment line read for it). A report that never reached back that far says nothing, and a provider with no lines at all raises nothing.
- `settlement_mismatch`: same currency and the line's gross is not the amount paid (a conversion changes the amount, so a different currency is not compared).
- `fee_mismatch`: with a fee rule.

For these `ledger_minor` is what the ledger holds (for a fee: the fee expected) and `settled_minor` what the provider reported (for a fee: the fee kept).

### Settlement CSV

`POST settlement/import` takes a report. A header row, then one line per payment or payout; columns in any order and any case:

| Column | |
|---|---|
| `reference` | required; the report's id of the line. The same reference read again is the same line (it fills in what was unknown) |
| `payment_ref` | required for a payment: the reference Payrudder's ledger holds for it (the Reference column of the Payments page) |
| `currency`, `gross`, `occurred_on` | required. Amounts are decimals in the currency's **major** unit (`10.50`, `-3`; JPY `1000`), never more decimals than the currency has |
| `provider` | an account key (`stripe`, `stripe~eu`); optional when the request names `provider` |
| `kind` | `payment` (default) or `payout` |
| `fee` | default 0; `net` default `gross - fee` (a `net` that is not `gross - fee` is refused) |
| `available_on`, `settled_on` | RFC 3339 or `2026-10-31` |
| `batch` | the payout or settlement reference |

The whole file or nothing (400 `payments.settlement.bad_csv`, naming the first lines); 10,000 lines and 5 MB at most (the gateway lets this one route take 5 MB). It works for any provider and for a bank statement turned into this shape. iyzico's "settlement files" page names the fields `paymentTxId`, `settlementDate`, `settlementAmount`, `settlementReferenceCode`, `MerchantPayoutTotal`, `iyziCommissionFee` and the others but **not their file format or how they are delivered** (read 2026-11-02), so no importer for that format is built: map the columns to the ones above.

### Reading a provider's own settlement

`payments.SettlementReader` (`pkg/payments/settlement.go`) is the interface; an adapter implements it only when its provider documents an API for it. **Read on 2026-11-02:**

| Provider | Documented | Built |
|---|---|---|
| **Stripe** | `GET /v1/balance_transactions` (`amount`, `fee`, `net`, `available_on`, `status`, `reporting_category`; `source`, expandable; filter `payout=po_...` "for automatic Stripe payouts only"), `GET /v1/payouts` (`arrival_date`, `status`, `reconciliation_status`: `completed` means its balance transactions can be listed), the Charge's `payment_intent` | **yes**: `stripe.Client.Settlement`. A balance transaction with `reporting_category` `charge` is the money of a payment (matched by the PaymentIntent, which is the ledger's reference); a paid payout whose `reconciliation_status` is `completed` marks its payments `settled` on its `arrival_date` in its batch; the payout is a line itself. Refunds, disputes and fees are other reporting categories and are **not read yet**. Manual and instant payouts cannot be listed per payout: their payments stay `pending_payout`. `POST settlement/sync` runs it (400 days at most, 45 by default) |
| **Paddle** | `GET /transactions`: `details.totals.fee` and `earnings` are `null` until the transaction is `completed`; `payout_totals` likewise; `payments[].captured_at`. `GET /adjustments`: `totals.fee`, `earnings`, `payout_totals` | **no**: neither page documents a payout date or batch per transaction, so "settled" cannot be established from them; they would give the fee and earnings only (an expected-fee check). Paddle documents `payout.*` notifications; the webhook handler ignores them (matrix). Import Paddle's payout report as CSV |
| **iyzico** | `GET /v2/reporting/payment/transactions?transactionDate=YYYY-MM-DD&page=` (`transactionType`, `iyzicoCommission`, `iyzicoFee`, `merchantPayoutAmount`, `settlementCurrency`, `afterSettlement` for refunds only) and `GET /v2/reporting/payment/details` | **no**: the pages give no payout date, schedule or delay (and name the same concept differently in the two endpoints), so "settled" cannot be established; the amounts could feed fee checks only. Import the settlement file as CSV |

Stripe's reading is built against a fake server written from those pages; whether `expand[]=data.source` is honoured on this list, the payment intent of a charge that has none, and the real shape of a payout's lines are **UNVERIFIED** until a Stripe account is used. Stripe's documentation of the reporting-category list says only that `charge` includes the types `charge` and `payment`; Payrudder does not read the others.

## Defaults (not agreed)

Schedule 6 h; periods current and previous; settlement lag 7 days; fee tolerance 1 minor unit; sync window 45 days (400 at most); a run keeps 5,000 discrepancies; bulk resolution 100; no alert on the new counters; no automatic credit note; "explained" stays in the queue until resolved or cleared.

## Not verified

No run against a real Stripe, Paddle or iyzico account or a real bank report; the Stripe reader and every number of the settlement are tested against fakes. The console was checked with the mock gateway in Chrome (no screenshots were read by a person). The schedule ran in tests with a fixed clock, never as a service for hours. Cells mode: the new integration tests passed with `ITEST_CELLS=1`; a populated cell was not moved.
