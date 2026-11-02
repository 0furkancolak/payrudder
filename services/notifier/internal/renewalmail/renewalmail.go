// Package renewalmail turns RenewalNotice facts (subscription.renewal-notice.v1) into the customer's "your subscription renews soon" e-mail, in the customer's language:
// the price of the next period, the date, and the seller's own words on how to cancel (docs/plans/21-contracts.md). Like the other consumers, the input is a fact and the
// e-mail is its own side effect, so there is no saga to reply to.
package renewalmail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

// Topic is the topic of the facts plans-svc relays from its outbox.
const Topic = "subscription.renewal-notice.v1"

// Template is the template of the mail (category system: it is in no family of pkg/mailcategory, so the seller's mail switch does not hold it back; the seller switches the
// notice off in its contract settings).
const Template = "renewal_notice.v1"

// Plans is the part of plans-svc the consumer uses.
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
}

// Admin resolves the tenant (name, environment and time zone).
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

// Apply sends the email of each record, in order, then advances the offset. A record that can never be processed goes to the DLQ; an infrastructure error returns, and the
// consumer retries the batch (already-sent emails are skipped by their dedup key).
func (c *Consumer) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	for _, rec := range recs {
		if err := c.one(ctx, rec); err != nil {
			return err
		}
	}
	return c.Offsets.Save(ctx, partition, recs[len(recs)-1].Offset+1)
}

func (c *Consumer) dlq(ctx context.Context, rec *kgo.Record, reason string, cause error) error {
	c.Log.WarnContext(ctx, "renewal notice goes to the DLQ", "reason", reason, "error", cause)
	return c.DLQ.Send(ctx, rec, reason, cause)
}

func (c *Consumer) one(ctx context.Context, rec *kgo.Record) error {
	var ev eventsv1.RenewalNotice
	if err := proto.Unmarshal(rec.Value, &ev); err != nil || ev.GetEventId() == "" {
		return c.dlq(ctx, rec, "DECODE_ERROR", err)
	}
	tid, err1 := uuid.Parse(ev.GetTenantId())
	cid, err2 := uuid.Parse(ev.GetCustomerId())
	if err1 != nil || err2 != nil || ev.GetRenewsAt() == nil || ev.GetCurrency() == "" {
		return c.dlq(ctx, rec, "INVALID_EVENT", errors.Join(err1, err2))
	}
	octx := tenant.OutgoingContext(tenant.NewContext(ctx, tenant.Info{ID: tid}))
	cust, err := c.Plans.GetCustomer(octx, &plansv1.GetCustomerRequest{Id: cid.String()})
	if status.Code(err) == codes.NotFound {
		return c.dlq(ctx, rec, "UNKNOWN_CUSTOMER", err)
	}
	if err != nil {
		return fmt.Errorf("customer lookup: %w", err)
	}
	if cust.GetCustomer().GetEmail() == "" {
		c.Log.InfoContext(ctx, "renewal notice dropped: the customer has no e-mail address", "tenant_id", tid, "customer_id", cid)
		return nil
	}
	tn, err := c.Admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	d := MailData(&ev, cust.GetCustomer().GetName(), tn.GetTenant().GetName(), tn.GetTenant().GetTimezone())
	d["locale"] = cust.GetCustomer().GetLocale()
	_, err = c.Mailer.Deliver(ctx, delivery.Delivery{Key: ev.GetEventId(), Kind: "renewal", TenantID: tid, Template: Template, To: cust.GetCustomer().GetEmail(), Data: d,
		Sandbox: tn.GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX})
	if err != nil && errors.Is(err, workerx.ErrPoison) {
		return c.dlq(ctx, rec, "INVALID_EVENT", err)
	}
	return err
}

// MailData is what the template reads. The date is written in the tenant's time zone (UTC when it has none, or one this host does not know), the calendar its customers
// live by; the price is the fee of the next period in the subscription's currency, the way the seller's other notices write one.
func MailData(ev *eventsv1.RenewalNotice, customerName, tenantName, zone string) map[string]string {
	loc := time.UTC
	if l, err := time.LoadLocation(zone); err == nil && zone != "" {
		loc = l
	}
	return map[string]string{
		"customer_name": customerName, "tenant_name": tenantName, "plan_code": ev.GetPlanCode(), "interval": ev.GetInterval(), "cancel_text": ev.GetCancelText(),
		"renewal_date": ev.GetRenewsAt().AsTime().In(loc).Format("2 Jan 2006"), "price": money.FormatMinorGrouped(ev.GetPriceMinor(), int(ev.GetCurrencyExponent())) + " " + ev.GetCurrency(),
	}
}
