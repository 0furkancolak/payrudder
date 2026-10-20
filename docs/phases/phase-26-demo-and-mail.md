# Phase 26 — Demo mode and mail by provider

## Scope

- `QUOTIFY_DEMO=true`: seeds a demo app (customers, plans, a few months of usage and invoices), allows only the mock provider, sends mail to the local catcher, shows a banner in the console, and `quotifyctl demo reset` returns to the seed. Refuses to start next to a live provider account.
- Provider capability `SendsOwnReceipts` (Paddle: true, iyzico: false, mock: false). The invoice saga's notify step is skipped, with a log line, for a payment taken by a provider that sends its own; the payment-failed mail follows the same rule. A tenant can switch Quotify's mail off entirely.

## Exit criteria

Demo start-up to a populated console in one command; an iyzico-run payment sends mail, a Paddle-run one does not, both tested.

## As built

- **Mail by provider:** `Capabilities.SendsOwnReceipts` (Paddle true; iyzico and the mock false). The flag is read when an invoice draft is made and stored in its snapshot (`provider_mails`), so a running saga never changes its mind; the saga table has a row `PAID → COMPLETED [the provider mails the receipt]` ahead of `PAID → NOTIFY_REQUESTED`. A give-up notice (`uncollectible`) is still Quotify's: the provider's receipts are for payments. Invoices made before the change have no flag and keep sending.
- **Demo mode:** `QUOTIFY_DEMO=true` (YAML default false). payments-worker answers every command, live tenants included, with the sandbox provider (mock-psp), so a demo stack cannot reach a real provider; the console shows a banner (`/env.js`); `make demo-mode` starts the stack that way, plays the README story (sample customers, usage, a period close, a price change, a re-rating) and prints where the console and the mail catcher are.
- **Not built:** a per-tenant switch for Quotify's mail, and refusing to start next to live provider accounts (the demo flag makes them unreachable instead, which is the property that matters).
