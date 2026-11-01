# Provider account health, the circuit breaker, and failover that cannot charge twice

Plan 22 G8. Built 2026-11-02 on `feat/provider-health-breaker` (on top of G7, [`reconciliation.md`](reconciliation.md)). Tested against a fault-injecting fake provider and a Postgres in Docker; **nothing ran against a real provider**.

## The rule

> A charge may be tried at a second account **only when the first provider definitely did not process it, only for a charge that is new, and then as a new attempt under a new key.** **A timeout never fails over.**

Why: a timeout means the provider may have charged and be slow to say. A second charge at another provider would charge the customer twice. What follows a timeout is what always did: the reply says the outcome is unknown, the saga asks the first provider by the key (`PAYMENT_UNKNOWN`, then the grace period and key horizon of plan 22 G2), and nothing is sent again until that answer is in. Failover does not touch that path (`TestATimeoutNeverFailsOver`: after a timeout, and after a second delivery, the second account is neither charged nor asked).

### What counts as "definitely did not process" (`payments.NotProcessed`)

- the connection to the provider **never opened**: a dial or DNS failure (the request was never sent). All four adapters (Stripe, Paddle, iyzico, mock-psp) turn a timeout into `ErrTimeout` before anything else, and any other transport error into `*TransientError` carrying the Go error, so the dial case is recognised from it without touching an adapter (`stripe/outage_test.go`, `failover_test.go`). A **connect timeout** is `ErrTimeout`: not proof;
- an adapter or provider that says "not processed" itself returns a `*payments.NotProcessedError`. **No adapter does today**: neither compatibility matrix documents an HTTP signal that a 5xx was returned before processing (**UNVERIFIED**), so none was invented;
- an **open circuit** (below): the provider was not called at all.

What does **not** count: a timeout, a 5xx (a gateway can answer 502 after the provider behind it charged), a 429, a reset in the middle of a call, a decline (the provider answered).

### Only a charge that is new

"New" is the first delivery of the command: `Attempts.Start` says the row did not exist. A delivery that is not the first might have been sent to the first account, so it is **never** failed over, even on a definite failure (`TestFailoverIsOnlyForANewChargeAndOnlyWhenConfigured`). It is retried, and the usual ask-first logic applies when the first account answers.

### A new key, the same on every delivery

The second attempt's key is `payments.FailoverKey(key, account)` = `key + "~fo~" + <12 hex of sha256(account)>`: new (the first provider may know the original key), and a function of the two, so a command delivered again goes to the *same* second attempt and an idempotent provider returns its first answer instead of charging again. The second attempt has its own `payment_attempts` row (a command id derived from the command's, written *before* the second provider is called), so what happened is on record.

A command delivered again after it failed over **resumes at the second account** (`FailoverAttempt` finds the row by the key's prefix) and never goes back to the first, which may be up again by now: otherwise the first delivery's charge at the second account plus a charge at the first would be a double charge (`TestADefiniteFailureFailsOverUnderANewKey`, second delivery). A status query for the original key asks the account that took the charge, under the key it was made with.

The charge is recorded under the **account key** of the account that took it (`stripe~eu`), in `payment_attempts.provider` and the ledger, so that a refund and a status query find the same account. **This changes how a labelled account's charge was recorded**: it used `prov.Name()`, the base provider, so a refund of a charge made at `stripe~eu` went to the primary `stripe` account (plan 10 lists this as untested). A provider without a label records what it always did (its key is its name).

### Where it is configured

`PUT /admin/v1/tenants/{id}/provider-accounts/{provider}/failover?environment=` `{"failover_to": "stripe~eu"}` (and `DELETE`): one account, same environment, **one hop** (the target's own entry is not followed, so no cycles), both must exist, no bank transfer (confirmed by hand). **No failover is configured by default (default, not agreed).** At the moment of the failure the target must also build, pass the environment guard, charge in the currency, and not be a manual collector; otherwise the failure is returned as it was.

## The circuit breaker

One per account (tenant, account key, environment), in the worker's memory (`payments.Breaker`):

| State | Meaning |
|---|---|
| `closed` | charges go to the provider |
| `open` | `PAYMENTS_BREAKER_FAILURES` (**5**) outages in a row: new charges fail fast, **without a call**, for `PAYMENTS_BREAKER_OPEN_FOR` (**30 s**) |
| `half_open` | the cool-down passed: **one** charge at a time is let through as a probe; a good one closes the circuit, a failed one opens it for another cool-down |

All four numbers are **defaults, not agreed**. An *outage* is a timeout, an unreachable provider or an HTTP 5xx / 429 (`payments.IsOutage`). A decline, a refusal of the request and a pending payment are the provider working: they reset the count and close the circuit. Only new charges are guarded: status queries and refunds are not (they are how an unknown outcome is resolved), and they are paced by the saga's backoff.

A charge that finds the circuit open is **not sent**: `Allow` returns a `*TransientError` wrapping `payments.ErrCircuitOpen` with `RetryAfter` set to what is left of the cool-down. The handler returns it, so the command goes through the delay queues like any transient failure (no reply, no ledger row), and the metric `provider_breaker_rejections` counts it. If a failover is set and this is the first delivery, the charge goes to the other account instead: the first was not called, so the rule holds. State is kept for people to read (`provider_account_health.breaker`, `consecutive_failures`, `opened_at`) and a **restart begins closed** (replicas do not share breakers). *Shortcut:* with several replicas each learns of an outage by itself, and the health monitor (below) helps all of them.

## Health

`health.Monitor` (`PAYMENTS_HEALTH_INTERVAL`, **5 min, default, not agreed**; 0 turns it off) checks every account of every tenant with the adapter's own `payments.HealthChecker` (one authenticated, read-only request that moves no money; limit `PAYMENTS_HEALTH_TIMEOUT`, 10 s) and keeps the result in `provider_account_health` (migration `00004`, tenant twin `tenant/00005`, keyed by the account key like `provider_accounts`):

| `status` | Meaning |
|---|---|
| `healthy` | the provider answered with the credentials |
| `down` | unreachable (`last_error` `unreachable`), or the circuit is open (`circuit_open`) |
| `bad_credentials` | the provider **answered and refused**: it is up, so the breaker counts it as reachable, but charges will be refused; `last_error` is a class, never the provider's message |
| `unknown` | never checked, or the adapter has no call (a bank transfer: `no_remote_check`), or no adapter can be built from what is stored (`unusable`) |

The outcome is also fed to the account's breaker: a provider that came back closes the circuit before a charge has to probe it, and one that is down opens it without a charge having to fail first. `GET /admin/v1/tenants/{id}/account-health` lists every account with its last check, breaker and failover target; the integrations page does not show it yet (plan 10).

Metrics (payments-worker): `provider_health_checks{provider,result}`, `provider_breaker_transitions{provider,to}`, `provider_breaker_rejections{provider}`. **No alert uses them yet** (promtool was not run; a rule such as "a live account `down` for 10 minutes" belongs with plan 22 G6).

## Not done, not verified

- **Not verified against any real provider**: that a real provider's dial failure looks like it does against `127.0.0.1:1`; the checks of Stripe (`GET /v1/balance`), iyzico (BIN check) and Paddle (see their matrices) as a health signal under load; what a provider returns for a repeated key with different parameters after a failover. Every number is a default, not agreed.
- A 5xx is never "definite" until an adapter has a documented signal for it; so for an HTTP outage the only defence is the breaker and the saga's own retry (a failover happens only when the connection did not open, the circuit is open, or a provider says so).
- Routing preview: `previewRouting` (plans-svc) does not mention health or failover; `billing-svc` and `plans-svc` were not changed. The routing rule still names one account; the failover is the worker's.
- No console screen (API only); `account_id` (plan 10) does not exist, rows are keyed by the account key.
- Breaker state is per process; the health monitor runs in every replica (a check each, every interval).
- The failover of a subscription that the provider itself renews (Paddle) is out of the question and not attempted: only Payrudder-driven charges go through the handler.
