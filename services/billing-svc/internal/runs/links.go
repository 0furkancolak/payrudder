package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/money"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rating"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// LinkInvoiceSpec is one use of an ad-hoc payment link: the amount to be paid and who it is for.
type LinkInvoiceSpec struct {
	AmountMinor int64
	// Currency is the tenant's when empty.
	Currency    string
	Description string
	// CustomerExternalID makes the invoice out to this customer; empty makes it a guest invoice (the nil customer id).
	CustomerExternalID string
}

// Customer is a customer of a tenant as plans-svc knows it; ok is false when there is none.
func (m *Manager) Customer(ctx context.Context, tenantID uuid.UUID, externalID string) (*plansv1.Customer, bool, error) {
	resp, err := m.cfg.Plans.GetCustomer(tenantCtx(ctx, tenantID), &plansv1.GetCustomerRequest{ExternalId: externalID})
	if isNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if resp.GetCustomer() == nil {
		return nil, false, nil
	}
	return resp.GetCustomer(), true, nil
}

func isNotFound(err error) bool { return status.Code(err) == codes.NotFound }

// Seller is the tenant as the party that issues a link's invoices.
type Seller struct {
	Name        string
	LogoKey     string
	Environment string
}

// Seller looks the tenant up: its name, the object key of its logo and its environment.
func (m *Manager) Seller(ctx context.Context, tenantID uuid.UUID) (Seller, error) {
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return Seller{}, fmt.Errorf("get tenant: %w", err)
	}
	t := tn.GetTenant()
	return Seller{Name: t.GetName(), LogoKey: logoKeyOf(tenantID, t), Environment: environmentOf(t)}, nil
}

// TenantCurrency is the tenant's default currency, which an ad-hoc link without one is in.
func (m *Manager) TenantCurrency(ctx context.Context, tenantID uuid.UUID) (string, error) {
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return "", fmt.Errorf("get tenant: %w", err)
	}
	return tn.GetTenant().GetCurrency(), nil
}

// CreateLinkInvoice makes the invoice of one use of an ad-hoc link: a single line for the amount, numbered gap-free by the same finalization every invoice goes
// through, so its saga renders the PDF and then waits for the customer to pay (the invoice is collected through the link, not charged to a stored method).
// There is no tax on it: the amount is what the customer pays.
func (m *Manager) CreateLinkInvoice(ctx context.Context, tenantID uuid.UUID, spec LinkInvoiceSpec) (uuid.UUID, error) {
	tn, err := m.cfg.Admin.GetTenant(operatorCtx(ctx), &plansv1.GetTenantRequest{TenantId: tenantID.String()})
	if err != nil {
		return uuid.Nil, fmt.Errorf("get tenant: %w", err)
	}
	t := tn.GetTenant()
	loc, err := time.LoadLocation(t.GetTimezone())
	if err != nil {
		return uuid.Nil, err
	}
	currency := orDefault(spec.Currency, t.GetCurrency())
	cur, ok := money.LookupCurrency(currency)
	if !ok {
		return uuid.Nil, permanent{fmt.Errorf("currency %q is not supported", currency)}
	}
	snap := saga.Snapshot{
		TenantName: t.GetName(), Environment: environmentOf(t), CurrencyExponent: cur.Exponent, LogoObjectKey: logoKeyOf(tenantID, t), SellerAddress: addressLines(t.GetAddress()),
		Collection: saga.CollectionLink, Description: spec.Description,
	}
	customerID, country := uuid.Nil, ""
	if spec.CustomerExternalID != "" {
		cu, found, err := m.Customer(ctx, tenantID, spec.CustomerExternalID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("get customer: %w", err)
		}
		if !found {
			return uuid.Nil, permanent{fmt.Errorf("customer %q does not exist", spec.CustomerExternalID)}
		}
		if customerID, err = uuid.Parse(cu.GetId()); err != nil {
			return uuid.Nil, err
		}
		snap.CustomerExternalID, snap.CustomerName, snap.CustomerEmail, snap.CustomerAddress = cu.GetExternalId(), cu.GetName(), cu.GetEmail(), addressLines(cu.GetAddress())
		country = cu.GetCountry()
	}
	snap.Country = country
	snap.SkipMail = snap.CustomerEmail == ""
	body, err := json.Marshal(snap)
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.New()
	line := rating.Line{Kind: rating.KindBaseFee, AmountMinor: spec.AmountMinor, Description: spec.Description}
	err = pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := m.q.WithTx(tx)
		// plan_version_id has no meaning for a payment link: the nil id says so.
		if _, err := q.InsertDraftInvoice(ctx, sqlc.InsertDraftInvoiceParams{
			TenantID: tenantID, ID: id, CustomerID: customerID, PeriodID: period.PeriodOf(m.cfg.Now(), loc), Kind: "LINK", PlanVersionID: uuid.Nil, Currency: currency,
			SubtotalMinor: spec.AmountMinor, TotalMinor: spec.AmountMinor, Snapshot: body, Provider: "", Country: store.NilIfEmpty(country),
		}); err != nil {
			return err
		}
		return insertLines(ctx, q, tenantID, id, []rating.Line{line}, []int{0})
	})
	if err != nil {
		return uuid.Nil, err
	}
	if err := m.Finalize(ctx, tenantID, id); err != nil {
		return uuid.Nil, err
	}
	m.invoices.Add(ctx, 1)
	return id, nil
}
