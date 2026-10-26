// Package limits turns LimitExceeded facts (usage.limit-exceeded.v1) into emails. Notifier reads Kafka directly here:
// the input is a fact and the email is the consumer's own side effect, so there is no saga to reply to (docs/03-services/workers.md#notifier).
package limits

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

// Topic is the limit alert topic.
const Topic = "usage.limit-exceeded.v1"

// limitTemplate is the template of the alert mail (category usage_alerts).
const limitTemplate = "limit_exceeded.v1"

// Plans is the part of plans-svc the consumer uses.
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
}

// Admin resolves the tenant (name and environment).
type Admin interface {
	GetTenant(ctx context.Context, in *plansv1.GetTenantRequest, opts ...grpc.CallOption) (*plansv1.GetTenantResponse, error)
}

// Consumer implements kafkax.PartitionHandler.
type Consumer struct {
	Mailer  *delivery.Mailer
	Plans   Plans
	Admin   Admin
	Offsets store.Offsets
	DLQ     kafkax.DLQ
	Log     *slog.Logger
}

// Apply sends the email of each record, in order, then advances the offset. A record that can never be processed (it does
// not decode, or its customer is gone) goes to the DLQ; an infrastructure error returns, and the consumer retries the
// batch (already-sent emails are skipped by their dedup key).
func (c *Consumer) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	for _, rec := range recs {
		if err := c.one(ctx, rec); err != nil {
			return err
		}
	}
	return c.Offsets.Save(ctx, partition, recs[len(recs)-1].Offset+1)
}

func (c *Consumer) dlq(ctx context.Context, rec *kgo.Record, reason string, cause error) error {
	c.Log.WarnContext(ctx, "limit alert goes to the DLQ", "reason", reason, "error", cause)
	return c.DLQ.Send(ctx, rec, reason, cause)
}

func (c *Consumer) one(ctx context.Context, rec *kgo.Record) error {
	var le eventsv1.LimitExceeded
	if err := proto.Unmarshal(rec.Value, &le); err != nil || le.GetEventId() == "" {
		return c.dlq(ctx, rec, "DECODE_ERROR", err)
	}
	tid, err1 := uuid.Parse(le.GetTenantId())
	cid, err2 := uuid.Parse(le.GetCustomerId())
	if err1 != nil || err2 != nil {
		return c.dlq(ctx, rec, "INVALID_EVENT", errors.Join(err1, err2))
	}
	if ok, err := c.Mailer.Permitted(ctx, tid, limitTemplate); err != nil {
		return err
	} else if !ok {
		return nil // the tenant switched usage alerts off: dropped, counted, and the offset still advances
	}
	octx := tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: tid}))
	cust, err := c.Plans.GetCustomer(octx, &plansv1.GetCustomerRequest{Id: cid.String()})
	if status.Code(err) == codes.NotFound {
		return c.dlq(ctx, rec, "UNKNOWN_CUSTOMER", err)
	}
	if err != nil {
		return fmt.Errorf("customer lookup: %w", err)
	}
	tn, err := c.Admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	scale := int(le.GetQuantityScale())
	// A limit over an hour, a day or a week names its window ("2026-11-05"); a monthly one its billing period, as before windows existed.
	window := le.GetWindowId()
	if window == "" {
		window = le.GetPeriodId()
	}
	data := map[string]string{
		"customer_name": cust.GetCustomer().GetName(), "tenant_name": tn.GetTenant().GetName(), "meter_code": le.GetMeterCode(),
		"period": window, "threshold_pct": fmt.Sprint(le.GetThresholdPct()),
		"used": quantity.FormatUnits(le.GetUsedUnits(), scale), "limit": quantity.FormatUnits(le.GetLimitUnits(), scale),
	}
	_, err = c.Mailer.Deliver(ctx, delivery.Delivery{
		Key: le.GetEventId(), Kind: "limit", Template: limitTemplate, To: cust.GetCustomer().GetEmail(), Data: data,
		Sandbox: tn.GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX,
	})
	if err != nil && errors.Is(err, workerx.ErrPoison) {
		return c.dlq(ctx, rec, "INVALID_EVENT", err)
	}
	return err
}
