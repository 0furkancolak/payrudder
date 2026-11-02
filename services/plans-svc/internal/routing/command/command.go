// Package command changes the tenant's provider routing rules.
package command

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/payments/providers"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	routingqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/routing/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes routing rules.
type Commands struct{ db *store.DB }

// New returns the routing commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// SetRules replaces the tenant's routing rules with the valid, ordered list given.
func (c *Commands) SetRules(ctx context.Context, in []*plansv1.RoutingRule) ([]*plansv1.RoutingRule, error) {
	rules := make([]domain.Rule, 0, len(in))
	for _, r := range in {
		rules = append(rules, domain.Rule{Country: r.GetCountry(), Currency: r.GetCurrency(), Provider: r.GetProvider()})
	}
	if err := domain.ValidateRules(rules, providers.Lookup); err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		if err := q.DeleteRoutingRules(ctx); err != nil {
			return err
		}
		for i, r := range rules {
			var country, currency *string
			if r.Country != "" {
				country = &r.Country
			}
			if r.Currency != "" {
				currency = &r.Currency
			}
			if err := q.InsertRoutingRule(ctx, sqlc.InsertRoutingRuleParams{Position: int32(i), Country: country, Currency: currency, Provider: r.Provider}); err != nil { //nolint:gosec // at most MaxRoutingRules
				return err
			}
		}
		shown := make([]map[string]string, len(rules))
		for i, r := range rules {
			shown[i] = map[string]string{"country": r.Country, "currency": r.Currency, "provider": r.Provider}
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "routing_rules.changed", TargetType: "routing_rules", After: rules,
			Summary: fmt.Sprintf("set %d routing rules", len(rules)), Detail: map[string]any{"count": len(rules), "rules": shown}})
	})
	if err != nil {
		return nil, convert.Err(err, "routing rules")
	}
	return routingqry.Messages(rules), nil
}
