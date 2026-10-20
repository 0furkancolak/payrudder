-- The dedup record of every email. notifier runs as notifier_system.

-- name: ClaimNotification :execrows
INSERT INTO sent_notifications (dedup_key, command_id, kind, template, recipient, status)
VALUES (@dedup_key, @command_id, @kind, @template, @recipient, 'SENDING')
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
