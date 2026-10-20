package runs

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// Events is the part of plans-svc's WebhookService billing uses: plans-svc owns the tenant's endpoints and delivers the events.
type Events interface {
	RecordTenantEvent(ctx context.Context, in *plansv1.RecordTenantEventRequest, opts ...grpc.CallOption) (*plansv1.RecordTenantEventResponse, error)
}

// FlushTenantEvents hands the events written to tenant_event_outbox to plans-svc, oldest first, and removes each one once plans-svc has recorded it.
// An event that plans-svc cannot take for good (invalid argument) is dropped with a warning; any other failure leaves it for the next pass.
func (m *Manager) FlushTenantEvents(ctx context.Context) error {
	if m.cfg.Events == nil {
		return nil
	}
	batch, err := m.q.ListTenantEvents(ctx)
	if err != nil {
		return err
	}
	for _, p := range batch {
		_, err := m.cfg.Events.RecordTenantEvent(tenantCtx(ctx, p.TenantID), &plansv1.RecordTenantEventRequest{
			EventId: p.EventID.String(), Type: p.Type, DataJson: string(p.Data), OccurredTime: timestamppb.New(p.OccurredAt)})
		switch status.Code(err) {
		case codes.OK:
		case codes.InvalidArgument:
			m.log.WarnContext(ctx, "tenant event dropped: plans-svc refused it", "tenant", p.TenantID, "event", p.EventID, "type", p.Type, "error", err)
		default:
			return fmt.Errorf("record tenant event %s: %w", p.EventID, err)
		}
		if err := m.q.DeleteTenantEvent(ctx, sqlc.DeleteTenantEventParams{TenantID: p.TenantID, EventID: p.EventID}); err != nil {
			return err
		}
	}
	return nil
}
