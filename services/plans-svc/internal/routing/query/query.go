// Package query reads the tenant's provider routing rules and previews where a customer would be routed.
package query

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Queries reads routing rules.
type Queries struct{ db *store.DB }

// New returns the routing queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Rules loads the tenant's routing rules, in order, in the caller's transaction.
func Rules(ctx context.Context, q *sqlc.Queries) ([]domain.Rule, error) {
	rows, err := q.ListRoutingRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Rule, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Rule{Country: convert.Str(r.Country), Currency: convert.Str(r.Currency), Provider: r.Provider})
	}
	return out, nil
}

// Messages maps routing rules to API messages.
func Messages(rules []domain.Rule) []*plansv1.RoutingRule {
	out := make([]*plansv1.RoutingRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, &plansv1.RoutingRule{Country: r.Country, Currency: r.Currency, Provider: r.Provider})
	}
	return out
}

func (r *Queries) load(ctx context.Context) ([]domain.Rule, error) {
	var rules []domain.Rule
	err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		rules, err = Rules(ctx, q)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "routing rules")
	}
	return rules, nil
}

// List returns the tenant's routing rules, in order.
func (r *Queries) List(ctx context.Context) ([]*plansv1.RoutingRule, error) {
	rules, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	return Messages(rules), nil
}

// Preview says which provider the rules route a customer of the country and currency to.
func (r *Queries) Preview(ctx context.Context, country, currency string) (*plansv1.PreviewRoutingResponse, error) {
	rules, err := r.load(ctx)
	if err != nil {
		return nil, err
	}
	provider, idx, ok := domain.Route(rules, country, currency)
	out := &plansv1.PreviewRoutingResponse{Provider: provider, RuleIndex: int32(idx), CanCharge: true} //nolint:gosec // bounded
	if ok {
		if c, known := providers.Lookup(provider); known {
			out.Owner = string(c.Owner())
			out.CanCharge = currency == "" || c.CanCharge(currency)
		}
	}
	return out, nil
}
