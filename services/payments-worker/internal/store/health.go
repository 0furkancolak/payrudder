package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// Statuses of an account's health (provider_account_health.status).
const (
	HealthUnknown        = "unknown"         // never checked, or its adapter has no call to ask the provider with
	HealthHealthy        = "healthy"         // the provider answered a read-only call with the credentials
	HealthBadCredentials = "bad_credentials" // the provider answered and refused them
	HealthDown           = "down"            // the provider could not be reached, or the circuit is open
)

// AccountRef names a provider account: the tenant's, in an environment, by its account key (provider or provider~label).
type AccountRef struct {
	Tenant      uuid.UUID
	Provider    string
	Environment string
}

// Health is what is known of an account's health, as the operator API shows it.
type Health struct {
	Provider            string     `json:"provider"`
	Environment         string     `json:"environment"`
	IsDefault           bool       `json:"is_default"`
	Status              string     `json:"status"`
	Breaker             string     `json:"breaker"`
	ConsecutiveFailures int64      `json:"consecutive_failures"`
	LastError           string     `json:"last_error,omitempty"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
	LastOKAt            *time.Time `json:"last_ok_at,omitempty"`
	LastFailureAt       *time.Time `json:"last_failure_at,omitempty"`
	OpenedAt            *time.Time `json:"opened_at,omitempty"`
	FailoverTo          string     `json:"failover_to,omitempty"`
}

// ProviderHealth is the health state of provider accounts and the failover configuration.
type ProviderHealth struct{ DB DB }

// PutCheck keeps what a periodic check found. ok is when the provider answered, failed when it did not; the other is left as it was.
func (p ProviderHealth) PutCheck(ctx context.Context, ref AccountRef, status, lastError string, checked time.Time, ok, failed *time.Time) error {
	return at(ctx, p.DB, ref.Tenant, func(q *sqlc.Queries) error {
		return q.PutAccountCheck(ctx, sqlc.PutAccountCheckParams{TenantID: ref.Tenant, Provider: ref.Provider, Environment: ref.Environment, Status: status, LastError: lastError,
			CheckedAt: &checked, LastOkAt: ok, LastFailureAt: failed})
	})
}

// PutBreaker keeps the state of an account's circuit breaker when it changes.
func (p ProviderHealth) PutBreaker(ctx context.Context, ref AccountRef, breaker string, failures int64, lastError string, opened, failed *time.Time) error {
	status := HealthDown
	if breaker == "closed" {
		status = HealthHealthy
	}
	return at(ctx, p.DB, ref.Tenant, func(q *sqlc.Queries) error {
		return q.PutAccountBreaker(ctx, sqlc.PutAccountBreakerParams{TenantID: ref.Tenant, Provider: ref.Provider, Environment: ref.Environment, Status: status, Breaker: breaker,
			Failures: failures, LastError: lastError, OpenedAt: opened, LastFailureAt: failed})
	})
}

// List returns the tenant's accounts with their health and failover target.
func (p ProviderHealth) List(ctx context.Context, tenant uuid.UUID) ([]Health, error) {
	rows, err := get(ctx, p.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListAccountHealthRow, error) { return q.ListAccountHealth(ctx, tenant) })
	if err != nil {
		return nil, err
	}
	out := make([]Health, len(rows))
	for i, r := range rows {
		out[i] = Health{Provider: r.Provider, Environment: r.Environment, IsDefault: r.IsDefault, Status: r.Status, Breaker: r.Breaker, ConsecutiveFailures: r.ConsecutiveFailures, LastError: r.LastError,
			LastCheckedAt: r.LastCheckedAt, LastOKAt: r.LastOkAt, LastFailureAt: r.LastFailureAt, OpenedAt: r.OpenedAt, FailoverTo: r.FailoverTo}
	}
	return out, nil
}

// Accounts returns every provider account of every tenant: the pooled database's and each serving tenant's of a cell (one that is moving is left for the next pass).
func (p ProviderHealth) Accounts(ctx context.Context) ([]AccountRef, error) {
	seen := map[AccountRef]bool{}
	var out []AccountRef
	add := func(rows []sqlc.ListAllAccountsRow) {
		for _, r := range rows {
			ref := AccountRef{Tenant: r.TenantID, Provider: r.Provider, Environment: r.Environment}
			if !seen[ref] {
				seen[ref] = true
				out = append(out, ref)
			}
		}
	}
	rows, err := get(ctx, p.DB, uuid.Nil, func(q *sqlc.Queries) ([]sqlc.ListAllAccountsRow, error) { return q.ListAllAccounts(ctx) })
	if err != nil {
		return nil, err
	}
	add(rows)
	if r, ok := p.DB.(*pgxx.Router); ok {
		err = r.EachReady(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
			rows, err := get(ctx, p.DB, t.TenantID, func(q *sqlc.Queries) ([]sqlc.ListAllAccountsRow, error) { return q.ListAllAccounts(ctx) })
			add(rows)
			return err
		})
	}
	return out, err
}

// Failover returns the account a new charge to provider may go to when provider definitely did not process it, if one is configured.
func (p ProviderHealth) Failover(ctx context.Context, tenant uuid.UUID, environment, provider string) (string, bool, error) {
	return lookup(ctx, p.DB, tenant, func(q *sqlc.Queries) (string, error) {
		return q.GetFailover(ctx, sqlc.GetFailoverParams{TenantID: tenant, Environment: environment, Provider: provider})
	})
}

// SetFailover sets the failover account of provider, and fails (ErrNoAccount) when either account does not exist in the environment.
func (p ProviderHealth) SetFailover(ctx context.Context, tenant uuid.UUID, environment, provider, to, by string) error {
	return tx(ctx, p.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		for _, name := range []string{provider, to} {
			if _, err := q.GetProviderAccountSettings(ctx, sqlc.GetProviderAccountSettingsParams{TenantID: tenant, Provider: name, Environment: environment}); err != nil {
				if notFound(err) == nil {
					return ErrNoAccount
				}
				return err
			}
		}
		return q.PutFailover(ctx, sqlc.PutFailoverParams{TenantID: tenant, Environment: environment, Provider: provider, FailoverTo: to, UpdatedBy: by})
	})
}

// ClearFailover removes the failover of provider and reports whether there was one.
func (p ProviderHealth) ClearFailover(ctx context.Context, tenant uuid.UUID, environment, provider string) (bool, error) {
	n, err := get(ctx, p.DB, tenant, func(q *sqlc.Queries) (int64, error) {
		return q.DeleteFailover(ctx, sqlc.DeleteFailoverParams{TenantID: tenant, Environment: environment, Provider: provider})
	})
	return n == 1, err
}

// FailoverAttempt is a charge attempt that was made at another account after the account the charge was routed to definitely did not process it.
type FailoverAttempt struct {
	// Provider is the account key the attempt was made at, and Key the idempotency key it was made under.
	Provider, Key string
}

// FailoverAttempt returns the failover attempt of the charge made under key for the invoice, if there is one: a command delivered again goes on there and never back to
// the account it failed over from, and a status query asks there.
func (a Attempts) FailoverAttempt(ctx context.Context, tenant, invoice uuid.UUID, prefix string) (FailoverAttempt, bool, error) {
	r, ok, err := lookup(ctx, a.DB, tenant, func(q *sqlc.Queries) (sqlc.GetFailoverAttemptRow, error) {
		return q.GetFailoverAttempt(ctx, sqlc.GetFailoverAttemptParams{TenantID: tenant, InvoiceID: invoice, Prefix: prefix})
	})
	if !ok {
		return FailoverAttempt{}, false, err
	}
	return FailoverAttempt{Provider: r.Provider, Key: r.IdempotencyKey}, true, nil
}
