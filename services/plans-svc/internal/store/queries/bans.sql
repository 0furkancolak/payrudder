-- Plan 14: bans of an account and of a person (a user). The flag is banned_at; every ban and lift is kept in ban_events. Run as plans_system.

-- name: LockAccountForBan :one
SELECT id, banned_at FROM accounts WHERE id = $1 AND NOT platform FOR UPDATE;

-- name: LockUserForBan :one
SELECT id, banned_at FROM users WHERE lower(email) = lower(@email::text) FOR UPDATE;

-- name: SetAccountBan :exec
UPDATE accounts SET banned_at = CASE WHEN @banned::bool THEN now() END WHERE id = @id;

-- name: SetUserBan :exec
UPDATE users SET banned_at = CASE WHEN @banned::bool THEN now() END WHERE id = @id;

-- name: InsertBanEvent :exec
INSERT INTO ban_events (id, subject, subject_id, kind, category, reason, actor, approved_by)
VALUES (@id, @subject, @subject_id, @kind, @category, @reason, @actor, @approved_by);

-- name: ListBanEvents :many
SELECT * FROM ban_events WHERE subject = @subject AND subject_id = @subject_id ORDER BY created_at DESC, id DESC LIMIT 100;

-- name: LatestBanEvent :one
SELECT * FROM ban_events WHERE subject = @subject AND subject_id = @subject_id ORDER BY created_at DESC, id DESC LIMIT 1;

-- name: AccountIsBanned :one
SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1 AND banned_at IS NOT NULL);

-- name: ListBannedAccountIDs :many
SELECT id FROM accounts WHERE banned_at IS NOT NULL;

-- name: ListAccountTenantIDs :many
SELECT id FROM tenants WHERE account_id = $1;

-- name: GetUserBannedByEmail :one
SELECT id, banned_at FROM users WHERE lower(email) = lower(@email::text);
