package grpcapi

import (
	"context"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

func loadRules(ctx context.Context, q *sqlc.Queries) ([]domain.Rule, error) {
	rows, err := q.ListRoutingRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Rule, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Rule{Country: strString(r.Country), Currency: strString(r.Currency), Provider: r.Provider})
	}
	return out, nil
}

func rulesToProto(rules []domain.Rule) []*plansv1.RoutingRule {
	out := make([]*plansv1.RoutingRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, &plansv1.RoutingRule{Country: r.Country, Currency: r.Currency, Provider: r.Provider})
	}
	return out
}

func (s *Server) GetRoutingRules(ctx context.Context, _ *plansv1.GetRoutingRulesRequest) (*plansv1.GetRoutingRulesResponse, error) {
	var rules []domain.Rule
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		rules, err = loadRules(ctx, q)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "routing rules")
	}
	return &plansv1.GetRoutingRulesResponse{Rules: rulesToProto(rules)}, nil
}

func (s *Server) SetRoutingRules(ctx context.Context, req *plansv1.SetRoutingRulesRequest) (*plansv1.SetRoutingRulesResponse, error) {
	rules := make([]domain.Rule, 0, len(req.GetRules()))
	for _, r := range req.GetRules() {
		rules = append(rules, domain.Rule{Country: r.GetCountry(), Currency: r.GetCurrency(), Provider: r.GetProvider()})
	}
	if err := domain.ValidateRules(rules, providers.Lookup); err != nil {
		return nil, err
	}
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		if err := q.DeleteRoutingRules(ctx); err != nil {
			return err
		}
		for i, r := range rules {
			var country, currency *string
			if r.Country != "" {
				country = ptr(r.Country)
			}
			if r.Currency != "" {
				currency = ptr(r.Currency)
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
	return &plansv1.SetRoutingRulesResponse{Rules: rulesToProto(rules)}, nil
}

func (s *Server) PreviewRouting(ctx context.Context, req *plansv1.PreviewRoutingRequest) (*plansv1.PreviewRoutingResponse, error) {
	var rules []domain.Rule
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		rules, err = loadRules(ctx, q)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "routing rules")
	}
	provider, idx, ok := domain.Route(rules, req.GetCountry(), req.GetCurrency())
	out := &plansv1.PreviewRoutingResponse{Provider: provider, RuleIndex: int32(idx), CanCharge: true} //nolint:gosec // bounded
	if ok {
		if c, known := providers.Lookup(provider); known {
			out.Owner = string(c.Owner())
			out.CanCharge = req.GetCurrency() == "" || c.CanCharge(req.GetCurrency())
		}
	}
	return out, nil
}
