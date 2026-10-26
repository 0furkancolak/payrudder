# Phase 53 — Integrations page and API keys

Delete this file in the closing PR.

## Scope

1. **Integrations** replaces "Payment providers": one page with groups **Payments** (iyzico, Paddle, bank transfer, mock/sandbox), **E-invoice** (the integrators) and **E-mail** (Payrudder's built-in mail, plus the tenant's own SMTP/provider if it exists in the code). Each card has the provider's **real logo** (SVG, licensed for nominative use, stored under `web/admin/public/brand/providers/`, light and dark variants) and its status, connect/configure sheet, test. Old URLs redirect. Docs and nav updated.
2. **API keys**: a key has a **role** (e.g. read-only, ingest-only, billing, admin) and selectable **permissions/scopes** enforced by the gateway (tests: a key without a scope gets 403 with an error key); the **sandbox and live keys are created in separate places** (sandbox keys under the sandbox environment view, live keys only in live and behind the go-live gate), with the environment shown on the key. Migration, proto, gateway enforcement, console UI, six languages.
