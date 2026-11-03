// Package command changes customers: creates them and sets their address and payment method.
package command

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/phone"
	"github.com/0furkancolak/payrudder/pkg/taxid"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/blocklist"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	entitlementscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/entitlements/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
	webhookscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/command"
	webhooksmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhooks/model"
)

// Referrer attributes a customer to an affiliate inside the caller's transaction (the affiliate program, phase 47).
type Referrer func(ctx context.Context, q *sqlc.Queries, in affiliatescmd.AttributeInput) (affiliatescmd.AttributeResult, error)

// BlocklistCheck refuses a customer who matches the app's or the platform's blocklist (internal/blocklist), in the caller's transaction.
type BlocklistCheck func(ctx context.Context, q *sqlc.Queries, s blocklist.Subject) error

// Commands changes customers.
type Commands struct {
	db        *store.DB
	referrer  Referrer
	blocklist BlocklistCheck
	now       func() time.Time
}

// New returns the customer commands.
func New(db *store.DB) *Commands { return &Commands{db: db, now: time.Now} }

// WithBlocklist makes a new customer, and a customer's new e-mail address or tax id, be checked against the blocklists (plan 13). Without it nothing is checked.
func (c *Commands) WithBlocklist(check BlocklistCheck) *Commands {
	c.blocklist = check
	return c
}

// checkBlocklist runs the gate when there is one.
func (c *Commands) checkBlocklist(ctx context.Context, q *sqlc.Queries, s blocklist.Subject) error {
	if c.blocklist == nil {
		return nil
	}
	return c.blocklist(ctx, q, s)
}

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
		if err := c.checkBlocklist(ctx, q, blocklist.Subject{Email: req.GetEmail(), ExternalID: req.GetExternalId()}); err != nil {
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
		number, _ := phone.Normalize(req.GetPhone()) // checked by ValidateCustomer
		out, err = q.CreateCustomer(ctx, sqlc.CreateCustomerParams{
			ID: idgen.NewUUIDv7(), ExternalID: req.GetExternalId(), Name: req.GetName(), Email: req.GetEmail(), PaymentMethod: pm,
			Country: country, BillingCurrency: billing, Address: convert.AddressJSON(req.GetAddress()), Locale: req.GetLocale(), CustomerType: req.GetCustomerType(), Phone: number,
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
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if out, err = q.SetCustomerAddress(ctx, params); err != nil {
			return err
		}
		// The address is printed on the customer's invoices: who changed it and that it changed is in the log, the address itself is not (it is personal data).
		return recordCustomer(ctx, tx, out, "customer.address_changed", "changed the address of the customer "+out.ExternalID, []string{"address"}, string(cur.Address), string(out.Address))
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetContact replaces the customer's name, e-mail address, language, kind and phone. An empty language or kind, and an unset phone, keep what the customer has.
func (c *Commands) SetContact(ctx context.Context, req *plansv1.SetCustomerContactRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateCustomerContact(req.GetName(), req.GetEmail(), req.GetLocale(), req.GetCustomerType(), req.GetPhone()); err != nil {
		return nil, err
	}
	var number *string
	if req.Phone != nil {
		n, _ := phone.Normalize(req.GetPhone()) // checked above
		number = &n
	}
	var out sqlc.Customer
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(cur.Email), strings.TrimSpace(req.GetEmail())) { // only an address that is new is checked: a listed customer can still change their name
			if err := c.checkBlocklist(ctx, q, blocklist.Subject{Email: req.GetEmail()}); err != nil {
				return err
			}
		}
		if out, err = q.SetCustomerContact(ctx, sqlc.SetCustomerContactParams{ExternalID: req.GetExternalId(), Name: req.GetName(), Email: req.GetEmail(), Locale: req.GetLocale(), CustomerType: req.GetCustomerType(), Phone: number}); err != nil {
			return err
		}
		var fields []string
		for _, f := range []struct {
			name      string
			was, will string
		}{{"name", cur.Name, out.Name}, {"email", cur.Email, out.Email}, {"phone", cur.Phone, out.Phone}, {"locale", cur.Locale, out.Locale}, {"customer_type", cur.CustomerType, out.CustomerType}} {
			if f.was != f.will {
				fields = append(fields, f.name)
			}
		}
		if len(fields) == 0 {
			return nil // nothing changed, nothing to say
		}
		// Names and addresses of people stay out of the log: the entry says which fields changed, and the hashes say that two entries saw the same state.
		return recordCustomer(ctx, tx, out, "customer.contact_changed", "changed the contact details of the customer "+out.ExternalID, fields,
			[]string{cur.Name, cur.Email, cur.Phone, cur.Locale, cur.CustomerType}, []string{out.Name, out.Email, out.Phone, out.Locale, out.CustomerType})
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
// customer whose block is in force keeps that block. A new block is recorded in the customer's history and announced as customer.blocked. With SuspendService it
// also publishes the customer's suspension record, in the same transaction, so that limits-svc answers "not allowed" on every meter until the block is lifted or ends.
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
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if convert.BlockOf(cur).InForce(now) {
			out = cur
			return nil
		}
		if out, err = q.BlockCustomer(ctx, sqlc.BlockCustomerParams{ExternalID: req.GetExternalId(), BlockedAt: &now, Reason: reason, Category: req.GetCategory(), BlockedUntil: until,
			SuspendService: req.GetSuspendService()}); err != nil {
			return err
		}
		if err := q.InsertCustomerBlockEvent(ctx, sqlc.InsertCustomerBlockEventParams{ID: idgen.NewUUIDv7(), CustomerID: out.ID, Action: "blocked", Category: req.GetCategory(),
			Reason: reason, BlockedUntil: until, SuspendService: req.GetSuspendService(), Actor: actorOf(req.GetActor()), CreatedAt: now}); err != nil {
			return err
		}
		switch {
		case req.GetSuspendService():
			err = entitlementscmd.Suspend(ctx, q, out.TenantID, out.ID, now, until)
		case cur.BlockSuspendService:
			// The block that ran out suspended the service; this one does not, and its record is still on the topic.
			err = entitlementscmd.Unsuspend(ctx, q, out.TenantID, out.ID)
		}
		if err != nil {
			return err
		}
		ti, _ := tenant.FromContext(ctx)
		if err := audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "customer.blocked", TargetType: "customer", TargetID: out.ID.String(),
			Summary: "blocked the customer " + out.ExternalID, Detail: map[string]any{"external_id": out.ExternalID, "category": req.GetCategory(), "reason": reason, "suspend_service": req.GetSuspendService()}}); err != nil {
			return err
		}
		data := map[string]any{"customer_id": out.ID.String(), "customer_external_id": out.ExternalID, "category": req.GetCategory(), "reason": reason, "suspend_service": req.GetSuspendService()}
		if until != nil {
			data["blocked_until"] = until.UTC().Format(time.RFC3339)
		}
		if err := emit(ctx, q, webhooksmodel.CustomerBlocked, data, now); err != nil {
			return err
		}
		return queueNotice(ctx, q, out.TenantID, out.ID, "blocked", req.GetSuspendService(), until, now)
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
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if cur.BlockedAt == nil {
			out = cur
			return nil
		}
		out, err = liftBlock(ctx, q, tx, cur, now, actorOf(req.GetActor()), "lifted the block on the customer ")
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// liftBlock clears the block the locked row cur carries and records it: the history row, the end of a suspension, the audit entry and customer.unblocked, in the
// caller's transaction. The sweep of blocks that ran out uses it too, with the actor "system".
func liftBlock(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, cur sqlc.Customer, now time.Time, actor, summary string) (sqlc.Customer, error) {
	out, err := q.UnblockCustomer(ctx, cur.ExternalID)
	if err != nil {
		return out, err
	}
	if err := q.CloseCustomerBlockAppeals(ctx, out.ID); err != nil {
		return out, err
	}
	if err := q.InsertCustomerBlockEvent(ctx, sqlc.InsertCustomerBlockEventParams{ID: idgen.NewUUIDv7(), CustomerID: out.ID, Action: "unblocked", Actor: actor, CreatedAt: now}); err != nil {
		return out, err
	}
	if cur.BlockSuspendService {
		if err := entitlementscmd.Unsuspend(ctx, q, out.TenantID, out.ID); err != nil {
			return out, err
		}
	}
	if err := audit.Record(ctx, tx, audit.Event{TenantID: out.TenantID, Action: "customer.unblocked", TargetType: "customer", TargetID: out.ID.String(),
		Summary: summary + out.ExternalID, Detail: map[string]any{"external_id": out.ExternalID}}); err != nil {
		return out, err
	}
	if err := emit(ctx, q, webhooksmodel.CustomerUnblocked, map[string]any{"customer_id": out.ID.String(), "customer_external_id": out.ExternalID}, now); err != nil {
		return out, err
	}
	return out, queueNotice(ctx, q, out.TenantID, out.ID, "unblocked", false, nil, now)
}

// recordCustomer writes the entry of a change of the customer's own data: the names of the fields that changed, and the hashes of the state before and after.
func recordCustomer(ctx context.Context, tx pgx.Tx, c sqlc.Customer, action, summary string, fields []string, before, after any) error {
	return audit.Record(ctx, tx, audit.Event{TenantID: c.TenantID, Action: action, TargetType: "customer", TargetID: c.ID.String(), Summary: summary,
		Before: before, After: after, Detail: map[string]any{"external_id": c.ExternalID, "fields": fields}})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// emit queues a customer event for the tenant's webhook endpoints, in the caller's transaction.
func emit(ctx context.Context, q *sqlc.Queries, typ string, data map[string]any, at time.Time) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = webhookscmd.Emit(ctx, q, idgen.NewUUIDv7(), typ, b, at)
	return err
}

// SetTax replaces the customer's tax id, tax office and e-fatura registration flag. An id that passes the local check of pkg/taxid is stamped as validated now; one
// that does not is stored all the same (default, not agreed: a wrong id is a warning on the customer, never a refusal).
func (c *Commands) SetTax(ctx context.Context, req *plansv1.SetCustomerTaxRequest) (*plansv1.Customer, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	if err := domain.ValidateCustomerTax(req); err != nil {
		return nil, err
	}
	var validated *time.Time
	if taxid.Recognised(req.GetTaxId()) {
		now := c.now()
		validated = &now
	}
	var out sqlc.Customer
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if taxid.Normalize(cur.TaxID) != taxid.Normalize(req.GetTaxId()) { // only an id that is new is checked
			if err := c.checkBlocklist(ctx, q, blocklist.Subject{TaxID: req.GetTaxId()}); err != nil {
				return err
			}
		}
		if out, err = q.SetCustomerTax(ctx, sqlc.SetCustomerTaxParams{ExternalID: req.GetExternalId(), TaxID: req.GetTaxId(), TaxOffice: req.GetTaxOffice(),
			EInvoiceRegistered: req.GetEInvoiceRegistered(), TaxIDValidatedAt: validated}); err != nil {
			return err
		}
		return recordCustomer(ctx, tx, out, "customer.tax_changed", "changed the tax data of the customer "+out.ExternalID, []string{"tax_id", "tax_office", "e_invoice_registered"},
			[]any{cur.TaxID, cur.TaxOffice, cur.EInvoiceRegistered}, []any{out.TaxID, out.TaxOffice, out.EInvoiceRegistered})
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

// SetPaymentMethod replaces the customer's payment method.
func (c *Commands) SetPaymentMethod(ctx context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.Customer, error) {
	var out sqlc.Customer
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
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
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if out, err = q.SetPaymentMethod(ctx, sqlc.SetPaymentMethodParams{ExternalID: req.GetExternalId(), PaymentMethod: &req.PaymentMethod}); err != nil {
			return err
		}
		// The token is what the customer is charged with: it is never in the log, only that it was replaced (and the hashes of the two).
		return recordCustomer(ctx, tx, out, "customer.payment_method_changed", "changed the payment method of the customer "+out.ExternalID, []string{"payment_method"},
			deref(cur.PaymentMethod), deref(out.PaymentMethod))
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}
