-- Phase 39: members join an account by invitation. An invitation is a single-use token mailed to an address; only its SHA-256 is stored. Like the other
-- identity tables it is not tenant data: only the identity module, on the system pool, reads or writes it.

-- +goose Up
CREATE TABLE account_invites (
  id          uuid PRIMARY KEY,
  account_id  uuid NOT NULL REFERENCES accounts (id),
  email       text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
  role        text NOT NULL CHECK (role IN ('owner', 'admin', 'viewer')),
  invited_by  uuid NOT NULL REFERENCES users (id),
  token_hash  bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_at  timestamptz NOT NULL DEFAULT now(),
  -- When the mail last went out (a resend moves it), for the one-a-minute rule.
  sent_at     timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  accepted_at timestamptz,
  revoked_at  timestamptz
);
-- One open invitation per address and account; a new invitation to the same address refreshes it instead of adding a second.
CREATE UNIQUE INDEX account_invites_open ON account_invites (account_id, lower(email)) WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX account_invites_by_account ON account_invites (account_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE ON account_invites TO plans_system;
-- Members change role and leave or are removed.
GRANT UPDATE, DELETE ON account_members TO plans_system;

-- +goose Down
REVOKE UPDATE, DELETE ON account_members FROM plans_system;
DROP TABLE account_invites;
