-- The dedup record of every email. notifier runs as notifier_system.

-- name: ClaimNotification :execrows
INSERT INTO sent_notifications (dedup_key, command_id, kind, template, recipient, tenant_id, status)
VALUES (@dedup_key, @command_id, @kind, @template, @recipient, @tenant_id, 'SENDING')
ON CONFLICT (dedup_key) DO NOTHING;

-- name: LockNotification :one
SELECT status, claimed_at, smtp_message_id FROM sent_notifications WHERE dedup_key = $1 FOR UPDATE;

-- name: RenewClaim :exec
UPDATE sent_notifications SET claimed_at = now() WHERE dedup_key = $1;

-- name: MarkNotificationSent :exec
UPDATE sent_notifications SET status = 'SENT', sent_at = now(), smtp_message_id = $2 WHERE dedup_key = $1;

-- name: ReleaseNotification :exec
DELETE FROM sent_notifications WHERE dedup_key = $1 AND status = 'SENDING';

-- name: GetSentMessageID :one
SELECT smtp_message_id FROM sent_notifications WHERE dedup_key = $1 AND status = 'SENT';

-- name: NotificationByMessageID :many
-- The mail a provider's bounce or complaint is about: the tenant it was sent for and the address it went to. Two rows are enough to see that the id is not unique.
SELECT tenant_id, recipient FROM sent_notifications WHERE smtp_message_id = $1 AND status = 'SENT' ORDER BY sent_at LIMIT 2;

-- A tenant's recent mail, newest first, a page at a time (the keyset is the claim time and the dedup key), with what the provider reported of each.
-- name: ListTenantMessages :many
SELECT dedup_key, kind, template, recipient, status, claimed_at, sent_at, delivery_state
FROM sent_notifications
WHERE tenant_id = @tenant_id
  AND (@after_key::text = '' OR (claimed_at, dedup_key) < (@after_at::timestamptz, @after_key::text))
ORDER BY claimed_at DESC, dedup_key DESC
LIMIT @page_size;

-- What the provider reported of a message. A permanent bounce or a complaint is the last word: a late "delivered" or "delayed" does not take it back.
-- name: RecordDelivery :execrows
UPDATE sent_notifications SET delivery_state = @delivery_state
WHERE smtp_message_id = @message_id AND delivery_state NOT IN ('bounced', 'complained');
