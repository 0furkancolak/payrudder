-- Phase 39: a person's e-mail address is proven (verification) and a forgotten password can be replaced (reset). Both use a single-use token mailed to the
-- address. Like the other identity tables, these are not tenant data: only the identity module, on the system pool, reads or writes them.

-- +goose Up
ALTER TABLE users
  -- Set when the person opens the mailed link (or resets the password through the mailed link, which proves the same thing). NULL: not proven.
  ADD COLUMN email_verified_at timestamptz,
  -- The language the person last used, for the mails sent to them (en, tr, de, fr, es, ar).
  ADD COLUMN locale text NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'tr', 'de', 'fr', 'es', 'ar')),
  -- When the owner of an address was last told that somebody tried to sign up with it; at most one such mail an hour.
  ADD COLUMN taken_notice_at timestamptz;

-- Only the SHA-256 of the token is stored (the token carries 256 random bits): a leaked database holds no working link.
CREATE TABLE user_tokens (
  id          uuid PRIMARY KEY,
  user_id     uuid NOT NULL REFERENCES users (id),
  kind        text NOT NULL CHECK (kind IN ('verify_email', 'reset_password')),
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  used_at     timestamptz
);
CREATE INDEX user_tokens_by_user ON user_tokens (user_id, kind, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON user_tokens TO plans_system;

-- +goose Down
DROP TABLE user_tokens;
ALTER TABLE users DROP COLUMN taken_notice_at, DROP COLUMN locale, DROP COLUMN email_verified_at;
