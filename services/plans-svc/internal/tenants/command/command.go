// Package command changes a tenant on an operator's word: its settings (tier, grace period, invoice logo, seller identity, bank transfer) and its status.
// It runs on the system pool (role plans_system, BYPASSRLS) because an operator reaches any tenant.
package command

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	apikeyscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes tenants.
type Commands struct {
	pool *pgxpool.Pool
	keys *apikeyscmd.Commands
	log  *slog.Logger
}

// New returns the tenant commands on the system pool. keys drops a suspended tenant's keys from the gateway cache.
func New(systemPool *pgxpool.Pool, keys *apikeyscmd.Commands, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, keys: keys, log: log}
}

// UpdateSettings changes the settings the request names and leaves the others as they are.
func (c *Commands) UpdateSettings(ctx context.Context, req *plansv1.UpdateTenantSettingsRequest) (*plansv1.Tenant, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	params := sqlc.UpdateTenantSettingsParams{ID: id}
	for _, set := range []func(*plansv1.UpdateTenantSettingsRequest, *sqlc.UpdateTenantSettingsParams) error{
		setGrace, setTier, setLogo, setSeller, setEInvoice, setBankTransfer,
	} {
		if err := set(req, &params); err != nil {
			return nil, err
		}
	}
	t, err := sqlc.New(c.pool).UpdateTenantSettings(ctx, params)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return convert.Tenant(t), nil
}

func setGrace(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	if req.GetGracePeriod() == nil {
		p.GracePeriod = pgtype.Interval{}
		return nil
	}
	g := req.GetGracePeriod().AsDuration()
	if err := domain.ValidateGrace(g); err != nil {
		return domain.Invalid("grace_period", err.Error())
	}
	p.GracePeriod = convert.Interval(g)
	return nil
}

func setTier(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	if req.GetTier() == plansv1.TenantTier_TENANT_TIER_UNSPECIFIED {
		return nil
	}
	name, ok := domain.TierToDB(req.GetTier())
	if !ok {
		return domain.Invalid("tier", "unknown tier")
	}
	p.Tier = &name
	return nil
}

func setLogo(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	p.ClearLogo = req.GetClearInvoiceLogo()
	logo := req.GetInvoiceLogoAssetId()
	if logo == "" {
		return nil
	}
	if req.GetClearInvoiceLogo() {
		return domain.Invalid("clear_invoice_logo", "cannot be combined with invoice_logo_asset_id")
	}
	lid, err := domain.ParseID("invoice_logo_asset_id", logo)
	if err != nil {
		return err
	}
	p.InvoiceLogoAssetID = &lid
	return nil
}

func setSeller(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	if req.GetAddress() != nil {
		if err := domain.ValidateAddress("address", req.GetAddress()); err != nil {
			return err
		}
		p.Address = convert.AddressJSON(req.GetAddress())
	}
	if req.GetLegal() != nil {
		if err := domain.ValidateLegal(req.GetLegal()); err != nil {
			return err
		}
		p.Legal = convert.LegalJSON(req.GetLegal())
	}
	return nil
}

func setEInvoice(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	v := req.GetEInvoiceOn()
	if v == "" {
		return nil
	}
	if err := domain.ValidateEInvoiceOn(v); err != nil {
		return err
	}
	p.EInvoiceOn = &v
	return nil
}

func setBankTransfer(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	p.ClearBankTransfer = req.GetClearBankTransfer()
	d := req.GetBankTransfer()
	if d == nil {
		return nil
	}
	if req.GetClearBankTransfer() {
		return domain.Invalid("clear_bank_transfer", "cannot be combined with bank_transfer")
	}
	if err := domain.ValidateBankTransfer("bank_transfer", d); err != nil {
		return err
	}
	p.BankTransfer = convert.BankTransferJSON(d)
	return nil
}

// SetStatus is the operator's hand on a live tenant's life. Suspending evicts every key of the tenant from the gateway cache at once.
func (c *Commands) SetStatus(ctx context.Context, req *plansv1.SetTenantStatusRequest) (*plansv1.Tenant, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	want, ok := domain.StatusToDB(req.GetStatus())
	if !ok || want == domain.StatusInactive {
		return nil, errkey.New(codes.InvalidArgument, "plans.tenant.bad_status", "status: active or suspended")
	}
	var out sqlc.Tenant
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.LockTenantForGoLive(ctx, id)
		if err != nil {
			return err
		}
		switch {
		case cur.Environment != env.Live.String():
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "a sandbox tenant is always active")
		case cur.Status == want:
			out = cur
			return nil
		case want == domain.StatusSuspended && cur.Status != domain.StatusActive:
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "only an active tenant can be suspended")
		case want == domain.StatusActive && cur.Status == domain.StatusInactive && strings.TrimSpace(req.GetReason()) == "":
			return errkey.New(codes.InvalidArgument, "plans.tenant.reason_required", "activating a tenant without its checklist needs a reason")
		}
		out, err = q.SetTenantStatus(ctx, sqlc.SetTenantStatusParams{ID: id, Status: want})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	c.log.WarnContext(ctx, "tenant status set by an operator", "tenant_id", id, "status", want, "reason", req.GetReason())
	if want == domain.StatusSuspended {
		if err := c.keys.EvictTenant(ctx, id); err != nil {
			return nil, err
		}
	}
	return convert.Tenant(out), nil
}
