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
- `billing.payment.amount_mismatch`
- `billing.payment.invoice_not_payable`
- `billing.payment.method_unavailable`
- `billing.payment_link.bad_amount`
- `billing.payment_link.bad_currency`
- `billing.payment_link.bad_description`
- `billing.payment_link.bad_key`
- `billing.payment_link.bad_kind`
- `billing.payment_link.bad_page`
- `billing.payment_link.bad_status`
- `billing.payment_link.consumed`
- `billing.payment_link.customer_not_found`
- `billing.payment_link.idempotency_conflict`
- `billing.payment_link.invoice_not_payable`
- `billing.payment_link.invoice_required`
- `billing.payment_link.not_found`
- `billing.portal_link.customer_not_found`
- `billing.portal_link.customer_required`
- `plans.app.bad_slug`
- `plans.app.slug_taken`
- `plans.subscription.coupon_blocks_credit`
- `plans.webhook.not_configured`
- `plans.webhook.too_many_endpoints`
