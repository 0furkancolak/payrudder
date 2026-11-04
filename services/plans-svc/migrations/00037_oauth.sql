-- Plan 20, OAuth mode of the MCP server (default, not agreed): the identity service as an OAuth 2.1 authorization server. A GRANT is one person's consent to one client acting on
-- one tenant (app + environment) with some scopes, for one resource (the MCP server's canonical URL). A CODE is the one-time answer of the consent page, a TOKEN is an access
-- token (qo_, an hour), a refresh token (qr_, thirty days, used once) or the short-lived credential for the API audience (qm_, minutes) that the MCP server gets in exchange for
-- an access token. Only the SHA-256 of a code or token is stored (each carries 256 random bits), so a leaked table holds nothing that works. Revoking the grant ends every token.
-- Person tables: no tenant twin. oauth_grants has a tenant_id but is found by the hash of a token, before there is a tenant, like api_keys (listed in the RLS allowlist).

-- +goose Up
CREATE TABLE oauth_grants (
  id           uuid PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users (id),
  tenant_id    uuid NOT NULL REFERENCES tenants (id),
  client_id    text NOT NULL CHECK (char_length(client_id) BETWEEN 1 AND 2048),
  client_name  text NOT NULL CHECK (char_length(client_name) BETWEEN 1 AND 200),
  resource     text NOT NULL CHECK (char_length(resource) BETWEEN 1 AND 2048),
  scopes       text[] NOT NULL CHECK (cardinality(scopes) > 0),
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at   timestamptz
);
CREATE INDEX oauth_grants_by_user ON oauth_grants (user_id, created_at DESC);

CREATE TABLE oauth_codes (
  code_hash      bytea PRIMARY KEY CHECK (length(code_hash) = 32),
  grant_id       uuid NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
  redirect_uri   text NOT NULL CHECK (char_length(redirect_uri) <= 2048),
  code_challenge text NOT NULL CHECK (char_length(code_challenge) = 43),
  expires_at     timestamptz NOT NULL,
  used_at        timestamptz
);
CREATE INDEX oauth_codes_by_grant ON oauth_codes (grant_id);

CREATE TABLE oauth_tokens (
  token_hash bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  grant_id   uuid NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
  kind       text NOT NULL CHECK (kind IN ('access', 'refresh', 'api')),
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_tokens_by_grant ON oauth_tokens (grant_id);
CREATE INDEX oauth_tokens_by_expiry ON oauth_tokens (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_grants, oauth_codes, oauth_tokens TO plans_system;

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
