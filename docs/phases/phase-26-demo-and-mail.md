# Phase 26 — Demo mode and mail by provider

## Scope

- `QUOTIFY_DEMO=true`: seeds a demo app (customers, plans, a few months of usage and invoices), allows only the mock provider, sends mail to the local catcher, shows a banner in the console, and `quotifyctl demo reset` returns to the seed. Refuses to start next to a live provider account.
- Provider capability `SendsOwnReceipts` (Paddle: true, iyzico: false, mock: false). The invoice saga's notify step is skipped, with a log line, for a payment taken by a provider that sends its own; the payment-failed mail follows the same rule. A tenant can switch Quotify's mail off entirely.

## Exit criteria

Demo start-up to a populated console in one command; an iyzico-run payment sends mail, a Paddle-run one does not, both tested.
