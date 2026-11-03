-- The appeals of blocked customers (plan 13): tenant-scoped, RLS.

-- name: InsertCustomerBlockAppeal :one
INSERT INTO customer_block_appeals (tenant_id, id, customer_id, status, statement)
VALUES (app_tenant_id(), @id, @customer_id, 'open', @statement) RETURNING *;

-- name: GetCustomerBlockAppealForUpdate :one
SELECT * FROM customer_block_appeals WHERE tenant_id = app_tenant_id() AND id = @id FOR UPDATE;

-- name: DecideCustomerBlockAppeal :one
UPDATE customer_block_appeals SET status = @status, answer = @answer, decided_by = @decided_by, decided_at = now()
WHERE tenant_id = app_tenant_id() AND id = @id AND status = 'open' RETURNING *;

-- The block was lifted by other means (the seller's own lift, the end of a block): an appeal still open has nothing left to ask for.
-- name: CloseCustomerBlockAppeals :exec
UPDATE customer_block_appeals SET status = 'closed', decided_by = 'system', decided_at = now()
WHERE tenant_id = app_tenant_id() AND customer_id = @customer_id AND status = 'open';

-- The latest 20, newest first.
-- name: ListCustomerBlockAppeals :many
SELECT * FROM customer_block_appeals WHERE tenant_id = app_tenant_id() AND customer_id = @customer_id
ORDER BY created_at DESC, id DESC LIMIT 20;

-- name: ResetCustomerBlockAppeals :execrows
DELETE FROM customer_block_appeals WHERE tenant_id = $1;
