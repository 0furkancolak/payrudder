-- Phase 42: the three ways to sign in. E-mail + password existed; this adds the one-time code (OTP, passwordless) and Google. Like the other identity tables
-- these are not tenant data: only the identity module, on the system pool, reads or writes them.

-- +goose Up
-- A one-time sign-in code mailed to an address. The row is keyed by the address, not by a user: an address without an account can sign up with a code. Only a
-- keyed hash of the code is stored (HMAC-SHA256 over salt, address and code, keyed with the server's pepper), the code has 6 digits, lives 10 minutes, works
-- once and is burned after 5 wrong tries.
CREATE TABLE login_codes (
  id          uuid PRIMARY KEY,
  email       text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  salt        bytea NOT NULL CHECK (length(salt) = 16),
  code_hash   bytea NOT NULL CHECK (length(code_hash) = 32),
  attempts    int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX login_codes_by_email ON login_codes (email, created_at DESC);

-- A login at an identity provider, linked to a user: (provider, subject) is what the provider calls the person and never changes, unlike the address.
CREATE TABLE user_identities (
  id             uuid PRIMARY KEY,
  user_id        uuid NOT NULL REFERENCES users (id),
  provider       text NOT NULL CHECK (provider IN ('google')),
  subject        text NOT NULL CHECK (length(subject) BETWEEN 1 AND 255),
  -- The address the provider reported when the link was made; informational (the user's own address is users.email).
  email          text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  last_login_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (provider, subject)
);
CREATE INDEX user_identities_by_user ON user_identities (user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON login_codes TO plans_system;
GRANT SELECT, INSERT, UPDATE ON user_identities TO plans_system;

-- +goose Down
DROP TABLE user_identities;
DROP TABLE login_codes;
