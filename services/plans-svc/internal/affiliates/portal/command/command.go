// Package command changes a partner's own records (phase 47): linking the partner records a seller made for them, applying to a program, changing their payout details. It
// runs for a person on the system pool; each statement names the person or the tenant it works on.
package command

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	portalq "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/portal/query"
	affq "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes a partner's own records. A seller placed in a cell (ADR 0018) has its partner records in its own schema: what is about the person (their
// verified address, their membership of the seller's account) stays in the pooled database, what is about the seller's program and its records is done in the
// seller's schema, one transaction for each of the two. The order is the one that is safe to stop between: the membership first (it can be repeated and sees nothing
// of the account), then the record.
type Commands struct {
	pool   *pgxpool.Pool
	now    func() time.Time
	places *placement.Places // where the tenants' own rows are; nil: the pooled database
}

// WithPlaces tells the commands where the tenants' own rows are.
func (c *Commands) WithPlaces(p *placement.Places) *Commands {
	c.places = p
	return c
}

// New returns the portal commands on the system pool.
func New(systemPool *pgxpool.Pool, now func() time.Time) *Commands {
	return &Commands{pool: systemPool, now: now}
}

func notVerified() error {
	return errkey.New(codes.PermissionDenied, "identity.email.not_verified", "confirm your e-mail address first")
}

// verified returns the address of a person who has proven it. A partner record is tied to an address, so a person who has not proven theirs can neither claim the
// records made for it nor apply with it.
func verified(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) (string, error) {
	u, err := q.PortalUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", notVerified()
	}
	if err != nil {
		return "", err
	}
	if u.EmailVerifiedAt == nil {
		return "", notVerified()
	}
	return strings.TrimSpace(u.Email), nil
}

// join makes the person a member of the seller's account with the role affiliate (which sees none of it), so that the console knows to show them the partner portal.
func join(ctx context.Context, q *sqlc.Queries, tenantID, userID uuid.UUID) error {
	account, err := q.PortalTenantAccount(ctx, tenantID)
	if err != nil {
		return err
	}
	return q.PortalAddAffiliateMember(ctx, sqlc.PortalAddAffiliateMemberParams{AccountID: account, UserID: userID})
}

// Claim links to the person the partner records that sellers made for their verified address, and returns how many it linked. It can be repeated: it only takes records
// nobody has claimed.
func (c *Commands) Claim(ctx context.Context, userID string) (int32, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return 0, notVerified()
	}
	var claimed int32
	var email string
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var err error
		if email, err = verified(ctx, q, uid); err != nil {
			return err
		}
		rows, err := q.PortalClaimAffiliates(ctx, sqlc.PortalClaimAffiliatesParams{UserID: &uid, Email: email})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := join(ctx, q, r.TenantID, uid); err != nil {
				return err
			}
		}
		claimed = int32(len(rows)) //nolint:gosec // a handful of records
		return nil
	})
	if err != nil {
		return 0, convert.Err(err, "affiliation")
	}
	n, err := c.claimInCells(ctx, uid, email)
	if err != nil {
		return claimed + n, convert.Err(err, "affiliation")
	}
	return claimed + n, nil
}

// claimInCells is Claim for the sellers placed in cells: each one is asked for the records made for the address, the person becomes a member of the seller's account,
// and only then are the records linked to them (a stop in between leaves a membership, and the next Claim finds the records still unclaimed).
func (c *Commands) claimInCells(ctx context.Context, uid uuid.UUID, email string) (int32, error) {
	var claimed int32
	err := c.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		var found []sqlc.PortalFindUnclaimedRow
		if err := c.places.CellTx(ctx, t, func(tx pgx.Tx) (err error) {
			found, err = sqlc.New(tx).PortalFindUnclaimed(ctx, email)
			return err
		}); err != nil || len(found) == 0 {
			return err
		}
		if err := join(ctx, sqlc.New(c.pool), t.TenantID, uid); err != nil {
			return err
		}
		return c.places.CellTx(ctx, t, func(tx pgx.Tx) error {
			rows, err := sqlc.New(tx).PortalClaimAffiliates(ctx, sqlc.PortalClaimAffiliatesParams{UserID: &uid, Email: email})
			claimed += int32(len(rows)) //nolint:gosec // a handful of records
			return err
		})
	})
	return claimed, err
}

func alreadyApplied() error {
	return errkey.New(codes.AlreadyExists, "affiliate.portal.already_applied", "you are a partner of this seller already, or have applied")
}

// Apply applies to a seller's program with the person's verified address. The record waits for the seller's approval, or is active at once if the program says so.
func (c *Commands) Apply(ctx context.Context, req *plansv1.ApplyAsAffiliateRequest) (*plansv1.MyAffiliation, error) {
	uid, err := uuid.Parse(req.GetUserId())
	if err != nil {
		return nil, notVerified()
	}
	tid, err := uuid.Parse(req.GetTenantId())
	if err != nil {
		return nil, portalq.Unavailable()
	}
	name := strings.TrimSpace(req.GetName())
	method, details := req.GetPayoutMethod(), req.GetPayoutDetails()
	if method == "" {
		method = "manual"
	}
	var v domain.Violations
	if len(name) > 200 {
		v.Add("name", "at most 200 characters")
	}
	if method != "manual" && method != "bank_transfer" {
		v.Add("payout_method", "manual or bank_transfer")
	}
	if len(details) > 1000 {
		v.Add("payout_details", "at most 1000 characters")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	if in, err := c.places.InCell(ctx, tid); err != nil {
		return nil, convert.Err(err, "affiliation")
	} else if in {
		return c.applyInCell(ctx, uid, tid, name, method, details)
	}
	var out *plansv1.MyAffiliation
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		email, err := verified(ctx, q, uid)
		if err != nil {
			return err
		}
		prog, err := q.PortalProgram(ctx, sqlc.PortalProgramParams{UserID: &uid, TenantID: tid})
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !portalq.Open(prog)) {
			return portalq.Unavailable()
		}
		if err != nil {
			return err
		}
		if prog.Applied {
			return alreadyApplied()
		}
		status := model.AffiliatePending
		if prog.AutoApprove {
			status = model.AffiliateActive
		}
		var id uuid.UUID
		for range 8 {
			code, err := command.RandomCode()
			if err != nil {
				return err
			}
			id, err = q.PortalInsertAffiliate(ctx, sqlc.PortalInsertAffiliateParams{TenantID: tid, Code: code, Email: email, Name: name, UserID: &uid, Status: status,
				PayoutMethod: method, PayoutDetails: details})
			if errors.Is(err, pgx.ErrNoRows) {
				continue // the code is taken: another
			}
			if err != nil {
				return err
			}
			break
		}
		if id == uuid.Nil {
			return errkey.New(codes.Unavailable, "affiliate.portal.try_again", "no code could be made just now: try again")
		}
		if err := join(ctx, q, tid, uid); err != nil {
			return err
		}
		rows, err := q.PortalListAffiliations(ctx, sqlc.PortalListAffiliationsParams{UserID: &uid, AffiliateID: &id})
		if err != nil || len(rows) != 1 {
			return errors.Join(err, affq.NotFound())
		}
		out = portalq.Affiliation(rows[0])
		return nil
	})
	if err != nil {
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation && strings.Contains(pgxx.ConstraintName(err), "affiliates_email") {
			return nil, alreadyApplied()
		}
		return nil, convert.Err(err, "affiliation")
	}
	return out, nil
}

// Update changes the partner's name and payout details.
func (c *Commands) Update(ctx context.Context, req *plansv1.UpdateMyAffiliationRequest) (*plansv1.MyAffiliation, error) {
	uid, err1 := uuid.Parse(req.GetUserId())
	aid, err2 := uuid.Parse(req.GetAffiliateId())
	if err1 != nil || err2 != nil {
		return nil, affq.NotFound()
	}
	name := strings.TrimSpace(req.GetName())
	var v domain.Violations
	if len(name) > 200 {
		v.Add("name", "at most 200 characters")
	}
	if m := req.GetPayoutMethod(); m != "manual" && m != "bank_transfer" {
		v.Add("payout_method", "manual or bank_transfer")
	}
	if len(req.GetPayoutDetails()) > 1000 {
		v.Add("payout_details", "at most 1000 characters")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	var out *plansv1.MyAffiliation
	missed := false
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		n, err := q.PortalUpdateAffiliation(ctx, sqlc.PortalUpdateAffiliationParams{ID: aid, UserID: &uid, Name: name, PayoutMethod: req.GetPayoutMethod(), PayoutDetails: req.GetPayoutDetails()})
		if err != nil {
			return err
		}
		if n == 0 {
			missed = true
			return affq.NotFound()
		}
		rows, err := q.PortalListAffiliations(ctx, sqlc.PortalListAffiliationsParams{UserID: &uid, AffiliateID: &aid})
		if err != nil || len(rows) != 1 {
			return errors.Join(err, affq.NotFound())
		}
		out = portalq.Affiliation(rows[0])
		return nil
	})
	if missed && c.places.CellsOn() {
		// Not a seller in the pooled database: one in a cell may have it.
		out, err = c.updateInCells(ctx, uid, aid, req)
	}
	if err != nil {
		return nil, convert.Err(err, "affiliation")
	}
	return out, nil
}

// updateInCells changes the person's partner record in whichever seller's schema has it.
func (c *Commands) updateInCells(ctx context.Context, uid, aid uuid.UUID, req *plansv1.UpdateMyAffiliationRequest) (*plansv1.MyAffiliation, error) {
	found := uuid.Nil
	err := c.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		if found != uuid.Nil {
			return nil
		}
		return c.places.CellTx(ctx, t, func(tx pgx.Tx) error {
			n, err := sqlc.New(tx).PortalUpdateAffiliation(ctx, sqlc.PortalUpdateAffiliationParams{ID: aid, UserID: &uid, Name: strings.TrimSpace(req.GetName()),
				PayoutMethod: req.GetPayoutMethod(), PayoutDetails: req.GetPayoutDetails()})
			if err == nil && n > 0 {
				found = t.TenantID
			}
			return err
		})
	})
	if err != nil {
		return nil, err
	}
	if found == uuid.Nil {
		return nil, affq.NotFound()
	}
	rows, err := portalq.InTenant(ctx, c.places, c.pool, uid, found, &aid)
	if err != nil || len(rows) != 1 {
		return nil, errors.Join(err, affq.NotFound())
	}
	return portalq.Affiliation(rows[0]), nil
}

// applyInCell is Apply for a seller placed in a cell.
func (c *Commands) applyInCell(ctx context.Context, uid, tid uuid.UUID, name, method, details string) (*plansv1.MyAffiliation, error) {
	sys := sqlc.New(c.pool)
	email, err := verified(ctx, sys, uid)
	if err != nil {
		return nil, err
	}
	prog, err := portalq.ProgramOfTenant(ctx, c.places, c.pool, uid, tid)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !portalq.Open(prog)) {
		return nil, portalq.Unavailable()
	}
	if err != nil {
		return nil, convert.Err(err, "affiliation")
	}
	if prog.Applied {
		return nil, alreadyApplied()
	}
	status := model.AffiliatePending
	if prog.AutoApprove {
		status = model.AffiliateActive
	}
	if err := join(ctx, sys, tid, uid); err != nil {
		return nil, convert.Err(err, "affiliation")
	}
	var id uuid.UUID
	err = c.places.Read(ctx, tid, func(q *sqlc.Queries) error {
		for range 8 {
			code, err := command.RandomCode()
			if err != nil {
				return err
			}
			id, err = q.PortalInsertAffiliate(ctx, sqlc.PortalInsertAffiliateParams{TenantID: tid, Code: code, Email: email, Name: name, UserID: &uid, Status: status,
				PayoutMethod: method, PayoutDetails: details})
			if errors.Is(err, pgx.ErrNoRows) {
				continue // the code is taken: another
			}
			return err
		}
		return nil
	})
	if err != nil {
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation && strings.Contains(pgxx.ConstraintName(err), "affiliates_email") {
			return nil, alreadyApplied()
		}
		return nil, convert.Err(err, "affiliation")
	}
	if id == uuid.Nil {
		return nil, errkey.New(codes.Unavailable, "affiliate.portal.try_again", "no code could be made just now: try again")
	}
	rows, err := portalq.InTenant(ctx, c.places, c.pool, uid, tid, &id)
	if err != nil || len(rows) != 1 {
		return nil, convert.Err(errors.Join(err, affq.NotFound()), "affiliation")
	}
	return portalq.Affiliation(rows[0]), nil
}
