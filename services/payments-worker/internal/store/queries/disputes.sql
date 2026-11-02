-- Disputes: a customer questioned a payment with their bank (migration 00011).

-- name: UpsertDispute :exec
-- The case as the latest notification says it. A case that ended is not reopened by a late notification about an earlier step (a "created" that arrives after "won"):
-- the outcome only moves from open to won or lost, and from lost to won (Stripe's late win).
INSERT INTO payment_disputes AS d (tenant_id, environment, provider, dispute_ref, payment_ref, invoice_id, amount_minor, currency, outcome, formal, provider_status, reason, evidence_due_at, opened_at, closed_at, fee_minor, fee_currency)
VALUES (@tenant_id, @environment, @provider, @dispute_ref, @payment_ref, sqlc.narg('invoice_id'), @amount_minor, @currency, @outcome, @formal, @provider_status, @reason, sqlc.narg('evidence_due_at'), @opened_at, sqlc.narg('closed_at'), sqlc.narg('fee_minor'), sqlc.narg('fee_currency'))
ON CONFLICT (tenant_id, provider, dispute_ref) DO UPDATE SET
  invoice_id = COALESCE(d.invoice_id, EXCLUDED.invoice_id),
  amount_minor = EXCLUDED.amount_minor, formal = d.formal OR EXCLUDED.formal,
  outcome = CASE WHEN d.outcome = 'open' OR (d.outcome = 'lost' AND EXCLUDED.outcome = 'won') THEN EXCLUDED.outcome ELSE d.outcome END,
  provider_status = CASE WHEN d.outcome = 'open' OR (d.outcome = 'lost' AND EXCLUDED.outcome = 'won') THEN EXCLUDED.provider_status ELSE d.provider_status END,
  reason = CASE WHEN EXCLUDED.reason <> '' THEN EXCLUDED.reason ELSE d.reason END,
  evidence_due_at = COALESCE(EXCLUDED.evidence_due_at, d.evidence_due_at),
  fee_minor = COALESCE(EXCLUDED.fee_minor, d.fee_minor), fee_currency = COALESCE(EXCLUDED.fee_currency, d.fee_currency),
  closed_at = CASE WHEN d.outcome = 'open' OR (d.outcome = 'lost' AND EXCLUDED.outcome = 'won') THEN EXCLUDED.closed_at ELSE d.closed_at END,
  updated_at = now();

-- name: ListDisputes :many
SELECT d.id, d.environment, d.provider, d.dispute_ref, d.payment_ref, d.invoice_id, d.amount_minor, d.currency, d.outcome, d.formal, d.provider_status, d.reason, d.evidence_due_at,
  d.opened_at, d.closed_at, d.fee_minor, d.fee_currency, d.handled_at, d.handled_by,
  (SELECT count(*) FROM payment_dispute_events e WHERE e.tenant_id = d.tenant_id AND e.dispute_id = d.id AND e.action = 'note')::bigint AS note_count
FROM payment_disputes d
WHERE d.tenant_id = @tenant_id AND (sqlc.narg('invoice_id')::uuid IS NULL OR d.invoice_id = sqlc.narg('invoice_id')::uuid) AND (@outcome::text = '' OR d.outcome = @outcome::text)
  AND (sqlc.narg('id')::bigint IS NULL OR d.id = sqlc.narg('id')::bigint)
ORDER BY d.opened_at DESC, d.id DESC
LIMIT @row_limit;

-- name: GetDisputeForUpdate :one
SELECT id, environment, provider, dispute_ref, invoice_id, handled_at FROM payment_disputes WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: SetDisputeHandled :exec
UPDATE payment_disputes SET handled_at = sqlc.narg('handled_at'), handled_by = @handled_by, updated_at = now() WHERE tenant_id = @tenant_id AND id = @id;

-- name: InsertDisputeEvent :one
INSERT INTO payment_dispute_events (tenant_id, environment, dispute_id, action, note, actor)
VALUES (@tenant_id, @environment, @dispute_id, @action, @note, @actor)
RETURNING id, created_at;

-- name: ListDisputeEvents :many
SELECT id, action, note, actor, created_at FROM payment_dispute_events WHERE tenant_id = @tenant_id AND dispute_id = @dispute_id ORDER BY id DESC LIMIT @row_limit;

-- name: ListOpenDisputes :many
-- What the reconciliation shows: the cases still undecided, with an invoice to point at.
SELECT dispute_ref, payment_ref, provider, invoice_id, amount_minor, currency, formal, provider_status, reason, opened_at
FROM payment_disputes WHERE tenant_id = @tenant_id AND outcome = 'open' AND invoice_id IS NOT NULL ORDER BY opened_at, id;

-- name: GetChargeByPaymentRef :one
-- The successful charge the ledger holds for a provider's payment reference, whichever invoice: the invoice a dispute is about.
SELECT invoice_id, amount_minor, currency FROM payment_ledger
WHERE tenant_id = @tenant_id AND provider = @provider AND kind = 'charge' AND status = 'succeeded' AND provider_ref = @provider_ref AND invoice_id IS NOT NULL
ORDER BY id LIMIT 1;

-- name: DisputeFactExists :one
-- Whether this step of a case (its provider reference and the status the ledger writes for the outcome) is in the ledger already: it was announced then.
SELECT EXISTS (SELECT 1 FROM payment_ledger WHERE tenant_id = @tenant_id AND provider = @provider AND kind = 'dispute' AND provider_ref = @provider_ref AND status = @status::text);
