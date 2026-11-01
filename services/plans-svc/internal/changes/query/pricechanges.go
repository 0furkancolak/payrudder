package query

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// PriceChangeNotFound is the answer for an id that is no price change of the tenant.
func PriceChangeNotFound() error {
	return errkey.New(codes.NotFound, "plans.price_change.not_found", "price change not found")
}

// PriceChangeOf is a price change row as the API returns it.
func PriceChangeOf(r sqlc.ListPriceChangesRow) *plansv1.PriceChange {
	out := &plansv1.PriceChange{Id: r.ID.String(), PlanCode: r.PlanCode, FromVersion: r.FromVersion, ToVersion: r.ToVersion, NoticeDays: r.NoticeDays,
		EffectiveTime: timestamppb.New(r.EffectiveAt), State: r.State, CreatedBy: r.CreatedBy, CreateTime: timestamppb.New(r.CreatedAt), WithdrawnBy: r.WithdrawnBy,
		Scheduled: r.Scheduled, Applied: r.Applied, Withdrawn: r.Withdrawn, Skipped: r.Skipped}
	if r.WithdrawnAt != nil {
		out.WithdrawTime = timestamppb.New(*r.WithdrawnAt)
	}
	return out
}

// PriceChange reads one price change with the number of subscriptions in each state.
func PriceChange(ctx context.Context, q *sqlc.Queries, id uuid.UUID) (*plansv1.PriceChange, error) {
	rows, err := q.ListPriceChanges(ctx, sqlc.ListPriceChangesParams{ID: &id})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, PriceChangeNotFound()
	}
	return PriceChangeOf(rows[0]), nil
}

// PriceChanges lists the price changes, newest first, of one plan or of all.
func (q *Queries) PriceChanges(ctx context.Context, planCode string) (*plansv1.ListPriceChangesResponse, error) {
	out := &plansv1.ListPriceChangesResponse{PriceChanges: []*plansv1.PriceChange{}}
	err := q.db.Tx(ctx, func(s *sqlc.Queries) error {
		rows, err := s.ListPriceChanges(ctx, sqlc.ListPriceChangesParams{PlanCode: planCode})
		for _, r := range rows {
			out.PriceChanges = append(out.PriceChanges, PriceChangeOf(r))
		}
		return err
	})
	return out, convert.Err(err, "price change")
}

// GetPriceChange reads one price change and its subscriptions, in one state or all (at most 500: shortcut, no paging until a price change has more).
func (q *Queries) GetPriceChange(ctx context.Context, rawID, state string) (*plansv1.GetPriceChangeResponse, error) {
	id, err := domain.ParseID("id", rawID)
	if err != nil {
		return nil, PriceChangeNotFound()
	}
	if state != "" && state != "scheduled" && state != "applied" && state != "withdrawn" && state != "skipped" {
		return nil, domain.Invalid("state", "scheduled, applied, withdrawn or skipped")
	}
	out := &plansv1.GetPriceChangeResponse{Items: []*plansv1.PriceChangeItem{}}
	err = q.db.Tx(ctx, func(s *sqlc.Queries) error {
		pc, err := PriceChange(ctx, s, id)
		if err != nil {
			return err
		}
		out.PriceChange = pc
		rows, err := s.ListPriceChangeItems(ctx, sqlc.ListPriceChangeItemsParams{PriceChangeID: id, State: state})
		for _, r := range rows {
			out.Items = append(out.Items, &plansv1.PriceChangeItem{SubscriptionId: r.SubscriptionID.String(), CustomerExternalId: r.CustomerExternalID, State: r.State, Reason: r.Reason,
				EffectiveTime: optTime(r.EffectiveAt), NotifyTime: optTime(r.NotifiedAt)})
		}
		return err
	})
	return out, convert.Err(err, "price change")
}

func optTime(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
