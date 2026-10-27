package command

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	saasmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Guard refuses the creation of a resource beyond the plan the account is on (phase 55, ADR 0024). The implementation runs in the caller's transaction
// and expects the account's lock (LockAccount) to be held. A Commands without a guard (SaaS off) creates freely.
type Guard interface {
	RequireRoom(ctx context.Context, db sqlc.DBTX, account uuid.UUID, r saasmodel.Resource) error
}

// WithGuard makes the creation of apps, invitations and keys obey the account's plan. A nil guard leaves them unlimited.
func (c *Commands) WithGuard(g Guard) *Commands {
	c.guard = g
	return c
}

// room asks the guard, if there is one, whether one more of r fits in the account's plan.
func (c *Commands) room(ctx context.Context, db sqlc.DBTX, account uuid.UUID, r saasmodel.Resource) error {
	if c.guard == nil {
		return nil
	}
	return c.guard.RequireRoom(ctx, db, account, r)
}

// issueWithinPlan issues an API key for a tenant of the account, after the plan says there is room for one more. The check and the issue are one
// critical section: the account's lock is held in a transaction of its own for the length of the issue, which commits on its own connection, so a
// second request waits for the lock and then counts the key the first made.
func (c *Commands) issueWithinPlan(ctx context.Context, accountID string, tenantID uuid.UUID, role string, perms []string) (*plansv1.ApiKey, string, error) {
	aid, err := uuid.Parse(accountID)
	if c.guard == nil || err != nil {
		return c.keys.Issue(ctx, tenantID, role, perms)
	}
	var key *plansv1.ApiKey
	var plaintext string
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.LockAccount(ctx, aid.String()); err != nil {
			return err
		}
		if err := c.room(ctx, tx, aid, saasmodel.APIKeys); err != nil {
			return err
		}
		var err error
		key, plaintext, err = c.keys.Issue(ctx, tenantID, role, perms)
		return err
	})
	return key, plaintext, err
}
