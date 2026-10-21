# Error keys

Generated from the code by `go test ./pkg/errkey` (see ADR 0020). The key is in `error.key` of every API error; translate keys, not messages.

- `billing.credit_note.bad_amount`
- `billing.credit_note.fully_credited`
- `billing.credit_note.not_paid`
- `billing.credit_note.nothing_to_refund`
- `billing.credit_note.refund_not_failed`
- `billing.credits.bad_amount`
- `billing.credits.bad_customer`
- `billing.credits.bad_expiry`
- `billing.credits.bad_key`
- `billing.credits.bad_pack`
- `billing.credits.bad_page_token`
- `billing.credits.bad_reason`
- `billing.credits.bad_source`
- `billing.credits.insufficient`
- `billing.credits.key_required`
- `billing.credits.key_reused`
- `billing.credits.pack_not_found`
- `billing.credits.unknown_currency`
- `billing.dunning.bad_delay`
- `billing.dunning.too_many_retries`
- `billing.invoice.not_found`
- `billing.invoice.pdf_not_ready`
- `billing.invoice_link.bad_expiry`
- `billing.invoice_link.draft_invoice`
- `billing.invoice_link.not_found`
- `plans.app.bad_slug`
- `plans.app.slug_taken`
- `plans.mail.unknown_category`
- `plans.subscription.coupon_blocks_credit`
- `plans.webhook.not_configured`
- `plans.webhook.too_many_endpoints`
