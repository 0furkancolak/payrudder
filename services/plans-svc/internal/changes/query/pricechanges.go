package query

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
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
	out.EffectiveFrom = optTime(r.EffectiveFrom)
	if r.NoticeSendDaysBefore != nil {
		out.NoticeSendDaysBefore = *r.NoticeSendDaysBefore
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

// itemPageSize is the default and the most a page of subscriptions holds.
const itemPageSize = 500

// itemToken is the position after the last subscription of a page: the customer's external id and the subscription, as an opaque string.
func itemToken(customer string, sub uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(sub.String() + "|" + customer))
}

func parseItemToken(tok string) (customer string, sub uuid.UUID, err error) {
	b, err := base64.RawURLEncoding.DecodeString(tok)
	id, rest, ok := strings.Cut(string(b), "|")
	if err != nil || !ok || rest == "" {
		return "", uuid.Nil, domain.Invalid("page_token", "not a token this list gave")
	}
	if sub, err = uuid.Parse(id); err != nil {
		return "", uuid.Nil, domain.Invalid("page_token", "not a token this list gave")
	}
	return rest, sub, nil
}

// GetPriceChange reads one price change and a page of its subscriptions by customer, in one state or all (page_size 1 to 500, default 500).
func (q *Queries) GetPriceChange(ctx context.Context, rawID, state string, pageSize int32, pageToken string) (*plansv1.GetPriceChangeResponse, error) {
	id, err := domain.ParseID("id", rawID)
	if err != nil {
		return nil, PriceChangeNotFound()
	}
	if state != "" && state != "scheduled" && state != "applied" && state != "withdrawn" && state != "skipped" {
		return nil, domain.Invalid("state", "scheduled, applied, withdrawn or skipped")
	}
	size, ok := page.Size(pageSize, itemPageSize, itemPageSize)
	if !ok {
		return nil, domain.Invalid("page_size", "between 1 and 500")
	}
	params := sqlc.ListPriceChangeItemsParams{PriceChangeID: id, State: state, PageSize: size + 1} // one more, to know whether there is a next page
	if pageToken != "" {
		if params.AfterCustomer, params.AfterSubscription, err = parseItemToken(pageToken); err != nil {
			return nil, err
		}
	}
	out := &plansv1.GetPriceChangeResponse{Items: []*plansv1.PriceChangeItem{}}
	err = q.db.Tx(ctx, func(s *sqlc.Queries) error {
		pc, err := PriceChange(ctx, s, id)
		if err != nil {
			return err
		}
		out.PriceChange = pc
		rows, err := s.ListPriceChangeItems(ctx, params)
		if err != nil {
			return err
		}
		rows, more := page.Trim(rows, int(size))
		for _, r := range rows {
			out.Items = append(out.Items, &plansv1.PriceChangeItem{SubscriptionId: r.SubscriptionID.String(), CustomerExternalId: r.CustomerExternalID, State: r.State, Reason: r.Reason,
				EffectiveTime: optTime(r.EffectiveAt), NotifyTime: optTime(r.NotifiedAt)})
		}
		if more {
			last := rows[len(rows)-1]
			out.NextPageToken = itemToken(last.CustomerExternalID, last.SubscriptionID)
		}
		return nil
	})
	return out, convert.Err(err, "price change")
}

func optTime(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
