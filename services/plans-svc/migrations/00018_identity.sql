-- Phase 38: people. A user signs up, owns an account (organisation) and signs in with a session token. Users are not tenants: none of these tables
-- has a tenant_id and plans_app has no grant on them; only the identity module, on the system pool, reads and writes them.

-- +goose Up
CREATE TABLE users (
  id             uuid PRIMARY KEY,
  -- Trimmed and lower-cased by the module; the unique index below makes it case-insensitive even if a caller forgets.
  email          text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  display_name   text NOT NULL DEFAULT '' CHECK (length(display_name) <= 200),
  -- PHC string of argon2id (parameters travel with the hash, so they can be raised later and old hashes still verify).
  password_hash  text NOT NULL,
  failed_logins  int NOT NULL DEFAULT 0 CHECK (failed_logins >= 0),
  locked_until   timestamptz,
  last_login_at  timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE account_members (
  account_id  uuid NOT NULL REFERENCES accounts (id),
  user_id     uuid NOT NULL REFERENCES users (id),
  role        text NOT NULL CHECK (role IN ('owner', 'admin', 'viewer')),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (account_id, user_id)
);
CREATE INDEX account_members_by_user ON account_members (user_id);

-- The token is opaque and random; only its SHA-256 is stored, so a database leak cannot be replayed as sessions.
CREATE TABLE user_sessions (
  id            uuid PRIMARY KEY,
  user_id       uuid NOT NULL REFERENCES users (id),
  token_hash    bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_seen_at  timestamptz NOT NULL DEFAULT now(),
  expires_at    timestamptz NOT NULL,
  revoked_at    timestamptz
);
CREATE INDEX user_sessions_by_user ON user_sessions (user_id);

GRANT SELECT, INSERT, UPDATE ON users TO plans_system;
GRANT SELECT, INSERT ON account_members TO plans_system;
GRANT SELECT, INSERT, UPDATE, DELETE ON user_sessions TO plans_system;

-- +goose Down
DROP TABLE user_sessions;
DROP TABLE account_members;
DROP TABLE users;
