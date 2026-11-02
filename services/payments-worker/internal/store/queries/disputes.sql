-- Disputes: a customer questioned a payment with their bank (migration 00011).

-- name: UpsertDispute :exec
-- The case as the latest notification says it. A case that ended is not reopened by a late notification about an earlier step (a "created" that arrives after "won"):
-- the outcome only moves from open to won or lost, and from lost to won (Stripe's late win).
INSERT INTO payment_disputes AS d (tenant_id, environment, provider, dispute_ref, payment_ref, invoice_id, amount_minor, currency, outcome, formal, provider_status, reason, evidence_due_at, opened_at, closed_at)
VALUES (@tenant_id, @environment, @provider, @dispute_ref, @payment_ref, sqlc.narg('invoice_id'), @amount_minor, @currency, @outcome, @formal, @provider_status, @reason, sqlc.narg('evidence_due_at'), @opened_at, sqlc.narg('closed_at'))
ON CONFLICT (tenant_id, provider, dispute_ref) DO UPDATE SET
  invoice_id = COALESCE(d.invoice_id, EXCLUDED.invoice_id),
  amount_minor = EXCLUDED.amount_minor, formal = d.formal OR EXCLUDED.formal,
  outcome = CASE WHEN d.outcome = 'open' OR (d.outcome = 'lost' AND EXCLUDED.outcome = 'won') THEN EXCLUDED.outcome ELSE d.outcome END,
  provider_status = CASE WHEN d.outcome = 'open' OR (d.outcome = 'lost' AND EXCLUDED.outcome = 'won') THEN EXCLUDED.provider_status ELSE d.provider_status END,
  reason = CASE WHEN EXCLUDED.reason <> '' THEN EXCLUDED.reason ELSE d.reason END,
  evidence_due_at = COALESCE(EXCLUDED.evidence_due_at, d.evidence_due_at),
  closed_at = CASE WHEN d.outcome = 'open' OR (d.outcome = 'lost' AND EXCLUDED.outcome = 'won') THEN EXCLUDED.closed_at ELSE d.closed_at END,
  updated_at = now();

-- name: ListDisputes :many
SELECT id, environment, provider, dispute_ref, payment_ref, invoice_id, amount_minor, currency, outcome, formal, provider_status, reason, evidence_due_at, opened_at, closed_at
FROM payment_disputes
WHERE tenant_id = @tenant_id AND (sqlc.narg('invoice_id')::uuid IS NULL OR invoice_id = sqlc.narg('invoice_id')::uuid) AND (@outcome::text = '' OR outcome = @outcome::text)
ORDER BY opened_at DESC, id DESC
LIMIT @row_limit;

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
