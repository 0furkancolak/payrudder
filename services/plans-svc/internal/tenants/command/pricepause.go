package command

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// PriceChangePause is the platform-wide kill switch of price changes (plan 15) as the API shows it.
func PriceChangePause(paused bool, reason, by string, at *time.Time) *plansv1.PriceChangePause {
	out := &plansv1.PriceChangePause{Paused: paused, Reason: reason, UpdatedBy: by}
	if at != nil {
		out.UpdateTime = timestamppb.New(*at)
	}
	return out
}

// GetPriceChangePause says whether price changes are paused, who paused them and why.
func (c *Commands) GetPriceChangePause(ctx context.Context) (*plansv1.PriceChangePause, error) {
	r, err := sqlc.New(c.pool).GetPriceChangePause(ctx)
	if err != nil {
		return nil, convert.Err(err, "price change pause")
	}
	return PriceChangePause(r.Paused, r.Reason, r.UpdatedBy, r.UpdatedAt), nil
}

// SetPriceChangePause pauses or resumes every price change of the platform and records who did it (audit_events, in the transaction of the change). Pausing twice, or
// resuming what is not paused, changes nothing but the note.
func (c *Commands) SetPriceChangePause(ctx context.Context, req *plansv1.SetPriceChangePauseRequest) (*plansv1.PriceChangePause, error) {
	reason := strings.TrimSpace(req.GetReason())
	if utf8.RuneCountInString(reason) > 500 {
		return nil, domain.Invalid("reason", "at most 500 characters")
	}
	actor := strings.TrimSpace(req.GetActor())
	if actor == "" {
		actor = "operator"
	}
	if r := []rune(actor); len(r) > maxActorRunes {
		actor = string(r[:maxActorRunes])
	}
	var out sqlc.SetPriceChangePauseRow
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) (err error) {
		if out, err = sqlc.New(tx).SetPriceChangePause(ctx, sqlc.SetPriceChangePauseParams{Paused: req.GetPaused(), Reason: reason, UpdatedBy: actor}); err != nil {
			return err
		}
		action, summary := "price_change.resumed", "resumed price changes"
		if req.GetPaused() {
			action, summary = "price_change.paused", "paused every price change"
		}
		return audit.Record(ctx, tx, audit.Event{Action: action, TargetType: "platform", TargetID: "price_changes", Summary: summary, Detail: map[string]any{"reason": reason}})
	})
	if err != nil {
		return nil, convert.Err(err, "price change pause")
	}
	return PriceChangePause(out.Paused, out.Reason, out.UpdatedBy, out.UpdatedAt), nil
}
