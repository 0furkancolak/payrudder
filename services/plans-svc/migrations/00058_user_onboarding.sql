-- What the console remembers for a person rather than for an account: how far they are in the first-run wizard, and which page tours they have seen. It used to
-- live in the browser's storage, so a second browser started the wizard and every tour again. Like the other person tables (users, user_sessions) it has no
-- tenant_id and plans_app has no grant on it: only the identity module, on the system pool, reads and writes it, always for the user the gateway resolved from
-- the session.
--
-- wizard is the progress document (status, step, what was chosen and made); seen_tours is a set of tour ids. Both are merged on write, never replaced: see
-- identity/onboarding.

-- +goose Up
CREATE TABLE user_onboarding (
  user_id     uuid PRIMARY KEY REFERENCES users (id),
  -- NULL: the wizard was never started. The shape is checked by the module; the database only keeps it an object.
  wizard      jsonb CHECK (jsonb_typeof(wizard) = 'object'),
  seen_tours  text[] NOT NULL DEFAULT '{}',
  updated_at  timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE ON user_onboarding TO plans_system;

-- +goose Down
DROP TABLE user_onboarding;
