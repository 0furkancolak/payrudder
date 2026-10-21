-- name: GetAttemptReply :one
SELECT reply FROM payment_attempts WHERE command_id = @command_id AND status = 'DONE';

-- name: StoreAttemptReply :exec
UPDATE payment_attempts SET reply = @reply, status = 'DONE', finished_at = now() WHERE command_id = @command_id;

-- name: StartAttempt :execrows
INSERT INTO payment_attempts (command_id, tenant_id, invoice_id, kind, environment, provider, idempotency_key, request, status)
VALUES (@command_id, @tenant_id, @invoice_id, @kind, @environment, @provider, @idempotency_key, @request, 'STARTED')
ON CONFLICT (command_id) DO NOTHING;

-- name: GetChargeAttemptByKey :one
SELECT tenant_id, invoice_id, environment, provider,
  COALESCE((request->>'amount_minor')::bigint, (request->>'amountMinor')::bigint, 0)::bigint AS amount_minor,
  COALESCE(request->>'currency', '')::text AS currency
FROM payment_attempts WHERE kind = 'charge' AND idempotency_key = @idempotency_key ORDER BY created_at DESC LIMIT 1;

-- name: GetChargeProvider :one
SELECT provider FROM payment_attempts WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND kind = 'charge' ORDER BY created_at DESC LIMIT 1;

-- name: CountLinkAttempts :one
SELECT count(*) FROM payment_attempts WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id AND kind = 'charge' AND idempotency_key LIKE 'link:%';
