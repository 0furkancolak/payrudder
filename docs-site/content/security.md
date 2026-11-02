# Security

What protects an account, and what you do to keep it safe: API keys, two-factor sign-in, the audit log and webhook signatures. Each section says how it works, in the terms of the repository's documents and code, and what is not covered.

**This page makes no claim about audits, certifications, penetration tests or compliance.** None is recorded in the repository, so none is described. It also says nothing about the infrastructure of a particular deployment: where it runs, how it is backed up and who can reach its servers are the deployment's own answers.

## API keys

- A key is `qk_test_…` (sandbox) or `qk_live_…` (live) and acts on that environment only. It is shown **once**, when it is made; Payrudder keeps only a hash, so a lost key is replaced, not recovered.
- Give each service the **narrowest role** that works: `ingest_only` for the code that reports usage, `read_only` for a dashboard, `custom` for exactly the permissions chosen. A request outside the role is `403`. The roles are in [Authentication](authentication.html).
- **Rotate** with an overlap (the old key gets an end time) and **revoke** at once when a key may have leaked or someone who knew it leaves. Both are recorded in the audit log.
- Keep keys on your servers, in an environment variable or a secret store. Never in a web page, a mobile app or a repository.
- The keys of a payment provider that you connect to Payrudder are stored **sealed** with a key the deployment holds, are shown once and are never returned by any call.

## Two-factor sign-in

People who sign in to the console can turn on two-factor sign-in with an authenticator app (TOTP, RFC 6238: six digits, thirty-second steps) and get ten single-use recovery codes, shown once.

- It applies to **every** way of signing in (password, a mailed code, Google): the first step ends in a challenge, not a session, until the code is right.
- A code works once. Wrong codes count against the same lock as a wrong password: five in a row lock sign-in for fifteen minutes, and the limit per address applies as well. The answer is the same whether the code was wrong, the challenge was spent or the account was locked.
- An **owner of an account can require it** for all the account's owners. While it is required, an owner who has not set it up can do nothing but set it up.
- **Not built:** passkeys (WebAuthn), remembering a device, a mail when two-factor sign-in changes, and a self-service way back after losing both the device and the recovery codes: today support removes the factor after checking who is asking by other means, and that step is not itself recorded.

## The audit log

Who did what and when, for the actions that change money or access: keys created, rotated and revoked; members invited, changed and removed; plan versions published; webhook endpoints created, changed and deleted and their secrets rotated; customers blocked and unblocked; credit notes and refunds; payment provider and e-invoice accounts connected, replaced and removed; the tenant's status and settings; two-factor changes.

- An entry is written **in the same transaction as the change**: if it cannot be written the change does not happen.
- The log is **append-only in the database**: nobody, the table's owner included, can update or delete an entry.
- An entry names the actor (a person, an API key, the operator or the system), the target, a one-line summary with no secret in it, the address the request came from and the request id.
- Read it with `GET /v1/audit-events` (and `/v1/audit-events/export` as CSV) with a key that has `audit:read`, which `admin` keys have and `read_only` keys do not; or in the console.
- **Not recorded yet:** price changes, coupons and tax rates, dunning and mail settings, offline payments and bank-transfer receipts, sign-ins and failed attempts, password changes. The log is also not a substitute for your own logs of what your systems did.

## Webhook signatures

Every delivery carries `Payrudder-Signature: t=<unix time>,v1=<hex>`, where the hex is HMAC-SHA256 of `<t>.<raw body>` under the endpoint's secret. Verify it on the **raw** bytes, compare in constant time and reject a time more than five minutes from your clock; the SDKs do all three ([Webhooks](webhooks.html)).

- The signing secret (`whsec_…`) is shown once. Rotating it keeps the old secret signing for 24 hours (two `v1` values), so you can switch without losing a delivery.
- An endpoint must be `https://`, without credentials in the address, and must resolve to a public address. The address is checked when the connection is opened, after name resolution, redirects are not followed and no proxy is used, so an endpoint cannot be pointed at a machine inside a network.
- Deliveries are at least once and not in order. Use the event `id` to ignore a repeat.

## Pages your customers open

An invoice link is addressed by an unguessable token (256 random bits; only a hash is kept), shown once when the link is made. The invoice page is not indexed or cached and loads nothing from another site. Payment links and the customer's own page (read-only, one customer's invoices and payments) also work by a token that is returned once. A link works until it expires (an ad hoc payment link lasts 30 days, an invoice link 90, at most 365) or is revoked.

## What is left to you

Protect the people who can sign in (two-factor, and remove members who leave), keep keys and signing secrets out of code, verify every webhook, watch the audit log for keys and members you do not recognise, and use a sandbox key wherever a test needs one.
