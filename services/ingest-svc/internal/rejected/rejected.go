// Package rejected serves the log of rejected events (docs/plans/18-quick-integration.md, work item 3): the list and the 24-hour summary. The log is written by
// internal/events/command; a read here never touches the hot path.
package rejected

import (
	"context"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/page"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// Handler reads the log for the caller's tenant (RLS enforced).
type Handler struct{ app pgxx.Beginner }

// New returns the handler; app is the ingest_app pool (or its router when cells are on).
func New(app pgxx.Beginner) *Handler { return &Handler{app: app} }

// ListRejectedEvents implements ingestv1.IngestServiceServer.
func (h *Handler) ListRejectedEvents(ctx context.Context, req *ingestv1.ListRejectedEventsRequest) (*ingestv1.ListRejectedEventsResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	size, ok := page.Size(req.GetPageSize(), 50, 200)
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "ingest.rejected_events.bad_page_size", "page_size: 1..200")
	}
	cur := page.Start()
	if tok := req.GetPageToken(); tok != "" {
		if cur, ok = page.ParseCursor(tok); !ok {
			return nil, errkey.New(codes.InvalidArgument, "ingest.rejected_events.bad_page_token", "page_token: not one this API gave")
		}
	}
	var rows []sqlc.ListRejectedEventsRow
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListRejectedEvents(ctx, sqlc.ListRejectedEventsParams{
			TenantID: ti.ID, MaxDays: domain.RejectedKeepDays, ErrorCode: req.GetErrorCode(), MeterCode: req.GetMeterCode(),
			AfterTime: cur.At, AfterID: cur.ID, PageSize: size + 1,
		})
		return err
	})
	if err != nil {
		return nil, unavailable(err)
	}
	rows, more := page.Trim(rows, int(size))
	resp := &ingestv1.ListRejectedEventsResponse{Events: make([]*ingestv1.RejectedEvent, len(rows))}
	for i, r := range rows {
		e := &ingestv1.RejectedEvent{
			Id: r.ID.String(), ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage, MeterCode: r.MeterCode,
			CustomerExternalId: r.CustomerExternalID, ReceivedTime: timestamppb.New(r.ReceivedAt), IdempotencyKey: r.IdempotencyKey,
		}
		if r.EventTime != nil {
			e.EventTime = timestamppb.New(*r.EventTime)
		}
		resp.Events[i] = e
	}
	if more {
		last := rows[len(rows)-1]
		resp.NextPageToken = page.Cursor{At: last.ReceivedAt, ID: last.ID}.Token()
	}
	return resp, nil
}

// GetRejectedEventSummary implements ingestv1.IngestServiceServer.
func (h *Handler) GetRejectedEventSummary(ctx context.Context, _ *ingestv1.GetRejectedEventSummaryRequest) (*ingestv1.GetRejectedEventSummaryResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	resp := &ingestv1.GetRejectedEventSummaryResponse{}
	err = pgxx.InTenantTx(ctx, h.app, ti.ID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		codesRows, err := q.RejectedEventCodeCounts(ctx, ti.ID)
		if err != nil {
			return err
		}
		for _, r := range codesRows {
			resp.Total += r.Count
			resp.ByCode = append(resp.ByCode, &ingestv1.GetRejectedEventSummaryResponse_CodeCount{ErrorCode: r.ErrorCode, Count: r.Count})
		}
		hours, err := q.RejectedEventHourCounts(ctx, ti.ID)
		if err != nil {
			return err
		}
		for _, r := range hours {
			resp.Hourly = append(resp.Hourly, &ingestv1.GetRejectedEventSummaryResponse_HourCount{Hour: timestamppb.New(r.Hour), Rejected: r.Rejected})
		}
		return nil
	})
	if err != nil {
		return nil, unavailable(err)
	}
	return resp, nil
}

// unavailable is the answer to a failed read: the log is a convenience, so every failure is a retryable 503.
func unavailable(error) error {
	return errkey.New(codes.Unavailable, "ingest.db.unavailable", "database unavailable, retry later")
}
