-- The newest saved-payment-method notification applied to a customer (docs/plans/22 payments review, item (f)). Every statement names the tenant.

-- name: GetPaymentMethodApplied :one
SELECT occurred_at FROM payment_method_applied WHERE tenant_id = @tenant_id AND provider = @provider AND environment = @environment AND customer_ref = @customer_ref;

-- name: PutPaymentMethodApplied :exec
-- Only forwards: the stored time never goes back, whatever order two workers finish in.
INSERT INTO payment_method_applied (tenant_id, provider, environment, customer_ref, occurred_at)
VALUES (@tenant_id, @provider, @environment, @customer_ref, @occurred_at)
ON CONFLICT (tenant_id, provider, environment, customer_ref) DO UPDATE SET
  occurred_at = GREATEST(payment_method_applied.occurred_at, EXCLUDED.occurred_at), updated_at = now();
