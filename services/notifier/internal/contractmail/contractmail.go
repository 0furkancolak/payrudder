// Package contractmail turns ContractAccepted facts (contract.accepted.v1) into the customer's copy of what they accepted, as an e-mail in the customer's language:
// the durable medium the distance contract rules ask for (docs/plans/21-contracts.md). Like the other consumers, the input is a fact and the e-mail is its own side
// effect, so there is no saga to reply to.
//
// The page is fetched from plans-svc, which writes it again from the stored record, and is mailed only when its sha256 is the one the acceptance holds: what the customer
// is sent is what was hashed.
package contractmail

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
const Topic = "contract.accepted.v1"

// Template is the template of the mail (category system: it is in no family of pkg/mailcategory, so the seller's mail switch does not hold it back).
const Template = "contract_accepted.v1"

// Plans is the part of plans-svc the consumer uses.
type Plans interface {
	GetCustomer(ctx context.Context, in *plansv1.GetCustomerRequest, opts ...grpc.CallOption) (*plansv1.GetCustomerResponse, error)
}

// Contracts is the part of ContractService the consumer uses.
type Contracts interface {
	GetContractSnapshot(ctx context.Context, in *plansv1.GetContractSnapshotRequest, opts ...grpc.CallOption) (*plansv1.GetContractSnapshotResponse, error)
}

// Admin resolves the tenant (name, environment and time zone).
type Admin interface {
	GetTenant(ctx context.Context, in *plansv1.GetTenantRequest, opts ...grpc.CallOption) (*plansv1.GetTenantResponse, error)
}

// Consumer implements kafkax.PartitionHandler.
type Consumer struct {
	Mailer    *delivery.Mailer
	Plans     Plans
	Contracts Contracts
	Admin     Admin
	Offsets   store.Offsets
	DLQ       kafkax.DLQ
	Log       *slog.Logger
}

// Apply sends the email of each record, in order, then advances the offset. A record that can never be processed goes to the DLQ; an infrastructure error returns, and
// the consumer retries the batch (already-sent emails are skipped by their dedup key).
func (c *Consumer) Apply(ctx context.Context, partition int32, recs []*kgo.Record) error {
	for _, rec := range recs {
		if err := c.one(ctx, rec); err != nil {
			return err
		}
	}
	return c.Offsets.Save(ctx, partition, recs[len(recs)-1].Offset+1)
}

func (c *Consumer) dlq(ctx context.Context, rec *kgo.Record, reason string, cause error) error {
	c.Log.WarnContext(ctx, "contract acceptance mail goes to the DLQ", "reason", reason, "error", cause)
	return c.DLQ.Send(ctx, rec, reason, cause)
}

func (c *Consumer) one(ctx context.Context, rec *kgo.Record) error {
	var ev eventsv1.ContractAccepted
	if err := proto.Unmarshal(rec.Value, &ev); err != nil || ev.GetEventId() == "" {
		return c.dlq(ctx, rec, "DECODE_ERROR", err)
	}
	tid, err1 := uuid.Parse(ev.GetTenantId())
	cid, err2 := uuid.Parse(ev.GetCustomerId())
	if err1 != nil || err2 != nil || ev.GetAcceptanceId() == "" || ev.GetAcceptedAt() == nil {
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
		c.Log.InfoContext(ctx, "contract acceptance mail dropped: the customer has no e-mail address", "tenant_id", tid, "customer_id", cid)
		return nil
	}
	snap, err := c.Contracts.GetContractSnapshot(octx, &plansv1.GetContractSnapshotRequest{AcceptanceId: ev.GetAcceptanceId()})
	if status.Code(err) == codes.NotFound {
		return c.dlq(ctx, rec, "UNKNOWN_ACCEPTANCE", err)
	}
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if snap.GetSha256() != ev.GetSnapshotSha256() {
		// The page the record writes now is not the page that was hashed when the customer accepted. Mailing it would send something else than what was accepted.
		return c.dlq(ctx, rec, "SNAPSHOT_MISMATCH", fmt.Errorf("fetched %s, the acceptance holds %s", snap.GetSha256(), ev.GetSnapshotSha256()))
	}
	tn, err := c.Admin.GetTenant(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetTenantRequest{TenantId: tid.String()})
	if err != nil {
		return fmt.Errorf("tenant lookup: %w", err)
	}
	_, err = c.Mailer.Deliver(ctx, delivery.Delivery{
		Key: ev.GetEventId(), Kind: "contract", TenantID: tid, Template: Template, To: cust.GetCustomer().GetEmail(),
		Data:    MailData(ev.GetAcceptedAt().AsTime(), cust.GetCustomer().GetName(), tn.GetTenant().GetName(), tn.GetTenant().GetTimezone(), cmp.Or(cust.GetCustomer().GetLocale(), tn.GetTenant().GetDefaultLanguage()), snap),
		Sandbox: tn.GetTenant().GetEnvironment() == plansv1.Environment_ENVIRONMENT_SANDBOX,
	})
	if err != nil && errors.Is(err, workerx.ErrPoison) {
		return c.dlq(ctx, rec, "INVALID_EVENT", err)
	}
	return err
}

// MailData is what the template reads. The time is written in the tenant's time zone (UTC when it has none, or one this host does not know); the page is the body of
// the snapshot, which the template prints as it is (it was written, and escaped, by plans-svc, and its hash was checked above).
func MailData(acceptedAt time.Time, customerName, tenantName, zone, locale string, snap *plansv1.GetContractSnapshotResponse) map[string]string {
	loc := time.UTC
	if l, err := time.LoadLocation(zone); err == nil && zone != "" {
		loc = l
	}
	return map[string]string{
		"customer_name": customerName, "tenant_name": tenantName, "locale": locale, "accepted_at": localefmt.For(locale).Date(acceptedAt.In(loc)) + ", " + acceptedAt.In(loc).Format("15:04 MST"),
		"snapshot_html": bodyOf(snap.GetHtml()), "snapshot_sha256": snap.GetSha256(),
	}
}

// bodyOf is what is between <body> and </body> of a snapshot page: a mail has a document of its own.
func bodyOf(page string) string {
	if i := strings.Index(page, "<body>"); i >= 0 {
		page = page[i+len("<body>"):]
	}
	if i := strings.LastIndex(page, "</body>"); i >= 0 {
		page = page[:i]
	}
	return strings.TrimSpace(page)
}
