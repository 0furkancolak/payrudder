// Package command changes the tenant's provider routing rules.
package command

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	routingqry "github.com/0furkancolak/quotify/services/plans-svc/internal/routing/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
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
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
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
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "routing rules")
	}
	return routingqry.Messages(rules), nil
}
