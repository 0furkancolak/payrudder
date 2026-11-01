// Package command changes customers: creates them and sets their address and payment method.
package command

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// Referrer attributes a customer to an affiliate inside the caller's transaction (the affiliate program, phase 47).
type Referrer func(ctx context.Context, q *sqlc.Queries, in affiliatescmd.AttributeInput) (affiliatescmd.AttributeResult, error)

// Commands changes customers.
type Commands struct {
	db       *store.DB
	referrer Referrer
	now      func() time.Time
}

// New returns the customer commands.
func New(db *store.DB) *Commands { return &Commands{db: db, now: time.Now} }

// WithReferral lets a customer be created with the code of the affiliate who brought them.
func (c *Commands) WithReferral(r Referrer, now func() time.Time) *Commands {
	c.referrer, c.now = r, now
	return c
}

// Create adds the customer the request describes, within the tier's customer quota.
func (c *Commands) Create(ctx context.Context, req *plansv1.CreateCustomerRequest) (*plansv1.Customer, error) {
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		if err := domain.ValidateCustomer(t.Environment, req); err != nil {
			return err
		}
		if quota := domain.QuotasFor(t.Tier).MaxCustomers; quota > 0 {
			n, err := q.CountCustomers(ctx)
			if err != nil {
				return err
			}
			if n >= quota {
				return errkey.Newf(codes.ResourceExhausted, "plans.customer.quota_reached", "customer quota of %d reached for tier %s", quota, t.Tier)
			}
		}
		var pm *string
		if req.GetPaymentMethod() != "" {
			pm = &req.PaymentMethod
		}
		var country, billing *string
		if req.GetCountry() != "" {
			country = &req.Country
		}
		if req.GetBillingCurrency() != "" {
			billing = &req.BillingCurrency
		}
		if c := req.GetAddress().GetCountry(); c != "" {
			country = &c
		}
		out, err = q.CreateCustomer(ctx, sqlc.CreateCustomerParams{
			ID: idgen.NewUUIDv7(), ExternalID: req.GetExternalId(), Name: req.GetName(), Email: req.GetEmail(), PaymentMethod: pm,
			Country: country, BillingCurrency: billing, Address: convert.AddressJSON(req.GetAddress()), Locale: req.GetLocale(), CustomerType: req.GetCustomerType(),
		})
		if err != nil {
			return err
		}
		if code := req.GetReferralCode(); code != "" && c.referrer != nil {
			// A code that cannot attribute (unknown, expired, the customer is the affiliate) is an answer, not a failure: the customer is created all the same.
			in := affiliatescmd.AttributeInput{CustomerID: out.ID, Code: code, Source: "api", Now: c.now()}
			if t := req.GetReferralTouchTime(); t != nil {
				in.TouchedAt = t.AsTime()
			}
			_, err = c.referrer(ctx, q, in)
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetAddress replaces the customer's address.
func (c *Commands) SetAddress(ctx context.Context, req *plansv1.SetCustomerAddressRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateAddress("address", req.GetAddress()); err != nil {
		return nil, err
	}
	params := sqlc.SetCustomerAddressParams{ExternalID: req.GetExternalId(), Address: convert.AddressJSON(req.GetAddress())}
	if c := req.GetAddress().GetCountry(); c != "" {
		params.Country = &c
	}
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = q.SetCustomerAddress(ctx, params)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetContact replaces the customer's name and e-mail address.
func (c *Commands) SetContact(ctx context.Context, req *plansv1.SetCustomerContactRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateCustomerContact(req.GetName(), req.GetEmail(), req.GetLocale(), req.GetCustomerType()); err != nil {
		return nil, err
	}
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = q.SetCustomerContact(ctx, sqlc.SetCustomerContactParams{ExternalID: req.GetExternalId(), Name: req.GetName(), Email: req.GetEmail(), Locale: req.GetLocale(), CustomerType: req.GetCustomerType()})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// BlockCategories are the reasons a block can name. Empty is allowed: none given.
var BlockCategories = []string{"fraud", "chargeback", "abuse", "request", "other"}

// maxActorRunes bounds the actor of a block or a lift, as the table does.
const maxActorRunes = 120

func actorOf(a string) string {
	a = strings.TrimSpace(a)
	if r := []rune(a); len(r) > maxActorRunes {
		return string(r[:maxActorRunes])
	}
	return a
}

// Block stops new subscriptions, plan changes and purchases for the customer (pkg/paymentgate says what is refused). The reason is the seller's note. Blocking a
// customer whose block is in force keeps that block. A new block is recorded in the customer's history and announced as customer.blocked.
func (c *Commands) Block(ctx context.Context, req *plansv1.BlockCustomerRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if utf8.RuneCountInString(req.GetReason()) > 500 {
		return nil, domain.Invalid("reason", "at most 500 characters")
	}
	if cat := req.GetCategory(); cat != "" && !slices.Contains(BlockCategories, cat) {
		return nil, domain.Invalid("category", "fraud, chargeback, abuse, request or other")
	}
	now := c.now()
	var until *time.Time
	if u := req.GetBlockedUntil(); u != nil {
		t := u.AsTime()
		if !t.After(now) {
			return nil, domain.Invalid("blocked_until", "must be in the future")
		}
		until = &t
	}
	reason := strings.TrimSpace(req.GetReason())
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if convert.BlockOf(cur).InForce(now) {
			out = cur
			return nil
		}
		if out, err = q.BlockCustomer(ctx, sqlc.BlockCustomerParams{ExternalID: req.GetExternalId(), BlockedAt: &now, Reason: reason, Category: req.GetCategory(), BlockedUntil: until}); err != nil {
			return err
		}
		if err := q.InsertCustomerBlockEvent(ctx, sqlc.InsertCustomerBlockEventParams{ID: idgen.NewUUIDv7(), CustomerID: out.ID, Action: "blocked", Category: req.GetCategory(),
			Reason: reason, BlockedUntil: until, Actor: actorOf(req.GetActor()), CreatedAt: now}); err != nil {
			return err
		}
		data := map[string]any{"customer_id": out.ID.String(), "customer_external_id": out.ExternalID, "category": req.GetCategory(), "reason": reason}
		if until != nil {
			data["blocked_until"] = until.UTC().Format(time.RFC3339)
		}
		return c.emit(ctx, q, webhooksmodel.CustomerBlocked, data, now)
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// Unblock lifts the block, also one that has ended by itself (it still sits in the row until somebody lifts it). A customer who was never blocked is returned as
// it is. A lift is recorded in the customer's history and announced as customer.unblocked.
func (c *Commands) Unblock(ctx context.Context, req *plansv1.UnblockCustomerRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	now := c.now()
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if cur.BlockedAt == nil {
			out = cur
			return nil
		}
		if out, err = q.UnblockCustomer(ctx, req.GetExternalId()); err != nil {
			return err
		}
		if err := q.InsertCustomerBlockEvent(ctx, sqlc.InsertCustomerBlockEventParams{ID: idgen.NewUUIDv7(), CustomerID: out.ID, Action: "unblocked", Actor: actorOf(req.GetActor()), CreatedAt: now}); err != nil {
			return err
		}
		return c.emit(ctx, q, webhooksmodel.CustomerUnblocked, map[string]any{"customer_id": out.ID.String(), "customer_external_id": out.ExternalID}, now)
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// emit queues a customer event for the tenant's webhook endpoints, in the caller's transaction.
func (c *Commands) emit(ctx context.Context, q *sqlc.Queries, typ string, data map[string]any, at time.Time) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = webhookscmd.Emit(ctx, q, idgen.NewUUIDv7(), typ, b, at)
	return err
}

// SetTax replaces the customer's tax id, tax office and e-fatura registration flag.
func (c *Commands) SetTax(ctx context.Context, req *plansv1.SetCustomerTaxRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateCustomerTax(req); err != nil {
		return nil, err
	}
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = q.SetCustomerTax(ctx, sqlc.SetCustomerTaxParams{ExternalID: req.GetExternalId(), TaxID: req.GetTaxId(), TaxOffice: req.GetTaxOffice(),
			EInvoiceRegistered: req.GetEInvoiceRegistered()})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetPaymentMethod replaces the customer's payment method.
func (c *Commands) SetPaymentMethod(ctx context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.Customer, error) {
	var out sqlc.Customer
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		t, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		if req.GetPaymentMethod() == "" {
			return domain.Invalid("payment_method", "required")
		}
		if err := domain.ValidatePaymentMethod(t.Environment, req.GetPaymentMethod()); err != nil {
			return domain.Invalid("payment_method", err.Error())
		}
		out, err = q.SetPaymentMethod(ctx, sqlc.SetPaymentMethodParams{ExternalID: req.GetExternalId(), PaymentMethod: &req.PaymentMethod})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}
