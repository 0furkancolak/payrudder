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
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes a partner's own records.
type Commands struct {
	pool *pgxpool.Pool
	now  func() time.Time
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
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		email, err := verified(ctx, q, uid)
		if err != nil {
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
	return claimed, nil
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
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		n, err := q.PortalUpdateAffiliation(ctx, sqlc.PortalUpdateAffiliationParams{ID: aid, UserID: &uid, Name: name, PayoutMethod: req.GetPayoutMethod(), PayoutDetails: req.GetPayoutDetails()})
		if err != nil {
			return err
		}
		if n == 0 {
			return affq.NotFound()
		}
		rows, err := q.PortalListAffiliations(ctx, sqlc.PortalListAffiliationsParams{UserID: &uid, AffiliateID: &aid})
		if err != nil || len(rows) != 1 {
			return errors.Join(err, affq.NotFound())
		}
		out = portalq.Affiliation(rows[0])
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliation")
	}
	return out, nil
}
