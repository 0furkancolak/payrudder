package tenantevents

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Events is the part of plans-svc's WebhookService billing uses: plans-svc owns the tenant's endpoints and delivers the events.
type Events interface {
	RecordTenantEvent(ctx context.Context, in *plansv1.RecordTenantEventRequest, opts ...grpc.CallOption) (*plansv1.RecordTenantEventResponse, error)
}

// Flush hands the events written to tenant_event_outbox to plans-svc, oldest first, and removes each one once plans-svc has recorded it.
// An event that plans-svc cannot take for good (invalid argument) is dropped with a warning; any other failure leaves it for the next pass.
func (r *Relay) Flush(ctx context.Context) error {
	if r.events == nil {
		return nil
	}
	batch, err := r.q.ListTenantEvents(ctx)
	if err != nil {
		return err
	}
	for _, p := range batch {
		_, err := r.events.RecordTenantEvent(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: p.TenantID})), &plansv1.RecordTenantEventRequest{
			EventId: p.EventID.String(), Type: p.Type, DataJson: string(p.Data), OccurredTime: timestamppb.New(p.OccurredAt)})
		switch status.Code(err) {
		case codes.OK:
		case codes.InvalidArgument:
			r.log.WarnContext(ctx, "tenant event dropped: plans-svc refused it", "tenant", p.TenantID, "event", p.EventID, "type", p.Type, "error", err)
		default:
			return fmt.Errorf("record tenant event %s: %w", p.EventID, err)
		}
		if err := r.q.DeleteTenantEvent(ctx, sqlc.DeleteTenantEventParams{TenantID: p.TenantID, EventID: p.EventID}); err != nil {
			return err
		}
	}
	return nil
}

// Relay hands the events written to tenant_event_outbox to plans-svc.
type Relay struct {
	events Events
	q      *sqlc.Queries
	log    *slog.Logger
}

// NewRelay returns the relay over the billing_system pool. Without events (nil) nothing is handed over and the events stay in tenant_event_outbox.
func NewRelay(system *pgxpool.Pool, events Events, log *slog.Logger) *Relay {
	if log == nil {
		log = slog.Default()
	}
	return &Relay{events: events, q: sqlc.New(system), log: log}
}
