-- People: users, memberships and sessions (phase 38). Run as plans_system from internal/identity only. None of these tables is tenant data.

-- name: InsertUser :one
INSERT INTO users (id, email, display_name, password_hash) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(@email::text);

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- A wrong password. After a lock has run out the count starts again at one instead of locking at once on the next slip.
-- name: RecordFailedLogin :one
UPDATE users SET
  failed_logins = CASE WHEN locked_until <= now() THEN 1 ELSE failed_logins + 1 END,
  locked_until = CASE
    WHEN locked_until <= now() THEN NULL
    WHEN failed_logins + 1 >= @max_failures::int THEN now() + @lock_for::interval
    ELSE locked_until
  END
WHERE id = @id
RETURNING failed_logins, locked_until;

-- name: RecordSuccessfulLogin :exec
UPDATE users SET failed_logins = 0, locked_until = NULL, last_login_at = now() WHERE id = $1;

-- name: InsertMember :exec
INSERT INTO account_members (account_id, user_id, role) VALUES ($1, $2, $3);

-- name: ListUserMemberships :many
SELECT m.account_id, a.name AS account_name, m.role
FROM account_members m JOIN accounts a ON a.id = m.account_id
WHERE m.user_id = $1 ORDER BY a.created_at, a.id;

-- name: GetMembership :one
SELECT m.role, a.name AS account_name, a.created_at
FROM account_members m JOIN accounts a ON a.id = m.account_id
WHERE m.account_id = @account_id AND m.user_id = @user_id;

-- name: ListAccountMembers :many
SELECT u.id AS user_id, u.email, u.display_name, m.role, m.created_at
FROM account_members m JOIN users u ON u.id = m.user_id
WHERE m.account_id = $1 ORDER BY m.created_at, u.id;

-- name: InsertSession :one
INSERT INTO user_sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4) RETURNING *;

-- A session is live while it is neither revoked nor expired; the clock is the database's.
-- name: GetLiveSession :one
SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, u.email, u.display_name, u.created_at AS user_created_at
FROM user_sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > now();

-- name: SlideSession :exec
UPDATE user_sessions SET last_seen_at = now(), expires_at = @expires_at WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE user_sessions SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: DeleteStaleSessions :exec
DELETE FROM user_sessions WHERE user_id = $1 AND (expires_at < now() - interval '1 day' OR revoked_at < now() - interval '1 day');

-- An app of an account the user is a member of, with the user's role. No membership, no row.
-- name: GetAppForUser :one
SELECT a.id, a.account_id, a.name, a.slug, a.created_at, m.role
FROM apps a JOIN account_members m ON m.account_id = a.account_id AND m.user_id = @user_id
WHERE a.id = @app_id;

-- name: GetTenantForUser :one
SELECT t.*, m.role
FROM tenants t JOIN account_members m ON m.account_id = t.account_id AND m.user_id = @user_id
WHERE t.id = @tenant_id;

-- name: ListAppTenants :many
SELECT * FROM tenants WHERE app_id = $1 ORDER BY environment DESC;

-- name: ListApiKeysOfTenants :many
SELECT * FROM api_keys WHERE tenant_id = ANY(@tenant_ids::uuid[]) ORDER BY created_at DESC, id;
