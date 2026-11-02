-- Runtime health of a tenant's provider accounts, and where a charge may go when an account is definitely down (docs/plans/22 G8, docs/provider-health.md).
-- Like the other tables of payments_db these hold ids, words and times only, and have no row-level security (only payments_system touches them).
-- +goose Up

-- The last known state of an account (keyed like provider_accounts, by the account key: provider or provider~label). A periodic check writes status and the check's time;
-- the circuit breaker of the account writes breaker and the failures in a row when it changes. last_error is a class (unreachable, bad_credentials, ...), never a message
-- of the provider. The breaker itself lives in each worker's memory: this row is what a person and the integrations page see, and a worker that starts begins closed.
CREATE TABLE provider_account_health (
  tenant_id             uuid NOT NULL,
  provider              text NOT NULL,
  environment           text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  status                text NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'healthy', 'bad_credentials', 'down')),
  breaker               text NOT NULL DEFAULT 'closed' CHECK (breaker IN ('closed', 'open', 'half_open')),
  consecutive_failures  bigint NOT NULL DEFAULT 0,
  last_error            text NOT NULL DEFAULT '',
  last_checked_at       timestamptz,
  last_ok_at            timestamptz,
  last_failure_at       timestamptz,
  opened_at             timestamptz,
  updated_at            timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, provider, environment)
);

-- Where a new charge may go instead when the account it was routed to definitely did not process it (a connection that never opened, or an open circuit): one account, in the
-- same environment, one hop (the target's own entry is not followed). No row means no failover, which is the default (not agreed). A timeout never fails over, whatever is set here.
CREATE TABLE provider_failover (
  tenant_id    uuid NOT NULL,
  environment  text NOT NULL CHECK (environment IN ('sandbox', 'live')),
  provider     text NOT NULL,
  failover_to  text NOT NULL,
  updated_by   text NOT NULL DEFAULT '',
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, environment, provider),
  CHECK (failover_to <> provider)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON provider_account_health, provider_failover TO payments_system;
GRANT SELECT ON provider_account_health, provider_failover TO payments_app;

-- +goose Down
DROP TABLE provider_failover, provider_account_health;
