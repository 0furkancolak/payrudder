package tenantevents

import (
	"context"
	"fmt"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Events is the part of plans-svc's WebhookService billing uses: plans-svc owns the tenant's endpoints and delivers the events.
type Events interface {
	RecordTenantEvent(ctx context.Context, in *plansv1.RecordTenantEventRequest, opts ...grpc.CallOption) (*plansv1.RecordTenantEventResponse, error)
}

// Flush hands the events written to tenant_event_outbox to plans-svc, oldest first, and removes each one once plans-svc has recorded it.
// An event that plans-svc cannot take for good (invalid argument) is dropped with a warning; any other failure leaves it for the next pass.
//
// It looks in every place a tenant's rows can be (the pooled database, each tenant placed in a cell, ADR 0018): the events of a place are handed over oldest first,
// and an event is removed from the place it was found in. A tenant that is being moved is left alone until it serves again.
func (r *Relay) Flush(ctx context.Context) error {
	if r.events == nil {
		return nil
	}
	return r.db.Each(ctx, r.flush)
}

func (r *Relay) flush(ctx context.Context, s tenantdb.Scope) error {
	batch, err := s.Scan().ListTenantEvents(ctx)
	if err != nil {
		return err
	}
	for _, p := range batch {
		if err := s.Check(ctx, p.TenantID); err != nil {
			if !tenantdb.Skippable(err) {
				return err
			}
			continue
		}
		_, err := r.events.RecordTenantEvent(tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: p.TenantID})), &plansv1.RecordTenantEventRequest{
			EventId: p.EventID.String(), Type: p.Type, DataJson: string(p.Data), OccurredTime: timestamppb.New(p.OccurredAt)})
		switch status.Code(err) {
		case codes.OK:
		case codes.InvalidArgument:
			r.log.WarnContext(ctx, "tenant event dropped: plans-svc refused it", "tenant", p.TenantID, "event", p.EventID, "type", p.Type, "error", err)
		default:
			return fmt.Errorf("record tenant event %s: %w", p.EventID, err)
		}
		if err := s.Q(p.TenantID).DeleteTenantEvent(ctx, sqlc.DeleteTenantEventParams{TenantID: p.TenantID, EventID: p.EventID}); err != nil {
			return err
		}
	}
	return nil
}

// Relay hands the events written to tenant_event_outbox to plans-svc.
type Relay struct {
	events Events
	db     *tenantdb.DB
	log    *slog.Logger
}

// NewRelay returns the relay over the billing_system database. Without events (nil) nothing is handed over and the events stay in tenant_event_outbox.
func NewRelay(system *tenantdb.DB, events Events, log *slog.Logger) *Relay {
	if log == nil {
		log = slog.Default()
	}
	return &Relay{events: events, db: system, log: log}
}
