# Phase 40 — Many currencies and every time zone

## Goal

An app sells in more than one currency, and a tenant can pick any currency and any time zone the world uses. Before this phase the supported list was seven currencies; the data model already let a plan version carry a fixed fee per currency and a subscription carry its own currency (phase 13), so the work is the table, the zones and the early refusals.

## Scope

- [x] **Currency table.** `pkg/money` lists every active ISO 4217 currency that is money (153 codes) with its minor-unit exponent: 0 (JPY, KRW, VND, CLP, ISK, UGX, XOF, XAF, XPF…), 2 (most), 3 (KWD, BHD, OMR, JOD, TND, IQD, LYD). Funds codes, precious metals, test codes and withdrawn currencies (BGN since 2026-01-01, HRK) are not accepted.
- [x] **Time zones.** Any name `time.LoadLocation` knows. `pkg/tzdata` embeds the IANA database (`time/tzdata`) and the six services that load a tenant's zone import it, because the images are distroless and have no zoneinfo.
- [x] **Early refusal.** A subscription that names (or is routed to) a provider that cannot charge its currency fails at creation with `plans.subscription.provider_currency` (it used to be an unkeyed `FailedPrecondition`). Routing rules, provider moves and the hosted payment methods already checked `Capabilities.CanCharge`.
- [x] **OpenAPI.** `currency` is `^[A-Z]{3}$` (no enum of seven); messages no longer list the supported codes.
- [x] **Console list.** `web/admin/src/shared/lib/currencies.ts` carries the same 153 codes; `tests/currencies_test.go` fails when the two lists differ.
- [x] **Tests.** Unit (exponents, rejected codes, zones without a host database), plans-svc integration on Postgres (accounts in BRL/INR/KRW/BHD/NPR/ZAR with half-hour and quarter-hour zones, one plan sold in KRW, KWD, BRL, USD and TRY, a currency without a fee, iyzico refusing KRW with its key).

## What an app can sell in

- The **tenant currency** is the app environment's default and reporting currency; it is fixed at creation (ADR 0010).
- A **plan version** has a monthly fee in the tenant currency (`base_fee_minor`) and `fees[]` in any other currency and interval. Usage prices per currency are `currency_prices[]`.
- A **customer** has an optional billing currency; a **subscription** has exactly one currency, which is the currency of every invoice it makes. An invoice never mixes currencies.
- **Credit packs, coupons, payment links and expenses** carry their own currency.
- **Dashboards, accounting and reports** group by currency; nothing adds, converts or compares amounts of two currencies (there is no exchange rate in the platform).

## Not done

- A plan cannot be published without a fee in the tenant currency (0 is allowed, which makes it free there). A "sold only in EUR" flag would be a new field; the console shows currencies with a fee as the ones the plan is sold in.
- The exponent table is static: a currency whose exponent ISO changes needs a release.
