package command

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

func (c *Commands) priceChangeNotice(ctx context.Context, db sqlc.DBTX) (*plansv1.PriceChangeNotice, error) {
	q := sqlc.New(db)
	s, err := q.GetPriceChangeNoticeSettings(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := q.ListPriceChangeNoticeRules(ctx)
	if err != nil {
		return nil, err
	}
	out := &plansv1.PriceChangeNotice{DefaultNoticeDays: s.DefaultNoticeDays, UpdatedBy: s.UpdatedBy, Rules: []*plansv1.PriceChangeNoticeRule{}}
	if s.UpdatedAt != nil {
		out.UpdateTime = timestamppb.New(*s.UpdatedAt)
	}
	for _, r := range rows {
		out.Rules = append(out.Rules, &plansv1.PriceChangeNoticeRule{Country: r.Country, MinNoticeDays: r.MinNoticeDays, Note: r.Note})
	}
	return out, nil
}

// GetPriceChangeNotice returns the notice rules of price changes.
func (c *Commands) GetPriceChangeNotice(ctx context.Context) (*plansv1.PriceChangeNotice, error) {
	out, err := c.priceChangeNotice(ctx, c.pool)
	return out, convert.Err(err, "price change notice")
}

// SetPriceChangeNotice replaces the notice rules (the default and the whole table) in one transaction and records who did it in audit_events. The rows are the
// operator's words: UNVERIFIED and not legal advice (plan 15).
func (c *Commands) SetPriceChangeNotice(ctx context.Context, req *plansv1.SetPriceChangeNoticeRequest) (*plansv1.PriceChangeNotice, error) {
	rules, notes := map[string]int{}, map[string]string{}
	var dup domain.Violations
	for _, r := range req.GetRules() {
		country := strings.ToUpper(strings.TrimSpace(r.GetCountry()))
		if _, seen := rules[country]; seen {
			dup.Add("rules", "each country once")
		}
		rules[country], notes[country] = int(r.GetMinNoticeDays()), strings.TrimSpace(r.GetNote())
	}
	if err := dup.Err(); err != nil {
		return nil, err
	}
	if err := domain.ValidateNoticeRules(int(req.GetDefaultNoticeDays()), rules, notes); err != nil {
		return nil, err
	}
	actor := strings.TrimSpace(req.GetActor())
	if actor == "" {
		actor = "operator"
	}
	if r := []rune(actor); len(r) > maxActorRunes {
		actor = string(r[:maxActorRunes])
	}
	var out *plansv1.PriceChangeNotice
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.SetPriceChangeNoticeSettings(ctx, sqlc.SetPriceChangeNoticeSettingsParams{DefaultNoticeDays: req.GetDefaultNoticeDays(), UpdatedBy: actor}); err != nil {
			return err
		}
		if err := q.DeletePriceChangeNoticeRules(ctx); err != nil {
			return err
		}
		for country, days := range rules {
			if err := q.InsertPriceChangeNoticeRule(ctx, sqlc.InsertPriceChangeNoticeRuleParams{Country: country, MinNoticeDays: int32(days), Note: notes[country]}); err != nil { //nolint:gosec // 14 to 365
				return err
			}
		}
		var err error
		if out, err = c.priceChangeNotice(ctx, tx); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "price_change.notice_rules_set", TargetType: "platform", TargetID: "price_change_notice", Summary: "set the notice rules of price changes",
			Detail: map[string]any{"default_notice_days": req.GetDefaultNoticeDays(), "rules": rules}})
	})
	return out, convert.Err(err, "price change notice")
}
