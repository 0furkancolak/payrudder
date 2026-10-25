package command

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Affiliates is the part of plans-svc's AffiliateService billing uses (phase 47): telling the program which partner a paying customer came through.
type Affiliates interface {
	AttributeCustomer(ctx context.Context, in *plansv1.AttributeCustomerRequest, opts ...grpc.CallOption) (*plansv1.AttributeCustomerResponse, error)
}

// BackendConfig is what the backend runs on.
type BackendConfig struct {
	Pool     *pgxpool.Pool // billing_system
	Inputs   *inputs.Inputs
	Invoices *invoicecmd.Commands
	Engine   *sagacommand.Engine
	// Affiliates is optional: without it a payer's referral cookie is not passed on.
	Affiliates Affiliates
	Now        func() time.Time
	Log        *slog.Logger
}

// Backend is the Directory of the commands over the real services: customers and sellers from plans-svc, the invoices of ad-hoc uses of a link, the promotion
// codes plans-svc redeems and the affiliate program. It also does the upkeep of links and promotions the scheduler asks for (ExpireLinks, ReversePromotions).
type Backend struct {
	cfg BackendConfig
	q   *sqlc.Queries
	in  *inputs.Inputs
}

// NewBackend returns the backend; a missing clock is the wall clock and a missing logger the default one.
func NewBackend(cfg BackendConfig) *Backend {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Backend{cfg: cfg, q: sqlc.New(cfg.Pool), in: cfg.Inputs}
}

// Customer is a customer of a tenant as plans-svc knows it; ok is false when there is none.
func (b *Backend) Customer(ctx context.Context, tenantID uuid.UUID, externalID string) (*plansv1.Customer, bool, error) {
	return b.in.Customer(ctx, tenantID, externalID)
}

// Seller is the tenant as the party that issues a link's invoices.
type Seller struct {
	Name        string
	LogoKey     string
	Environment string
}

// Seller looks the tenant up: its name, the object key of its logo and its environment.
func (b *Backend) Seller(ctx context.Context, tenantID uuid.UUID) (Seller, error) {
	t, err := b.in.Tenant(ctx, tenantID)
	if err != nil {
		return Seller{}, err
	}
	return Seller{Name: t.GetName(), LogoKey: inputs.LogoKey(tenantID, t), Environment: inputs.Environment(t)}, nil
}

// TenantCurrency is the tenant's default currency, which an ad-hoc link without one is in.
func (b *Backend) TenantCurrency(ctx context.Context, tenantID uuid.UUID) (string, error) {
	t, err := b.in.Tenant(ctx, tenantID)
	if err != nil {
		return "", err
	}
	return t.GetCurrency(), nil
}
