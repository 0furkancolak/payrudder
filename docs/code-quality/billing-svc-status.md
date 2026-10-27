# billing-svc: what became of the findings (phase 49, step 3)

Every finding of [billing-svc.md](billing-svc.md) (99), the billing-svc part of [cqrs-plans-billing.md](cqrs-plans-billing.md) and [logic-billing-svc.md](logic-billing-svc.md) was checked against the code. Numbers count the bullets of each section in the report's order. PRs are `#n` of this repository.

Status: **Fixed** (the PR), **Earlier** (already true on main when checked), **Not real** (read the code, the claim does not hold), **Partly**, **Deferred** (real, left on purpose, with the reason), **Open** (a logic finding that stays a bug).

| PR | What |
|---|---|
| #149 | `saga` becomes `sagas/model` + `sagas/command` |
| #155 | credit notes become `creditnotes/{model,command}` |
| #160 | `rerating`, `fees`, `invoices/command`, `rating/inputs`, `billingruns/model`; `rate.go` split |
| #162 | payment links, promotions, affiliate attribution into `hosted/command`; `tenantevents.Relay` |
| #163 | the rest of `runs` into `billingruns/command` |
| #164 | bug: dunning defaults shown to a tenant |
| #165 | `apierr`: unexpected failures are logged and masked in one place |
| #166 | dead code, copies and magic strings |
| #168 | API completeness test, dead-letter test and tidy, storage settings, rune counts |
| #170 | bug: a re-rating credit has a currency |
| #171 | bug: a re-rating can be undone |

## billing-svc.md

### Kod tekrarı

| # | Finding | Status |
|---|---|---|
| 1 | keyset pagination, six copies | Earlier: `pkg/page` (step 2) |
| 2 | "internal error" masked 45 times, five helpers | Fixed #165 (`apierr.Internal` logs the cause) |
| 3 | UUID parse and error, many copies | Earlier: `errkey.ParseUUID`; three thin wrappers stay (`parseIDs`, `parseOptionalUUID`, `parseApp`) because their keys differ per field |
| 4 | `invalid(msg)` in three packages | Earlier (gone) |
| 5 | `brokerErr` twice in deadletters | Fixed #168 (`deadletters/model.Refusal`) |
| 6 | command envelope built three times | Fixed #149 (`workercmd.Send`) |
| 7 | `fact` / `FinalizedFact` | Fixed #149 (`writeFact`) |
| 8 | `notification` / `transferNotification` | Fixed #149 (`mailData`) |
| 9 | three sweeps with one skeleton | Fixed #149 (`sweepEach`) |
| 10 | usage stream read twice | Fixed #160 (`inputs.PeriodUsage`) |
| 11 | tenant + zone + period bounds five times | Partly #160 (`inputs.Tenant`, `EachBillableTenant`); the zone and bounds steps stay inline because the callers treat their errors differently (permanent, transient, raw) |
| 12 | snapshot built by hand twice | Fixed #160 (`inputs.SnapshotOf`); the payment-link snapshot has no customer and is another shape |
| 13 | applyCredits + CREDIT line twice | Fixed #160 (`invoicecmd.ApplyCredits`) |
| 14 | gap-free number flow twin (invoice, credit note) | Deferred: two tables and two sets of statements; only the three-step shape is common |
| 15 | `ListTenants` loop twice | Fixed #160 (`EachBillableTenant`) |
| 16 | customer lookup five times | Fixed #166 (`customerOf`) |
| 17 | best-effort void loop twice | Fixed #162 (`voidUnpaid`) |
| 18 | "everything but TAX" three times | Fixed #160 for `rating.Line` (`rating.Subtotal`); the two loops over command protos stay (another type) |
| 19 | `invoice_lines.quantity` parsed two ways | Fixed #166 (`rating.StoredUnits`) |
| 20 | reports' own period regex | Earlier (`period.Valid`) |
| 21 | `ptr`, `ptrOrNil`, `keyPtr`, `untilPtr` | Fixed #166 (`store.*`) |
| 22 | `deref` ×3, `i32` ×2 | Fixed #149, #166 (`store.Deref`, `store.I32`) |
| 23 | currency exponent fallback twice | Fixed #166 |
| 24 | pack quantity rule twice | Fixed #166 (`packQuantity`) |
| 25 | token length check twice | Fixed #166 (`validToken`) |
| 26 | payment link event data twice | Deferred: the created and the expired event carry different fields; merging changes payloads |
| 27 | hand-written comparators | Fixed #166 (`cmp.Or`, `strings.Compare`) |
| 28 | accounting `Input` built twice | Fixed #166 for the handler (`inputOf`); the insert and update parameters in `accounting/command` stay (deferred) |
| 29 | `Pack` mapped twice | Fixed #166 (`packOf`) |
| 30 | `GrantPurchase` / `grantLot` | Fixed #166 |
| 31 | base fee description twice | Fixed #160 (`baseFeeLine`) |
| 32 | credit / refund disposition twice | Fixed #155 (`model.Disposition`) |
| 33 | `on == "" \|\| "off"` twice | Fixed #166 (`einvoicemodel.Enabled`) |
| 34 | NotFound errors defined twice | Fixed #166 |
| 35 | tzdata embedded three times | Fixed with this status: only `main.go` imports `pkg/tzdata` |
| 36 | `addMonths` vs `pkg/subscription` | Deferred: the shared one is unexported and keeps the time of day, this one is day-only |
| 37 | insertion sort in `sortUsage` | Fixed #166 |
| 38 | two decimal contexts | Not real as a duplicate: `rating` rounds half to even, billing's contexts half up; merging changes results at ties. `ctx` renamed `decCtx` (#166) |
| 39 | offsets map string round trip | Fixed #160 |
| 40 | raw SQL seeds in five test files | Deferred (test code) |
| 41 | `day()` twice, `ptr` in itest, `api.New` ×10 | Fixed #155 (`e.api()`), #166 (`store.Ptr`); the two test `day()` helpers stay |

### Overengineering

| # | Finding | Status |
|---|---|---|
| 1 | thin command/query modules | Not changed: ADR 0020 gives every module `command/` and `query/`; a thin one is not a defect |
| 2 | six unused sqlc queries | Fixed #166 |
| 3 | `UnpaidInvoices` | Fixed #166 |
| 4 | `RunExists` | Fixed #160 |
| 5 | `PackByID` only in tests | Kept: the integration tests call it |
| 6 | dead `mgr == nil` branch | Fixed #162 |
| 7 | `einvoices.Handler.system` | Fixed #166 |
| 8 | `Deps.Now` never set | Fixed #166 (`time.Now()`, as it always ran) |
| 9 | `Config.Lookback` never set | Fixed #163 (a constant) |
| 10 | no-op `sort.SliceStable` | Fixed #149 |
| 11 | `saga.TenantEvent` wrapper | Fixed #149 |
| 12 | `Link{Row}`, `NoLink()` | Partly #166 (`ErrNoLink` exported); the wrapper struct stays (`Row` is read in about 30 places) |
| 13 | `PortalPage.Scope` | Fixed #166 |
| 14 | `timestampOf`, `snapshotLines` | `timestampOf` fixed #160; `snapshotLines` stays (three callers) |
| 15 | `runProto` parameter | Fixed #166 |
| 16 | `grantLot` `ref` always nil | Not real: `GrantPurchase` passes one; the two copies are now one (#166) |
| 17 | `nilIfEmptyBytes` | Fixed #166 (`requestHash`) |
| 18 | exports used only in their package | Fixed with this status (`parseSnapshot`, `voidUnpaid`, `eInvoiceParties`), #165 (`newToken`), #166 (`UnpaidInvoices` gone); `SourceManual`/`SourcePurchase` are read by tests |
| 19 | defaults in Go and in YAML | Deferred: tests build the engine and the manager without a config; dropping the fallbacks needs a test config helper |

### Spagetti kod

| # | Finding | Status |
|---|---|---|
| 1 | `rate.go` 892 lines, `Manager` god struct | Fixed #160, #162, #163 (rate, rate_usage, rate_late, rate_draft; fees, refunds, links, promotions, re-rating, e-document parties and tenant events left the manager) |
| 2 | `summary.Build` ~100 lines | Deferred |
| 3 | `CreateLink` ~97 lines | Deferred |
| 4 | `normalize` ~65 lines | Fixed #168 |
| 5 | `lateLines` nine parameters | Fixed #160 |
| 6 | `Consume` ~65 lines | Fixed #168 |
| 7 | `dlq` List / Redrive | Fixed #168 (one connection, a context, helpers, a test) |
| 8 | `Tick` runs everything | Partly #155, #160, #162: every upkeep is a named chore registered in `main.go` in the original order; one tick still runs them |
| 9 | `startLink` nesting | Deferred |
| 10 | `GetPortal` mapping | Deferred |
| 11 | domain imports `sagas/command` | Resolved by the move: it is a module, and ADR 0020 lets a module call another's exported command functions |
| 12 | `runs` returns gRPC status errors | Fixed #163 (`model.ErrBadPeriod`, `*model.PeriodOpenError`) |
| 13 | `saga/engine.go` 560 lines | Fixed #149 (nine files) |

### Clean code

| # | Finding | Status |
|---|---|---|
| 1 | reference and note errors carry `billing.payment.bad_amount` | **Fixed** (phase 49): `billing.payment.bad_reference` and `billing.payment.bad_note`, listed and translated in the six console locales; the unit test pins the keys |
| 2 | internal errors never logged | Fixed #165 |
| 3 | `notFoundOrVoided` swallows a database error | Fixed #165 |
| 4 | `Void` errors dropped | Fixed #162 (logs all but "saga busy" / "no saga yet") |
| 5 | `_ =` on errors | Fixed #160 (`FailReratingJob`), #165 (the rest) |
| 6 | error mapping differs per module | Fixed #165 (`apierr.Wrap`; e-documents and saga operator stop leaking raw errors) |
| 7 | invalid UUID answered inconsistently | Partly #165: credits answered `InvalidArgument` next to `pack_not_found`, now `NotFound`; hosted and accounting answer `NotFound` for a malformed id on purpose ("no such X") |
| 8 | `"sandbox"` literal ×12 | Earlier (`env.Sandbox`) |
| 9 | status, kind and owner strings | Fixed #166 (`invoices/model`, `payments.OwnerProvider`, `rating.Kind*`) |
| 10 | dunning defaults from built-ins | **Fixed #164 (real bug)** |
| 11 | `MaxDunning` name and comment | Fixed #166 (`DefaultDunningAttempts`) |
| 12 | credit note not found as invoice not found | Fixed #155 (`model.ErrNoteNotFound`, same key) |
| 13 | two clocks | Partly #168: credit note settlement takes the caller's clock. E-document results keep `time.Now()` (the tests' fake clock runs ahead of the wall clock and the retry schedule depends on it), and the credits package keeps it (no clock reaches its functions): deferred |
| 14 | long parameter lists, bool flags | Deferred (low) |
| 15 | messages repeat constants | Deferred (low) |
| 16 | bytes vs characters | Fixed #168 |
| 17 | magic numbers | Partly: `dlq` limits, `originBatch`, `tenantPage` named; the ones in `main.go` stay |
| 18 | comments contradicting code | Fixed #160, #166 |
| 19 | `"23505"` | Fixed #166 |
| 20 | `bad_expiry` key on payment links | Deferred: `billing.payment_link.bad_expiry` is a new key (console translations) |
| 21 | `Deps.PDFs` name and hidden coupling | Deferred |
| 22 | `LogoURL` loads a whole invoice | Deferred |
| 23 | `credits/query` imports `command.Pack` | Fixed #166 (`credits/model`) |
| 24 | decimal context named `ctx` | Fixed #166 |
| 25 | saga id in the command namespace | Comment added; changing it would change the id of every saga |
| 26 | literals in API responses | Deferred |

## cqrs-plans-billing.md (billing-svc)

| # | Finding | Status |
|---|---|---|
| 1 | `ListDeadLetters` peeks by requeueing | Documented on `deadletters/query` (#168): the broker has no side-effect-free read, so the redelivery is stated |
| 2 | `UnpaidInvoices` in command | Fixed #166 |
| 3 | `credits/query` imports `command.Pack` | Fixed #166 |
| 4 | saga imports `credits/command` | Resolved by the move (#149): a module calling another module's exported command, as the ADR allows |
| 5 | `sagas/command.Void` returns status errors | Not changed: commands return keyed errors by ADR 0020; the swallow is fixed (#162) |
| 6 | handler imports another handler's mapper | Deferred |
| 7 | `hosted.New` builds a seller closure | Fixed #162 |
| 8 | invoices written from hosted | Earlier (no such function any more) |
| 9 | one sqlc package for the service | Deferred (per-module sqlc packages) |
| 10 to 14 | UUID parsing in handlers, inconsistent | See clean code 7 |
| 15 to 24 | `runs` outside the modules | Fixed #155 to #163 (see the table at the top; `links`/`linkexpiry` to `hosted`, `einvoice` to `rating/inputs`, `tenantevents` to `tenantevents.Relay`, `providermail` to `rating/inputs`, `util` gone) |
| 25 | `saga` outside the modules | Fixed #149 |
| 26 | `replies` outside the modules | Deferred: it stays an inbound adapter; it calls the credit note, e-document and saga commands |
| 27 | `tenantevents` as a shared package | Partly #149, #162: it owns the event types, the data builders and the relay, and is the one place events are written |
| 28 | `rating` as a pure package | Accepted as a domain package |
| 29 | `dlq` adapter | Kept next to `deadletters` |
| 30 | no every-method-has-a-module test | Fixed #168 |
| 31 | `runs` returns status errors, upstream leaks | Fixed #163 |
| 32 | `err.Error()` in a client message | Earlier for runs (`errors_test.go`); fixed #165 for e-documents |
| 33 to 36 | plain `status.Error`s without a key | Partly: the 42 `Internal` masks are `apierr`; the keyed refusals were already keyed; no new keys were added (see clean code 1) |
| 37 | dunning defaults | Fixed #164 |
| 38 | defaults in Go and YAML | Deferred (overengineering 19) |
| 39 | S3 keys optional | Fixed #168 (`checkObjectStorage`) |
| 40 | `COALESCE` sentinel in `links.sql` | Deferred |
| 41 | `payments/query` passthrough | Kept until a payments list RPC exists |
| 42 | thin wrappers around `Manager` | Fixed: the rules are in the commands now |

## logic-billing-svc.md (re-verified, not redone)

| # | Finding | Status |
|---|---|---|
| 1 | dunning charges the whole invoice after a part payment | Earlier (phase 44a): the charge is the total less `invoice_payments`; test `DunningChargesWhatIsOutstandingAfterAPartPayment` |
| 2 | void refunds the whole invoice despite credit notes | Earlier (44a): `Ctx.CreditedMinor`; test `VoidRefundsWhatCreditNotesLeft` |
| 3 | credit note during a void | Earlier (44a): `create` locks the saga first and refuses `REFUND_*` / `VOIDED` |
| 4 | void with a part payment orphans the money | **Fixed** (decision: a credit balance, not a refusal; migration `00055`, `credit_balances.source_invoice_id`; test `VoidingAPartPaidInvoiceCreditsWhatWasReceived`). A voided invoice that was paid in full by hand still asks its provider for a refund: open |
| 5 | `ErrNoRefund` only without a saga | **Fixed** for a refund credit note: an invoice with money recorded by hand (`invoice_payments`) has no card to refund and the note is refused with `billing.credit_note.nothing_to_refund`, before it has a number; test `RefundCreditNoteNeedsACardPayment` |
| 6 | credits applied to a voided invoice not reopened | Earlier (44a): `reopenCredits`; test `VoidingAnInvoiceReopensTheCreditsItConsumed` |
| 7 | re-rating credit without a currency | **Fixed #170** (code, migration `00047`, test) |
| 8 | plan change "unused time" is a CREDIT line | Earlier (44a): a DISCOUNT line; test `PlanChangeTaxIsOnWhatIsLeftAfterTheUnusedTime` |
| 9 | CORRECTION invoices carry no tax | **Fixed**: a correction is taxed at the customer's rate like the invoice it corrects (a TAX line), and a re-rating credit includes the tax on it; none for a provider-owned subscription; tests `RerateCorrectionsCarryTheTax`, `RerateCreditsGiveTheTaxBack` |
| 10 | re-rating ignores trial, advance fee, provider fee | Earlier (44a): `feeWaived`; test `RerateKeepsTheWaivedFixedFeesWaived` |
| 11 | a job skips subscriptions already on its version | **Fixed #171** (code, test) |
| 12 | comment says usage is never re-counted | Fixed #160 (the comment; reading the active generation is deliberate) |
| 13 | a FAILED job cannot be restarted | **Open** (low) |
| 14 | fee segments priced in each member's currency | Not verified (the report says "muhtemel"; a chain in several currencies is skipped by re-rating) |
| 15 | late usage overwritten instead of summed | Not real: the aggregator keeps one row per tenant, billed period, customer, meter, origin and generation |
| 16 | a negative total clamped to zero | **Open** (low; the stale comment is fixed) |
| 17 | trial ending inside the period charges the whole fee | **Open** (low) |
| 18 | `PriorLateUsage` sums quantities of different scales | **Open** (low) |
| 19 | numbers use the UTC year | **Open** (low; changes the numbering) |

## logic-plans-svc.md, the coupon on a provider migration

| Finding | Status |
|---|---|
| a provider migration drops the coupon (`ErrCouponOnCredit` swallowed, no `MoveSubscriptionCoupon`) | **Fixed**. The move to a Payrudder-run, in-advance successor already carried the coupon (`SwitchTo`, since phase 44a). A successor that cannot hold one (run by a provider, or billed in arrears) is now refused with `plans.migration.coupon_blocks_move` until the coupon is removed: at `StartProviderMigration` for a provider-run target, where waiting for the customer to subscribe would be too late, and in the cutover itself. Test `ProviderMigrationDoesNotDropTheCoupon` |
