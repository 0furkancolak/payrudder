// Package pricechange turns PriceChangeScheduled facts (subscription.price-change.v1) into the customer's "your price is changing" e-mail, in the customer's
// language. Like the price change notices, the input is a fact and the e-mail is the consumer's own side effect, so there is no saga to reply to.
package pricechange

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/kafkax"
	"github.com/0furkancolak/payrudder/pkg/localefmt"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/mailevents"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
	"github.com/0furkancolak/payrudder/services/notifier/internal/templates"
)

// Topic is the topic of the facts plans-svc relays from its outbox.
const Topic = "subscription.price-change.v1"

// Template is the template of the mail (category system: it is in no family of pkg/mailcategory).
const Template = "price_change.v1"

// Plans is the part of plans-svc the consumer uses.
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
}

// Admin resolves the tenant (name, environment and time zone) and tells whether the operator has paused price changes.
type Admin interface {
	GetTenant(ctx context.Context, in *plansv1.GetTenantRequest, opts ...grpc.CallOption) (*plansv1.GetTenantResponse, error)
	GetPriceChangePause(ctx context.Context, in *plansv1.GetPriceChangePauseRequest, opts ...grpc.CallOption) (*plansv1.GetPriceChangePauseResponse, error)
}

// While the kill switch is on the consumer waits, looking again every pauseLook, for at most heldFor before it hands the batch back (the partition is read again a
// second later, so the wait is never longer than the group's session allows). An answer is kept for pauseLook, so a batch of a thousand notices asks once.
const (
	pauseLook = 3 * time.Second
	heldFor   = 15 * time.Second
)

// errHeld is returned when the switch is still on after heldFor: nothing was sent, the offset did not move, and the batch is read again.
var errHeld = errors.New("price change mail is held: the operator has paused price changes")

// Consumer implements kafkax.PartitionHandler.
type Consumer struct {
	Mailer  *delivery.Mailer
	Plans   Plans
	Admin   Admin
	Offsets store.Offsets
	DLQ     kafkax.DLQ
	Log     *slog.Logger
	// PauseLook and HeldFor override pauseLook and heldFor (tests).
	PauseLook, HeldFor time.Duration

	mu        sync.Mutex // partitions of one poll are applied concurrently
	pauseAt   time.Time
	pauseSeen bool
}

// Apply sends the email of each record, in order, then advances the offset. A record that can never be processed (it does not decode, or its customer is gone)
// goes to the DLQ; an infrastructure error returns, and the consumer retries the batch (already-sent emails are skipped by their dedup key).
func (c *Consumer) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	for _, rec := range recs {
		// The operator's kill switch (plan 15): no price change mail goes out while it is on. The notices wait where they are, and go when it is released.
		if err := c.holdWhilePaused(ctx); err != nil {
			return err
		}
		if err := c.one(ctx, rec); err != nil {
			return err
		}
	}
	return c.Offsets.Save(ctx, partition, recs[len(recs)-1].Offset+1)
}

// paused asks whether the operator has paused price changes, at most once per look.
func (c *Consumer) paused(ctx context.Context, look time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pauseAt.IsZero() && time.Since(c.pauseAt) < look {
		return c.pauseSeen, nil
	}
	r, err := c.Admin.GetPriceChangePause(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetPriceChangePauseRequest{})
	if err != nil {
		return false, fmt.Errorf("price change pause lookup: %w", err)
	}
	c.pauseAt, c.pauseSeen = time.Now(), r.GetPause().GetPaused()
	return c.pauseSeen, nil
}

// holdWhilePaused returns when price changes are not paused. A question it cannot get an answer to is an error like any other lookup: the mail does not go out on a
// guess that the switch is off.
func (c *Consumer) holdWhilePaused(ctx context.Context) error {
	look, held := cmp.Or(c.PauseLook, pauseLook), cmp.Or(c.HeldFor, heldFor)
	deadline := time.Now().Add(held)
	for {
		paused, err := c.paused(ctx, look)
		if err != nil || !paused {
			return err
		}
		if !time.Now().Add(look).Before(deadline) {
			return errHeld
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(look):
		}
	}
}

func (c *Consumer) dlq(ctx context.Context, rec *kgo.Record, reason string, cause error) error {
	c.Log.WarnContext(ctx, "price change notice goes to the DLQ", "reason", reason, "error", cause)
	return c.DLQ.Send(ctx, rec, reason, cause)
}

func (c *Consumer) one(ctx context.Context, rec *kgo.Record) error {
	var ev eventsv1.PriceChangeScheduled
	if err := proto.Unmarshal(rec.Value, &ev); err != nil || ev.GetEventId() == "" {
		return c.dlq(ctx, rec, "DECODE_ERROR", err)
	}
	tid, err1 := uuid.Parse(ev.GetTenantId())
	cid, err2 := uuid.Parse(ev.GetCustomerId())
	if err1 != nil || err2 != nil || ev.GetEffectiveAt() == nil {
		return c.dlq(ctx, rec, "INVALID_EVENT", errors.Join(err1, err2))
	}
	if ok, err := c.Mailer.Permitted(ctx, tid, Template); err != nil {
		return err
	} else if !ok {
		return nil // the tenant switched this kind of mail off: dropped, counted, and the offset still advances
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
		c.Log.InfoContext(ctx, "price change notice dropped: the customer has no e-mail address", "tenant_id", tid, "customer_id", cid)
		return nil
	}
	tn, err := c.Admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	locale := cmp.Or(cust.GetCustomer().GetLocale(), tn.GetTenant().GetDefaultLanguage())
	_, err = c.Mailer.Deliver(ctx, delivery.Delivery{
		Key: ev.GetEventId(), Kind: "price_change", TenantID: tid, Template: Template, To: cust.GetCustomer().GetEmail(),
		Data:    withLocale(mailData(&ev, cust.GetCustomer().GetName(), tn.GetTenant().GetName(), tn.GetTenant().GetTimezone(), locale), locale),
		Sandbox: tn.GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX,
	})
	if err != nil && errors.Is(err, workerx.ErrPoison) {
		return c.dlq(ctx, rec, "INVALID_EVENT", err)
	}
	return err
}

// withLocale adds the language of the customer, which the template is chosen by (empty is English).
func withLocale(d map[string]string, locale string) map[string]string {
	d["locale"] = locale
	return d
}

// mailData is what the template reads. The date is written in the tenant's time zone (UTC when it has none, or one this host does not know), which is the calendar
// its customers live by; the amount is the renewal fee in the subscription's currency. Both are written in the language of the mail (pkg/localefmt).
func mailData(ev *eventsv1.PriceChangeScheduled, customerName, tenantName, zone, locale string) map[string]string {
	loc := time.UTC
	if l, err := time.LoadLocation(zone); err == nil && zone != "" {
		loc = l
	}
	f := localefmt.For(locale)
	exp := 2
	if cur, ok := money.LookupCurrency(ev.GetCurrency()); ok {
		exp = cur.Exponent
	}
	return map[string]string{
		"customer_name": customerName, "tenant_name": tenantName, "plan_code": ev.GetPlanCode(),
		"effective_date": f.Date(ev.GetEffectiveAt().AsTime().In(loc)), "new_renewal": f.Money(ev.GetNewRenewalMinor(), exp, ev.GetCurrency()),
	}
}

// Preview renders the notice as Consume would write it for a customer, from the numbers the console has (the date in the tenant's time zone, the amount in the
// language of the mail). It sends nothing.
func Preview(in mailevents.PriceChangePreview) (mailevents.Rendered, error) {
	ev := &eventsv1.PriceChangeScheduled{PlanCode: in.PlanCode, EffectiveAt: timestamppb.New(in.EffectiveTime), NewRenewalMinor: in.NewRenewalMinor, Currency: in.Currency}
	r, err := templates.Render(Template, withLocale(mailData(ev, in.CustomerName, in.TenantName, in.Timezone, in.Locale), in.Locale), false)
	return mailevents.Rendered{Subject: r.Subject, HTML: r.HTML, Text: r.Text}, err
}
