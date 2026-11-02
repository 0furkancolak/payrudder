// Package blocknotice turns CustomerBlockNotice facts (customer.block-notice.v1) into the customer's e-mail that the seller blocked them, or lifted the block (plan 13). The input
// is a fact and the e-mail is the consumer's own side effect, like the trial reminder. The mail belongs to the category customer_notices, which is off until the seller switches
// it on, so by default the consumer reads the fact and sends nothing. It never says why: the fact has neither the seller's category nor the note.
package blocknotice

import (
	"cmp"
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
	"github.com/0furkancolak/payrudder/pkg/localefmt"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/workerx"
	"github.com/0furkancolak/payrudder/services/notifier/internal/delivery"
	"github.com/0furkancolak/payrudder/services/notifier/internal/store"
)

// Topic is the topic of the facts plans-svc relays from its outbox.
const Topic = "customer.block-notice.v1"

// The templates of the two notices (category customer_notices, see pkg/mailcategory).
const (
	BlockedTemplate   = "customer_blocked.v1"
	UnblockedTemplate = "customer_unblocked.v1"
)

// Plans is the part of plans-svc the consumer uses.
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
}

// Admin resolves the tenant (name, environment, time zone and default language).
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

// Apply sends the email of each record, in order, then advances the offset. A record that can never be processed (it does not decode, or its customer is gone) goes to the
// DLQ; an infrastructure error returns, and the consumer retries the batch (already-sent emails are skipped by their dedup key).
func (c *Consumer) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	for _, rec := range recs {
		if err := c.one(ctx, rec); err != nil {
			return err
		}
	}
	return c.Offsets.Save(ctx, partition, recs[len(recs)-1].Offset+1)
}

func (c *Consumer) dlq(ctx context.Context, rec *kgo.Record, reason string, cause error) error {
	c.Log.WarnContext(ctx, "customer notice goes to the DLQ", "reason", reason, "error", cause)
	return c.DLQ.Send(ctx, rec, reason, cause)
}

// templateOf maps the kind of a notice to its template; any other kind can never be mailed.
func templateOf(kind string) (string, bool) {
	switch kind {
	case "blocked":
		return BlockedTemplate, true
	case "unblocked":
		return UnblockedTemplate, true
	}
	return "", false
}

func (c *Consumer) one(ctx context.Context, rec *kgo.Record) error {
	var ev eventsv1.CustomerBlockNotice
	if err := proto.Unmarshal(rec.Value, &ev); err != nil || ev.GetEventId() == "" {
		return c.dlq(ctx, rec, "DECODE_ERROR", err)
	}
	tid, err1 := uuid.Parse(ev.GetTenantId())
	cid, err2 := uuid.Parse(ev.GetCustomerId())
	template, known := templateOf(ev.GetKind())
	if err1 != nil || err2 != nil || !known {
		return c.dlq(ctx, rec, "INVALID_EVENT", errors.Join(err1, err2))
	}
	if ok, err := c.Mailer.Permitted(ctx, tid, template); err != nil {
		return err
	} else if !ok {
		return nil // the app has not switched the notices on (the default) or is on hold: dropped, counted, and the offset still advances
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
		c.Log.InfoContext(ctx, "customer notice dropped: the customer has no e-mail address", "tenant_id", tid, "customer_id", cid)
		return nil
	}
	tn, err := c.Admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	locale := cmp.Or(cust.GetCustomer().GetLocale(), tn.GetTenant().GetDefaultLanguage())
	d := MailData(&ev, cust.GetCustomer().GetName(), tn.GetTenant().GetName(), tn.GetTenant().GetTimezone(), locale)
	d["locale"] = locale
	_, err = c.Mailer.Deliver(ctx, delivery.Delivery{Key: ev.GetEventId(), Kind: "customer_notice", TenantID: tid, Template: template, To: cust.GetCustomer().GetEmail(), Data: d,
		Sandbox: tn.GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX})
	if err != nil && errors.Is(err, workerx.ErrPoison) {
		return c.dlq(ctx, rec, "INVALID_EVENT", err)
	}
	return err
}

// MailData is what the templates read. "until" is the end of the block in the tenant's time zone (UTC when it has none, or one this host does not know) with the zone named,
// in the language of the mail (pkg/localefmt); it is absent for a block with no end. "service_suspended" is "true" when the block also stops the service, else absent: a
// template tests for presence.
func MailData(ev *eventsv1.CustomerBlockNotice, customerName, tenantName, zone, locale string) map[string]string {
	d := map[string]string{"customer_name": customerName, "tenant_name": tenantName}
	if ev.GetKind() != "blocked" {
		return d
	}
	if ev.GetServiceSuspended() {
		d["service_suspended"] = "true"
	}
	if ev.GetBlockedUntil() != nil {
		loc := time.UTC
		if l, err := time.LoadLocation(zone); err == nil && zone != "" {
			loc = l
		}
		end := ev.GetBlockedUntil().AsTime().In(loc)
		d["until"] = localefmt.For(locale).Date(end) + ", " + end.Format("15:04") + " (" + loc.String() + ")"
	}
	return d
}
