-- Plan 22 (G9): two-factor sign-in for the console. A user has at most one authenticator app (TOTP, RFC 6238): its secret is sealed with the platform's secrets key and bound to
-- the user, and it counts only once the first code was proven (confirmed_at). last_step is the last 30-second step a code was accepted for, so a code works once. Ten
-- single-use recovery codes are kept as keyed hashes. A sign-in that needs the second factor leaves a login_challenge (a token kept as a hash, five tries, five
-- minutes) instead of a session. Everything here is about people, not tenants: the pooled database only, no tenant twin.
-- accounts.require_two_factor is the owner's setting that the owners of the account must have it (default off).

-- +goose Up
CREATE TABLE user_totp (
  user_id      uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  secret_enc   bytea NOT NULL,
  confirmed_at timestamptz,
  last_step    bigint NOT NULL DEFAULT 0,
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_recovery_codes (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  code_hash  bytea NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (user_id, code_hash)
);

CREATE TABLE login_challenges (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash bytea NOT NULL UNIQUE,
  attempts   int NOT NULL DEFAULT 0,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_challenges_by_user ON login_challenges (user_id);

ALTER TABLE accounts ADD COLUMN require_two_factor boolean NOT NULL DEFAULT false;

GRANT SELECT, INSERT, UPDATE, DELETE ON user_totp, user_recovery_codes, login_challenges TO plans_system;

-- +goose Down
ALTER TABLE accounts DROP COLUMN require_two_factor;
DROP TABLE login_challenges, user_recovery_codes, user_totp;
