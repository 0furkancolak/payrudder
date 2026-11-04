-- OAuth 2.1 authorization server (plan 20, default, not agreed). Run as plans_system from internal/identity/oauth only. A code and a token are found by the SHA-256 of what the
-- client holds; the clock is the database's.

-- name: InsertOAuthGrant :one
INSERT INTO oauth_grants (id, user_id, tenant_id, client_id, client_name, resource, scopes) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;

-- name: InsertOAuthCode :exec
INSERT INTO oauth_codes (code_hash, grant_id, redirect_uri, code_challenge, expires_at) VALUES (@code_hash, @grant_id, @redirect_uri, @code_challenge, now() + @ttl::interval);

-- The code is spent by the first try, right or wrong: a guess cannot be repeated. A second presentation finds no row here (see OAuthCodeSpent).
-- name: SpendOAuthCode :one
UPDATE oauth_codes SET used_at = now() WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING grant_id, redirect_uri, code_challenge;

-- A code that exists and was spent already: its presentation is a replay, and the grant it opened ends.
-- name: OAuthCodeSpent :one
SELECT grant_id FROM oauth_codes WHERE code_hash = $1 AND used_at IS NOT NULL;

-- name: GetOAuthGrant :one
SELECT * FROM oauth_grants WHERE id = $1;

-- name: InsertOAuthToken :exec
INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES (@token_hash, @grant_id, @kind, now() + @ttl::interval);

-- A token with what the answer to a presentation needs: live is "not expired, not used (a refresh token), and its grant is not revoked".
-- name: GetOAuthToken :one
SELECT t.kind, t.expires_at, g.id AS grant_id, g.user_id, g.tenant_id, g.client_id, g.client_name, g.resource, g.scopes, u.email, u.email_verified_at,
       (t.expires_at > now() AND t.used_at IS NULL AND g.revoked_at IS NULL)::boolean AS live
FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id JOIN users u ON u.id = g.user_id
WHERE t.token_hash = $1;

-- A refresh token is used once. No row: unknown, expired, used already or its grant ended.
-- name: SpendRefreshToken :one
UPDATE oauth_tokens t SET used_at = now() FROM oauth_grants g
WHERE t.token_hash = $1 AND t.kind = 'refresh' AND t.used_at IS NULL AND t.expires_at > now() AND g.id = t.grant_id AND g.revoked_at IS NULL
RETURNING t.grant_id;

-- A refresh token that was used before: presented again, it ends the grant (it was stolen, or the client lost its copy and the thief may have it).
-- name: RefreshTokenSpent :one
SELECT grant_id FROM oauth_tokens WHERE token_hash = $1 AND kind = 'refresh' AND used_at IS NOT NULL;

-- name: TouchOAuthGrant :exec
UPDATE oauth_grants SET last_used_at = now() WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');

-- name: RevokeOAuthGrant :execrows
UPDATE oauth_grants SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- RFC 7009: the token's own grant, and only for the client it was issued to (an empty client_id is the caller that already proved it is the person: not used by the endpoint).
-- name: RevokeOAuthGrantOfToken :execrows
UPDATE oauth_grants g SET revoked_at = now() FROM oauth_tokens t
WHERE t.token_hash = @token_hash AND g.id = t.grant_id AND g.client_id = @client_id AND g.revoked_at IS NULL;

-- name: RevokeUserOAuthGrant :one
UPDATE oauth_grants SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL RETURNING tenant_id, client_name;

-- name: ListUserOAuthGrants :many
SELECT g.id, g.client_id, g.client_name, g.tenant_id, g.resource, g.scopes, g.created_at, g.last_used_at, t.environment, a.name AS app_name
FROM oauth_grants g JOIN tenants t ON t.id = g.tenant_id JOIN apps a ON a.id = t.app_id
WHERE g.user_id = $1 AND g.revoked_at IS NULL ORDER BY g.created_at DESC, g.id LIMIT 100;

-- Whatever has expired is of no use; a day later it is deleted, as the sign-in codes are.
-- name: DeleteStaleOAuth :exec
WITH c AS (DELETE FROM oauth_codes WHERE expires_at < now() - interval '1 day'),
     t AS (DELETE FROM oauth_tokens WHERE expires_at < now() - interval '1 day')
DELETE FROM oauth_grants g WHERE g.revoked_at < now() - interval '30 days';
