package query

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// A partner is a person: the portal finds their records through the person, across sellers. A seller in the pooled database is one statement over all of them; a seller
// placed in a cell (ADR 0018) has its affiliates and program in its own schema, and the seller's environment and name in the pooled tenants and apps tables. So with
// cells on the portal asks the pooled database as ever, then each tenant in a cell for the person's records in it, and puts the two sides together. The cost is one
// small statement per tenant in a cell for each call of the portal.

// WithPlaces tells the queries where the tenants' own rows are.
func (p *Queries) WithPlaces(pl *placement.Places) *Queries {
	p.places = pl
	return p
}

// Merge puts the pooled side of an affiliation (the seller's environment and name) to the tenant's side.
func Merge(a sqlc.PortalListAffiliationsInTenantRow, info sqlc.PortalTenantInfoRow) sqlc.PortalListAffiliationsRow {
	return sqlc.PortalListAffiliationsRow{
		ID: a.ID, TenantID: a.TenantID, Environment: info.Environment, SellerName: info.SellerName, Code: a.Code, Status: a.Status, Name: a.Name, Email: a.Email,
		PayoutMethod: a.PayoutMethod, PayoutDetails: a.PayoutDetails, CreatedAt: a.CreatedAt, ProgramStatus: a.ProgramStatus, CommissionKind: a.CommissionKind,
		CommissionValue: a.CommissionValue, ProgramCurrency: a.ProgramCurrency, CommissionTerm: a.CommissionTerm, CommissionTermValue: a.CommissionTermValue,
		AttributionWindowDays: a.AttributionWindowDays, HoldDays: a.HoldDays, MinPayoutMinor: a.MinPayoutMinor,
	}
}

// InTenant reads one tenant's affiliations of the person (all of them, or the one with affiliateID) where the tenant's rows are, with the seller's pooled side.
func InTenant(ctx context.Context, places *placement.Places, sys sqlc.DBTX, uid, tenantID uuid.UUID, affiliateID *uuid.UUID) ([]sqlc.PortalListAffiliationsRow, error) {
	var rows []sqlc.PortalListAffiliationsInTenantRow
	err := places.Read(ctx, tenantID, func(q *sqlc.Queries) (err error) {
		rows, err = q.PortalListAffiliationsInTenant(ctx, sqlc.PortalListAffiliationsInTenantParams{UserID: &uid, AffiliateID: affiliateID})
		return err
	})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	info, err := sqlc.New(sys).PortalTenantInfo(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.PortalListAffiliationsRow, len(rows))
	for i, r := range rows {
		out[i] = Merge(r, info)
	}
	return out, nil
}

// listAffiliations is PortalListAffiliations for the person across the pooled database and every tenant in a cell, in the order that statement gives: live ones
// first, then the newest.
func (p *Queries) listAffiliations(ctx context.Context, uid uuid.UUID, affiliateID *uuid.UUID) ([]sqlc.PortalListAffiliationsRow, error) {
	rows, err := p.sys.PortalListAffiliations(ctx, sqlc.PortalListAffiliationsParams{UserID: &uid, AffiliateID: affiliateID})
	if err != nil || !p.places.CellsOn() {
		return rows, err
	}
	err = p.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		more, err := InTenant(ctx, p.places, p.pool, uid, t.TenantID, affiliateID)
		rows = append(rows, more...)
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(rows, func(a, b sqlc.PortalListAffiliationsRow) int {
		if c := strings.Compare(b.Environment, a.Environment); c != 0 { // environment DESC
			return c
		}
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 { // created_at DESC
			return c
		}
		return slices.Compare(a.ID[:], b.ID[:])
	})
	return rows, nil
}

// ProgramOfTenant is PortalProgram for a seller wherever its rows are: the program and whether the person applied from the tenant's side, the seller's environment,
// status and name from the pooled side. pgx.ErrNoRows when the seller has no program.
func ProgramOfTenant(ctx context.Context, places *placement.Places, sys sqlc.DBTX, uid, tenantID uuid.UUID) (sqlc.PortalProgramRow, error) {
	in, err := places.InCell(ctx, tenantID)
	if err != nil {
		return sqlc.PortalProgramRow{}, err
	}
	sq := sqlc.New(sys)
	if !in {
		return sq.PortalProgram(ctx, sqlc.PortalProgramParams{UserID: &uid, TenantID: tenantID})
	}
	var prog sqlc.PortalProgramInTenantRow
	if err := places.Read(ctx, tenantID, func(q *sqlc.Queries) (err error) {
		prog, err = q.PortalProgramInTenant(ctx, sqlc.PortalProgramInTenantParams{UserID: &uid, TenantID: tenantID})
		return err
	}); err != nil {
		return sqlc.PortalProgramRow{}, err
	}
	info, err := sq.PortalTenantInfo(ctx, tenantID)
	if err != nil {
		return sqlc.PortalProgramRow{}, err
	}
	return sqlc.PortalProgramRow{
		TenantID: tenantID, Environment: info.Environment, TenantStatus: info.TenantStatus, SellerName: info.SellerName, Status: prog.Status,
		CommissionKind: prog.CommissionKind, CommissionValue: prog.CommissionValue, Currency: prog.Currency, CommissionTerm: prog.CommissionTerm,
		CommissionTermValue: prog.CommissionTermValue, AttributionWindowDays: prog.AttributionWindowDays, HoldDays: prog.HoldDays, MinPayoutMinor: prog.MinPayoutMinor,
		AutoApprove: prog.AutoApprove, Applied: prog.Applied,
	}, nil
}
