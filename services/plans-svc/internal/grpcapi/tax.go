package grpcapi

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

func taxRateMessages(rows []sqlc.ListTaxRatesRow) []*plansv1.TaxRate {
	out := make([]*plansv1.TaxRate, 0, len(rows))
	for _, r := range rows {
		out = append(out, &plansv1.TaxRate{Country: r.Country, Name: r.Name, RateBps: r.RateBps})
	}
	return out
}

func (s *Server) GetTaxRates(ctx context.Context, _ *plansv1.GetTaxRatesRequest) (*plansv1.GetTaxRatesResponse, error) {
	resp := &plansv1.GetTaxRatesResponse{}
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListTaxRates(ctx)
		resp.Rates = taxRateMessages(rows)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "tax rate")
	}
	return resp, nil
}

// SetTaxRates replaces the tenant's tax rates. Rates are checked when saved: a country at most once, a name, a rate between 0 and 100 %.
func (s *Server) SetTaxRates(ctx context.Context, req *plansv1.SetTaxRatesRequest) (*plansv1.SetTaxRatesResponse, error) {
	var v domain.Violations
	if len(req.GetRates()) > 300 {
		v.Add("rates", "at most 300")
	}
	seen := map[string]bool{}
	for i, r := range req.GetRates() {
		field := fmt.Sprintf("rates[%d]", i)
		switch {
		case r.GetCountry() != "" && !countryCodeRe.MatchString(r.GetCountry()):
			v.Add(field+".country", "an ISO 3166-1 alpha-2 code such as DE, or empty for every other country")
		case seen[r.GetCountry()]:
			v.Add(field+".country", "a country at most once")
		case strings.TrimSpace(r.GetName()) == "" || len(r.GetName()) > 40:
			v.Add(field+".name", "1–40 characters, such as VAT")
		case r.GetRateBps() < 0 || r.GetRateBps() > 10000:
			v.Add(field+".rate_bps", "0..10000 (2000 is 20 %)")
		}
		seen[r.GetCountry()] = true
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	resp := &plansv1.SetTaxRatesResponse{}
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		if err := q.DeleteTaxRates(ctx); err != nil {
			return err
		}
		for _, r := range req.GetRates() {
			if err := q.InsertTaxRate(ctx, sqlc.InsertTaxRateParams{Country: r.GetCountry(), Name: strings.TrimSpace(r.GetName()), RateBps: r.GetRateBps()}); err != nil {
				return err
			}
		}
		rows, err := q.ListTaxRates(ctx)
		resp.Rates = taxRateMessages(rows)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "tax rate")
	}
	return resp, nil
}
