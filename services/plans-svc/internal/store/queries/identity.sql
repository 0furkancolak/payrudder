-- People: users, memberships and sessions (phase 38). Run as plans_system from internal/identity only. None of these tables is tenant data.

-- name: InsertUser :one
INSERT INTO users (id, email, display_name, password_hash, locale) VALUES ($1, $2, $3, $4, $5) RETURNING *;

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
SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, u.email, u.display_name, u.created_at AS user_created_at, u.email_verified_at
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

-- E-mail verification and password reset (phase 39). A token row holds only the SHA-256 of the mailed token.
-- name: InsertUserToken :exec
INSERT INTO user_tokens (id, user_id, kind, token_hash, expires_at) VALUES ($1, $2, $3, $4, $5);

-- The newest token of a kind the user was sent, for the "at most one a minute" rule.
-- name: LastUserTokenAt :one
SELECT created_at FROM user_tokens WHERE user_id = @user_id AND kind = @kind ORDER BY created_at DESC LIMIT 1;

-- Reading a token without using it, so a bad one is refused before the work of hashing a new password.
-- name: GetLiveUserToken :one
SELECT user_id FROM user_tokens WHERE token_hash = @token_hash AND kind = @kind AND used_at IS NULL AND expires_at > now();

-- Using a token is one atomic UPDATE: of two requests carrying the same token exactly one gets a row.
-- name: ConsumeUserToken :one
UPDATE user_tokens SET used_at = now()
WHERE token_hash = @token_hash AND kind = @kind AND used_at IS NULL AND expires_at > now()
RETURNING user_id;

-- A new link replaces the older ones: they stop working.
-- name: InvalidateUserTokens :exec
UPDATE user_tokens SET used_at = now() WHERE user_id = @user_id AND kind = @kind AND used_at IS NULL;

-- name: DeleteStaleUserTokens :exec
DELETE FROM user_tokens WHERE user_id = $1 AND (expires_at < now() - interval '1 day' OR used_at < now() - interval '1 day');

-- name: MarkEmailVerified :exec
UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE id = $1;

-- A new password also clears the sign-in lock: the person has proven the mailbox.
-- name: SetPassword :exec
UPDATE users SET password_hash = @password_hash, failed_logins = 0, locked_until = NULL WHERE id = @id;

-- name: RevokeUserSessions :exec
UPDATE user_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- "Somebody tried to sign up with your address": at most one such mail an hour per address, so sign-up cannot be used to flood an inbox.
-- name: TouchTakenNotice :one
UPDATE users SET taken_notice_at = now()
WHERE id = $1 AND (taken_notice_at IS NULL OR taken_notice_at < now() - interval '1 hour')
RETURNING id;

-- name: SetUserLocale :exec
UPDATE users SET locale = $2 WHERE id = $1;

-- Sign-in by one-time code (phase 41). A row holds a keyed hash of the code, never the code.
-- name: InsertLoginCode :exec
INSERT INTO login_codes (id, email, salt, code_hash, expires_at) VALUES (@id, @email, @salt, @code_hash, now() + @ttl::interval);

-- The newest code sent to an address, for the "at most one a minute" rule.
-- name: LastLoginCodeAt :one
SELECT created_at FROM login_codes WHERE email = @email ORDER BY created_at DESC LIMIT 1;

-- name: CountRecentLoginCodes :one
SELECT count(*) FROM login_codes WHERE email = @email AND created_at > now() - @within::interval;

-- A new code replaces the older ones: they stop working.
-- name: InvalidateLoginCodes :exec
UPDATE login_codes SET used_at = now() WHERE email = @email AND used_at IS NULL;

-- name: DeleteStaleLoginCodes :exec
DELETE FROM login_codes WHERE email = @email AND created_at < now() - interval '1 day';

-- One try at the newest live code of an address: the attempt is counted in the same statement that returns the hash, so parallel guesses cannot exceed the
-- limit (the outer conditions are re-checked after a concurrent update). A code at its limit, used or expired returns no row.
-- name: TryLoginCode :one
UPDATE login_codes SET attempts = attempts + 1
WHERE id = (SELECT c.id FROM login_codes c WHERE c.email = @email AND c.used_at IS NULL AND c.expires_at > now() AND c.attempts < @max_attempts::int ORDER BY c.created_at DESC LIMIT 1)
  AND used_at IS NULL AND expires_at > now() AND attempts < @max_attempts::int
RETURNING id, salt, code_hash, attempts;

-- Spending a code is one atomic UPDATE: of two requests with the same right code exactly one gets a row.
-- name: ConsumeLoginCode :one
UPDATE login_codes SET used_at = now() WHERE id = $1 AND used_at IS NULL AND expires_at > now() RETURNING id;

-- Sign-in with an identity provider (phase 41).
-- name: GetIdentityUser :one
SELECT user_id FROM user_identities WHERE provider = @provider AND subject = @subject;

-- name: InsertIdentity :exec
INSERT INTO user_identities (id, user_id, provider, subject, email) VALUES ($1, $2, $3, $4, $5);

-- name: TouchIdentity :exec
UPDATE user_identities SET last_login_at = now() WHERE provider = @provider AND subject = @subject;

-- A password cleared: the account signs in by code or provider until the person sets a new one through "forgot password".
-- name: ClearPassword :exec
UPDATE users SET password_hash = '', failed_logins = 0, locked_until = NULL WHERE id = $1;

-- name: SetDisplayNameIfEmpty :exec
UPDATE users SET display_name = @display_name WHERE id = @id AND display_name = '';
