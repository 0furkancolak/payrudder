# Phase 54 — Customer sheet, hosted pages, PDF, accounting

Reference images 7–10. Delete this file in the closing PR.

## Scope

1. **Customer sheet**: the payment page (customer portal) must not open inside the sheet as an embedded card; offer links (copy, open in a new tab, regenerate/revoke) in a small popover or a section, never a big in-place panel.
2. **Hosted customer portal / payment page** (`/c/<token>`, image 10): redesign to fit the app (same tokens, typography, Stripe-like layout, light and dark, the tenant's brand name, responsive); promo code field usable; no browser-autofill clash.
3. **Invoice PDF**: the SANDBOX watermark is clipped; centre it, scale to the page, repeat if needed; check A4 and Letter.
4. **Accounting**: "Add expense" is not at the bottom — put it in the page header and as a sheet; **automatic invoice scanning**: upload a receipt/invoice (PDF or image) and prefill vendor, date, amount, currency, tax (text PDFs parsed server side; images through a pluggable extractor interface with a documented default and a mock), user confirms before saving; kept as an attachment.
