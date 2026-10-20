# Error keys

Generated from the code by `go test ./pkg/errkey` (see ADR 0020). The key is in `error.key` of every API error; translate keys, not messages.

- `billing.credit_note.bad_amount`
- `billing.credit_note.fully_credited`
- `billing.credit_note.not_paid`
- `billing.credit_note.nothing_to_refund`
- `billing.credit_note.refund_not_failed`
- `billing.dunning.bad_delay`
- `billing.dunning.too_many_retries`
- `billing.invoice.not_found`
- `billing.invoice.pdf_not_ready`
- `billing.invoice_link.bad_expiry`
- `billing.invoice_link.draft_invoice`
- `billing.invoice_link.not_found`
- `einvoice.account.invalid`
- `einvoice.account.missing`
- `einvoice.account.sandbox_mock_only`
- `einvoice.buyer.incomplete`
- `einvoice.cancel.unsupported`
- `einvoice.document.not_failed`
- `einvoice.document.not_found`
- `einvoice.document.not_issued`
- `einvoice.file.not_ready`
- `einvoice.provider.rejected`
- `einvoice.provider.unavailable`
- `einvoice.seller.incomplete`
- `einvoice.setting.invalid`
- `plans.app.bad_slug`
- `plans.app.slug_taken`
- `plans.customer.tax_invalid`
- `plans.subscription.coupon_blocks_credit`
- `plans.webhook.not_configured`
- `plans.webhook.too_many_endpoints`
