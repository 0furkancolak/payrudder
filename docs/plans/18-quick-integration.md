# 18 — Quick integration for apps

## The request
List 2 (3): a **fast integration structure for applications**.

## Where we are
#255: a "Connect your app" page per tenant (developers section, with a tour): code to paste in curl, TypeScript, Python, Go and PHP built from the tenant's own meter and a customer id (escaped; only the shape of a key), a webhook verification snippet per language, the API address typed once and kept in the browser, and a checklist (meter, customer, first usage event counted in the last week, polled every 5 s). Unit tests of the snippet generator (incl. injection cases) and mock e2e.

## Gaps
- **The snippets were never executed.** They follow the SDK READMEs; no CI job compiles/runs them against a running stack, so they can rot.
- The SDKs are **pre-release and unpublished** (install lines point at a checkout), so "quick" is not quick: no `npm install @payrudder/sdk`.
- The page cannot create the key (keys are shown once on another page), cannot create the first meter/customer for the person, and the API address is typed by hand (the console doesn't know the public address).
- No framework quick-starts (Next.js, Express, FastAPI, Laravel, Rails…), no usage-event best practices (batching, retries, idempotency keys, clock skew), no test-mode event simulator, no webhook **local tunnel** helper or "send a test webhook" button in the checklist.
- No "integration health" over time (event rate, rejected events with error codes) beyond the first event.
- Onboarding (03) and this page overlap; the relationship is not designed.

## Research first
1. Watch a developer integrate (the owner or a colleague) from sign-up to first event on the real stack and note every stumble.
2. List what Stripe/Paddle quick-starts and "test events" tools provide; choose the first set.

## Decisions needed
Publishing the SDKs (package names, registry accounts) — currently "no releases until complete"; whether to ship framework templates.

## Work
1. CI job that extracts each snippet and runs it against the compose stack (TypeScript, Python, Go, PHP, curl) so they are always correct.
2. Key creation inline (shown once, copied, never stored by the page) and a one-click "create sample meter and customer" in sandbox.
3. "Send a test event" and "send a test webhook" buttons; rejected-event viewer with error codes and fixes.
4. Framework templates (two to start) in a `examples/` directory with their own CI.
5. Publish SDKs when the owner lifts the no-release rule; then real install lines.

## Done means
A new developer reaches a counted event and a verified webhook in under ten minutes on a clean stack, measured with a person, with the snippet CI green.

## Not verifiable here
The ten-minute claim without a person.

## Risks
Snippets drift from the SDKs: the CI job is the guard.
