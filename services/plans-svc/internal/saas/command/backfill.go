package command

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// WithDB gives the commands the platform tenant's own database (the app role), which the backfill of an allowance writes to.
func (c *Commands) WithDB(db *store.DB) *Commands {
	c.db = db
	return c
}

// BackfillMails gives the platform's own plan versions that were published **before the `mails` meter existed** the allowance their plan names (default, not agreed:
// Free 100 hard, Pro 5,000 notify, Scale 50,000 notify; `model.DefaultPlans`), and publishes the entitlements of the subscriptions on them again, so that limits-svc
// learns the limit. A version is append-only, but its entitlements are rows of their own: the missing one is added, nothing else is touched. It runs at every start
// and is idempotent: a version that has the row is not listed, and a second run finds none.
//
// Versions published after the meter are left as their operator made them (leaving the mails out of one is a choice: no limit), and so are plans that are not one of
// the seeded three. It reports how many versions and subscriptions it handled. shortcut: one transaction per version, so a version with very many subscriptions
// writes them all in one; batch it if an installation has tens of thousands of organisations on one version.
func (c *Commands) BackfillMails(ctx context.Context) (versions, subscriptions int, err error) {
	if !c.cfg.Enabled || c.db == nil {
		return 0, 0, nil
	}
	p, err := c.qry.Platform(ctx)
	if err != nil {
		return 0, 0, err
	}
	pctx := p.Context(ctx)
	for _, spec := range model.DefaultPlans() {
		l := spec.Limits.Of(model.Mails)
		var todo []sqlc.ListVersionsWithoutEntitlementRow
		err := c.db.Tx(pctx, func(q *sqlc.Queries) error {
			since, err := q.FirstMeterTime(pctx, model.Mails.Meter())
			if err != nil {
				return err
			}
			todo, err = q.ListVersionsWithoutEntitlement(pctx, sqlc.ListVersionsWithoutEntitlementParams{PlanCode: spec.Code, Before: since, MeterCode: model.Mails.Meter()})
			return err
		})
		if err != nil {
			return versions, subscriptions, fmt.Errorf("saas: plan %s: %w", spec.Code, err)
		}
		for _, v := range todo {
			n, err := c.backfillVersion(pctx, p.TenantID, v.ID, l)
			if err != nil {
				return versions, subscriptions, fmt.Errorf("saas: plan %s version %d: %w", spec.Code, v.Version, err)
			}
			versions++
			subscriptions += n
			c.log.InfoContext(ctx, "saas: the mails allowance was added to a plan version that predates the meter", "plan", spec.Code, "version", v.Version, "subscriptions", n)
		}
	}
	return versions, subscriptions, nil
}

// backfillVersion adds the mails entitlement to one version and publishes the entitlements of its subscriptions, in one transaction.
func (c *Commands) backfillVersion(ctx context.Context, tenantID, versionID uuid.UUID, l model.Limit) (int, error) {
	n := 0
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		scales, err := metersqry.Scales(ctx, q)
		if err != nil {
			return err
		}
		units := l.Units * int64(math.Pow10(scales[model.Mails.Meter()]))
		if err := q.InsertPlanEntitlement(ctx, sqlc.InsertPlanEntitlementParams{PlanVersionID: versionID, MeterCode: model.Mails.Meter(), LimitUnits: units,
			AlertThresholdsPct: []int32{80, 100}, QuotaWindow: "month", Mode: string(l.Mode)}); err != nil {
			return err
		}
		now := c.cfg.Now()
		subs, err := q.ListUnendedSubscriptionsOnVersion(ctx, sqlc.ListUnendedSubscriptionsOnVersionParams{PlanVersionID: versionID, Now: now})
		if err != nil {
			return err
		}
		for _, s := range subs {
			var end *time.Time
			if !s.OpenEnded {
				e := s.EndTime
				end = &e
			}
			if err := entitlementscmd.Publish(ctx, q, tenantID, s.CustomerID, versionID, s.StartTime, end, now); err != nil {
				return err
			}
		}
		n = len(subs)
		return nil
	})
	return n, err
}
