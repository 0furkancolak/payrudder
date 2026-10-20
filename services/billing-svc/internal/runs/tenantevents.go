package runs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
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
	type pending struct {
		tenant, id uuid.UUID
		typ        string
		data       []byte
		at         time.Time
	}
	rows, err := m.cfg.Pool.Query(ctx, `SELECT tenant_id, event_id, type, data, occurred_at FROM tenant_event_outbox ORDER BY created_at LIMIT 200`)
	if err != nil {
		return err
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.tenant, &p.id, &p.typ, &p.data, &p.at); err != nil {
			rows.Close()
			return err
		}
		batch = append(batch, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range batch {
		_, err := m.cfg.Events.RecordTenantEvent(tenantCtx(ctx, p.tenant), &plansv1.RecordTenantEventRequest{
			EventId: p.id.String(), Type: p.typ, DataJson: string(p.data), OccurredTime: timestamppb.New(p.at)})
		switch status.Code(err) {
		case codes.OK:
		case codes.InvalidArgument:
			m.log.WarnContext(ctx, "tenant event dropped: plans-svc refused it", "tenant", p.tenant, "event", p.id, "type", p.typ, "error", err)
		default:
			return fmt.Errorf("record tenant event %s: %w", p.id, err)
		}
		if _, err := m.cfg.Pool.Exec(ctx, `DELETE FROM tenant_event_outbox WHERE tenant_id = $1 AND event_id = $2`, p.tenant, p.id); err != nil {
			return err
		}
	}
	return nil
}
