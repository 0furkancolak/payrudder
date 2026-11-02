package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store/sqlc"
)

// SettlementLine is a line of what a provider or a bank says it settled (settlement_lines).
type SettlementLine struct {
	Provider, LineRef, Kind, PaymentRef, Currency string
	GrossMinor, FeeMinor, NetMinor                int64
	OccurredAt                                    time.Time
	AvailableOn, SettledAt                        *time.Time
	Batch, Source                                 string
}

// Settlements is the kept settlement lines and the fee rules the expected fee comes from.
type Settlements struct{ DB DB }

// Put keeps lines in one transaction and reports how many were new or changed. The same line (provider and reference) read again updates what it knew less of and is
// never a second line.
func (s Settlements) Put(ctx context.Context, tenant uuid.UUID, environment string, lines []SettlementLine) (int64, error) {
	var changed int64
	err := tx(ctx, s.DB, tenant, func(_ pgx.Tx, q *sqlc.Queries) error {
		changed = 0
		for _, l := range lines {
			n, err := q.UpsertSettlementLine(ctx, sqlc.UpsertSettlementLineParams{TenantID: tenant, Environment: environment, Provider: l.Provider, LineRef: l.LineRef, Kind: l.Kind,
				PaymentRef: l.PaymentRef, Currency: l.Currency, GrossMinor: l.GrossMinor, FeeMinor: l.FeeMinor, NetMinor: l.NetMinor, OccurredAt: l.OccurredAt,
				AvailableOn: l.AvailableOn, SettledAt: l.SettledAt, Batch: l.Batch, Source: l.Source})
			if err != nil {
				return err
			}
			changed += n
		}
		return nil
	})
	return changed, err
}

// Since returns the tenant's lines that happened on or after since.
func (s Settlements) Since(ctx context.Context, tenant uuid.UUID, since time.Time) ([]SettlementLine, error) {
	rows, err := get(ctx, s.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListSettlementLinesRow, error) {
		return q.ListSettlementLines(ctx, sqlc.ListSettlementLinesParams{TenantID: tenant, Since: since})
	})
	if err != nil {
		return nil, err
	}
	out := make([]SettlementLine, len(rows))
	for i, r := range rows {
		out[i] = SettlementLine{Provider: r.Provider, LineRef: r.LineRef, Kind: r.Kind, PaymentRef: r.PaymentRef, Currency: r.Currency, GrossMinor: r.GrossMinor, FeeMinor: r.FeeMinor,
			NetMinor: r.NetMinor, OccurredAt: r.OccurredAt, AvailableOn: r.AvailableOn, SettledAt: r.SettledAt, Batch: r.Batch, Source: r.Source}
	}
	return out, nil
}

// FeeRule is what a provider is expected to charge per payment in a currency: rate_bps of the gross (hundredths of a percent) plus a fixed amount.
type FeeRule struct {
	Environment string    `json:"environment"`
	Provider    string    `json:"provider"`
	Currency    string    `json:"currency"`
	RateBps     int32     `json:"rate_bps"`
	FixedMinor  int64     `json:"fixed_minor"`
	UpdatedAt   time.Time `json:"updated_at,omitempty"`
}

// PutFeeRule sets the rule of a provider and currency.
func (s Settlements) PutFeeRule(ctx context.Context, tenant uuid.UUID, r FeeRule) error {
	return at(ctx, s.DB, tenant, func(q *sqlc.Queries) error {
		return q.UpsertFeeRule(ctx, sqlc.UpsertFeeRuleParams{TenantID: tenant, Environment: r.Environment, Provider: r.Provider, Currency: r.Currency, RateBps: r.RateBps, FixedMinor: r.FixedMinor})
	})
}

// DeleteFeeRule removes a rule and reports whether there was one.
func (s Settlements) DeleteFeeRule(ctx context.Context, tenant uuid.UUID, environment, provider, currency string) (bool, error) {
	n, err := get(ctx, s.DB, tenant, func(q *sqlc.Queries) (int64, error) {
		return q.DeleteFeeRule(ctx, sqlc.DeleteFeeRuleParams{TenantID: tenant, Environment: environment, Provider: provider, Currency: currency})
	})
	return n == 1, err
}

// FeeRules returns the tenant's rules.
func (s Settlements) FeeRules(ctx context.Context, tenant uuid.UUID) ([]FeeRule, error) {
	rows, err := get(ctx, s.DB, tenant, func(q *sqlc.Queries) ([]sqlc.ListFeeRulesRow, error) { return q.ListFeeRules(ctx, tenant) })
	if err != nil {
		return nil, err
	}
	out := make([]FeeRule, len(rows))
	for i, r := range rows {
		out[i] = FeeRule{Environment: r.Environment, Provider: r.Provider, Currency: r.Currency, RateBps: r.RateBps, FixedMinor: r.FixedMinor, UpdatedAt: r.UpdatedAt}
	}
	return out, nil
}
