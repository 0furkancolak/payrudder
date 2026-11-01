package command

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/settlement"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/target"
)

// MaxCSV is the largest report an import takes.
const MaxCSV = 5 << 20

// SyncWindow is how far back one read of a provider's settlement may reach, and DefaultSync how far it reaches when the request names no time (default, not agreed:
// a payout is reported weeks after its payments, and the read is repeatable, so a window longer than the longest payout delay is enough).
const (
	SyncWindow  = 400 * 24 * time.Hour
	DefaultSync = 45 * 24 * time.Hour
)

// ImportResult is what an import or a read of a provider kept: how many lines it saw and how many were new or changed (reading the same report twice changes none).
type ImportResult struct {
	Lines   int   `json:"lines"`
	Changed int64 `json:"changed"`
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func badEnvironment(environment string) error {
	if _, err := env.Parse(environment); err != nil {
		return refuse.New(http.StatusBadRequest, "payments.request.bad_environment", "environment must be sandbox or live")
	}
	return nil
}

// ImportCSV keeps the lines of a settlement report (settlement.ParseCSV; docs/reconciliation.md). The whole file or nothing.
func ImportCSV(ctx context.Context, d Deps, tenant uuid.UUID, environment, provider string, body io.Reader) (*ImportResult, error) {
	if err := badEnvironment(environment); err != nil {
		return nil, err
	}
	lines, errs := settlement.ParseCSV(body, provider)
	if len(errs) > 0 {
		shown := make([]string, 0, 5)
		for _, e := range errs[:min(len(errs), 5)] {
			shown = append(shown, fmt.Sprintf("line %d: %s", e.Line, e.Message))
		}
		return nil, refuse.New(http.StatusBadRequest, "payments.settlement.bad_csv", "the report was not imported ("+fmt.Sprint(len(errs))+" problems): "+strings.Join(shown, "; "))
	}
	changed, err := d.Settlements.Put(ctx, tenant, environment, lines)
	if err != nil {
		return nil, err
	}
	return &ImportResult{Lines: len(lines), Changed: changed}, nil
}

// Sync reads what a provider says it settled since a time and keeps it. Only a provider whose adapter implements payments.SettlementReader can be read; the others are
// imported from their report.
func Sync(ctx context.Context, d Deps, tenant uuid.UUID, environment, provider string, since time.Time) (*ImportResult, error) {
	if err := badEnvironment(environment); err != nil {
		return nil, err
	}
	now := d.now()
	if since.IsZero() {
		since = now.Add(-DefaultSync)
	}
	if since.After(now) || now.Sub(since) > SyncWindow {
		return nil, refuse.New(http.StatusBadRequest, "payments.settlement.bad_since", "since must be in the past and at most 400 days ago")
	}
	if d.Resolver == nil {
		return nil, refuse.New(http.StatusServiceUnavailable, "payments.provider.failed", payments.ErrUnsupported.Error())
	}
	tg, err := target.Resolve(ctx, d.Resolver, tenant, environment, provider)
	if err != nil {
		return nil, err
	}
	reader, err := target.As[payments.SettlementReader](tg, "reading its settlement (import its report instead)")
	if err != nil {
		return nil, err
	}
	read, err := reader.Settlement(ctx, since)
	if err != nil {
		return nil, &refuse.Provider{What: "read the settlement of " + provider, Err: err}
	}
	lines := make([]store.SettlementLine, len(read))
	for i, l := range read {
		lines[i] = store.SettlementLine{Provider: provider, LineRef: l.Ref, Kind: l.Kind, PaymentRef: l.PaymentRef, Currency: l.Currency, GrossMinor: l.GrossMinor, FeeMinor: l.FeeMinor,
			NetMinor: l.NetMinor, OccurredAt: l.OccurredAt, AvailableOn: l.AvailableOn, SettledAt: l.SettledAt, Batch: l.Batch, Source: payments.BaseProvider(provider)}
	}
	changed, err := d.Settlements.Put(ctx, tenant, environment, lines)
	if err != nil {
		return nil, err
	}
	return &ImportResult{Lines: len(lines), Changed: changed}, nil
}

// PutFeeRule sets what a provider is expected to charge per payment in a currency: rate_bps hundredths of a percent of the gross, plus a fixed amount in minor units.
func PutFeeRule(ctx context.Context, d Deps, tenant uuid.UUID, r store.FeeRule) error {
	bad := func(msg string) error {
		return refuse.New(http.StatusBadRequest, "payments.settlement.bad_fee_rule", msg)
	}
	if err := badEnvironment(r.Environment); err != nil {
		return err
	}
	r.Currency = strings.ToUpper(r.Currency)
	if _, ok := money.LookupCurrency(r.Currency); !ok {
		return bad("currency is not a supported ISO 4217 code")
	}
	if !payments.ValidAccountKey(r.Provider) {
		return bad("provider must be a provider or an account key such as stripe~eu")
	}
	if r.RateBps < 0 || r.RateBps > 10000 || r.FixedMinor < 0 {
		return bad("rate_bps is 0 to 10000 (hundredths of a percent) and fixed_minor is not negative")
	}
	return d.Settlements.PutFeeRule(ctx, tenant, r)
}
