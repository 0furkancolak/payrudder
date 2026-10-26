# Phase 52 — Onboarding, product tour, free credits and trials

Delete this file in the closing PR.

## Scope

1. **Onboarding for a new sign-up** (after e-mail verification, first visit to `/app`): a short wizard that says why Payrudder exists (meter usage, price it, bill it, get paid) and does the first setup in the same flow: organisation name/currency/time zone → first app → first meter (template choices: API calls, GB storage, seats…) → first package with a price → send a test event in sandbox → done screen with next steps. Resumable (progress stored server side or per user), skippable, steps tick off a persistent "Getting started" checklist on the app overview.
2. **Tour of the home page** on first arrival (spotlight steps over the real UI: app switcher, sidebar groups, sandbox switch, user menu), and a **tour icon in every page header** that starts that page's tour (steps per page, in six languages, keyboard accessible, skippable, remembered as seen per user and page). Build one small tour engine (`shared/ui/tour`) with step definitions next to each page.
3. **Free credits and trials for the tenant's customers** (the console has none today): grant free credits to a customer (reason, amount, optional expiry) from the customer sheet and credit pages; **trial** subscriptions (days, no card) selectable when creating a subscription and as a default on a package; trial end shown and reminded; a "free tier" package option. Reuse the existing credit grant API and subscription trial support; add backend only where missing, with tests.
