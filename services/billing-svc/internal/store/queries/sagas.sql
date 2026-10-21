-- Invoice sagas, their audit log and the reply inbox.

-- name: GetSagaIDByInvoice :one
SELECT id FROM invoice_sagas WHERE tenant_id = @tenant_id AND invoice_id = @invoice_id;

-- name: GetSaga :one
SELECT id, invoice_id, state, version, attempt, dunning_attempt, last_error FROM invoice_sagas WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListSagaEvents :many
SELECT seq, from_state, to_state, cause, command_id, at FROM saga_events WHERE tenant_id = @tenant_id AND saga_id = @saga_id ORDER BY seq;

-- name: LockSagaWithInvoice :one
SELECT s.tenant_id, s.id, s.invoice_id, i.customer_id, s.state, s.version, s.attempt, s.step_attempt, s.dunning_attempt,
       i.number, i.period_id, i.currency, i.status, i.version AS invoice_version, i.total_minor,
       COALESCE(i.finalized_at, i.created_at)::timestamptz AS finalized_at, i.snapshot
FROM invoice_sagas s JOIN invoices i ON i.tenant_id = s.tenant_id AND i.id = s.invoice_id
WHERE s.tenant_id = @tenant_id AND s.id = @id
FOR UPDATE OF s;

-- name: InsertSaga :execrows
INSERT INTO invoice_sagas (tenant_id, id, invoice_id, state, version) VALUES (@tenant_id, @id, @invoice_id, 'FINALIZED', 1)
ON CONFLICT (tenant_id, invoice_id) DO NOTHING;

-- name: TransitionSaga :execrows
UPDATE invoice_sagas SET state = @state, version = version + 1, attempt = @attempt, step_attempt = @step_attempt, step_deadline = sqlc.narg('step_deadline'),
       dunning_attempt = @dunning_attempt, next_attempt_at = sqlc.narg('next_attempt_at'), last_error = NULLIF(@last_error::text, ''), updated_at = @updated_at
WHERE tenant_id = @tenant_id AND id = @id AND version = @version AND state = @from_state;

-- name: ReissueSaga :exec
UPDATE invoice_sagas SET attempt = @attempt, step_attempt = @step_attempt, step_deadline = @step_deadline::timestamptz, version = version + 1, updated_at = @updated_at
WHERE tenant_id = @tenant_id AND id = @id;

-- name: NextSagaEventSeq :one
SELECT (COALESCE(max(seq), 0) + 1)::int AS seq FROM saga_events WHERE tenant_id = @tenant_id AND saga_id = @saga_id;

-- name: InsertSagaEvent :exec
INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, command_id, at)
VALUES (@tenant_id, @saga_id, @seq, @from_state, @to_state, @cause, sqlc.narg('command_id'), @at);

-- name: InsertSagaReissueEvent :exec
INSERT INTO saga_events (tenant_id, saga_id, seq, from_state, to_state, cause, at)
VALUES (@tenant_id, @saga_id, @seq, @state, @state, 'timeout:reissue', @at);

-- name: ListSagasPastDeadline :many
SELECT tenant_id, id FROM invoice_sagas WHERE step_deadline IS NOT NULL AND step_deadline < @now::timestamptz ORDER BY step_deadline LIMIT @row_limit;

-- name: ListSagasDueForDunning :many
SELECT tenant_id, id FROM invoice_sagas WHERE state = 'PAYMENT_DECLINED' AND next_attempt_at <= @now::timestamptz ORDER BY next_attempt_at LIMIT @row_limit;

-- name: LockSagaPastDeadline :one
SELECT id FROM invoice_sagas
WHERE tenant_id = @tenant_id AND id = @id AND step_deadline IS NOT NULL AND step_deadline < @now::timestamptz
FOR UPDATE SKIP LOCKED;

-- name: LockSagaDueForDunning :one
SELECT id FROM invoice_sagas
WHERE tenant_id = @tenant_id AND id = @id AND state = 'PAYMENT_DECLINED' AND next_attempt_at <= @now::timestamptz
FOR UPDATE SKIP LOCKED;

-- name: ListSagasDueForPoll :many
SELECT tenant_id, id FROM invoice_sagas WHERE state = 'PAYMENT_PENDING' AND next_attempt_at <= @now::timestamptz ORDER BY next_attempt_at LIMIT @row_limit;

-- name: LockSagaDueForPoll :one
SELECT id FROM invoice_sagas
WHERE tenant_id = @tenant_id AND id = @id AND state = 'PAYMENT_PENDING' AND next_attempt_at <= @now::timestamptz
FOR UPDATE SKIP LOCKED;

-- name: InsertInboxReply :execrows
INSERT INTO inbox_replies (message_id) VALUES (@message_id) ON CONFLICT DO NOTHING;

-- name: CountSagasByState :many
SELECT state, count(*) AS sagas FROM invoice_sagas GROUP BY state;
