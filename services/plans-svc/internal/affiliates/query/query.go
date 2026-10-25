// Package query reads the affiliate program: its rules, its affiliates and who they brought. It never writes.
package query

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads the affiliate program.
type Queries struct{ db *store.DB }

// New returns the affiliate queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// NotSet is the error for a tenant that has no program yet.
func NotSet() error {
	return errkey.New(codes.NotFound, "affiliate.program.not_set", "this environment has no affiliate program yet")
}

// NotFound is the error for an affiliate that does not exist (or is another tenant's).
func NotFound() error {
	return errkey.New(codes.NotFound, "affiliate.not_found", "no such affiliate")
}

// ProgramRules turns the stored program into the rules the model works with.
func ProgramRules(r sqlc.GetAffiliateProgramRow) model.Program {
	p := model.Program{
		Active: r.Status == model.ProgramActive, Kind: r.CommissionKind, Value: r.CommissionValue, Term: r.CommissionTerm,
		AttributionWindowDays: r.AttributionWindowDays, HoldDays: r.HoldDays,
	}
	if r.Currency != nil {
		p.Currency = *r.Currency
	}
	if r.CommissionTermValue != nil {
		p.TermValue = *r.CommissionTermValue
	}
	return p
}

// ProgramMessage maps the stored program to its API message.
func ProgramMessage(r sqlc.GetAffiliateProgramRow) *plansv1.AffiliateProgram {
	m := &plansv1.AffiliateProgram{
		Status: r.Status, CommissionKind: r.CommissionKind, CommissionValue: r.CommissionValue, CommissionTerm: r.CommissionTerm,
		AttributionWindowDays: r.AttributionWindowDays, HoldDays: r.HoldDays, MinPayoutMinor: r.MinPayoutMinor, DailyClickCap: r.DailyClickCap, AutoApprove: r.AutoApprove,
		CreateTime: timestamppb.New(r.CreatedAt), UpdateTime: timestamppb.New(r.UpdatedAt),
	}
	m.Currency = convert.Str(r.Currency)
	m.ReferredCouponCode = convert.Str(r.ReferredCouponCode)
	m.LandingUrl = convert.Str(r.LandingUrl)
	if r.CommissionTermValue != nil {
		m.CommissionTermValue = *r.CommissionTermValue
	}
	return m
}

// Program returns the tenant's program, or affiliate.program.not_set.
func (r *Queries) Program(ctx context.Context) (*plansv1.AffiliateProgram, error) {
	var out *plansv1.AffiliateProgram
	err := r.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetAffiliateProgram(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return NotSet()
		}
		if err != nil {
			return err
		}
		out = ProgramMessage(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliate program")
	}
	return out, nil
}

// Row is an affiliate as the queries read it.
type Row struct {
	ID            uuid.UUID
	Code          string
	Email         string
	Name          string
	UserID        *uuid.UUID
	Status        string
	PayoutMethod  string
	PayoutDetails string
	Source        string
	ApprovedAt    *time.Time
	CreatedAt     time.Time
	Clicks        int64
	Referrals     int64
}

// Message maps an affiliate to its API message.
func Message(r Row) *plansv1.Affiliate {
	a := &plansv1.Affiliate{
		Id: r.ID.String(), Code: r.Code, Email: r.Email, Name: r.Name, Status: r.Status, PayoutMethod: r.PayoutMethod, PayoutDetails: r.PayoutDetails, Source: r.Source,
		SignedIn: r.UserID != nil, Clicks: r.Clicks, Referrals: r.Referrals, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.ApprovedAt != nil {
		a.ApproveTime = timestamppb.New(*r.ApprovedAt)
	}
	return a
}

// RowOf reads the row of GetAffiliate.
func RowOf(r sqlc.GetAffiliateRow) Row {
	return Row{ID: r.ID, Code: r.Code, Email: r.Email, Name: r.Name, UserID: r.UserID, Status: r.Status, PayoutMethod: r.PayoutMethod, PayoutDetails: r.PayoutDetails,
		Source: r.Source, ApprovedAt: r.ApprovedAt, CreatedAt: r.CreatedAt, Clicks: r.Clicks, Referrals: r.Referrals}
}

// Get returns one affiliate with its click and referral counts.
func (r *Queries) Get(ctx context.Context, id string) (*plansv1.Affiliate, error) {
	aid, err := uuid.Parse(id)
	if err != nil {
		return nil, NotFound()
	}
	var out *plansv1.Affiliate
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetAffiliate(ctx, aid)
		if errors.Is(err, pgx.ErrNoRows) {
			return NotFound()
		}
		if err != nil {
			return err
		}
		out = Message(RowOf(row))
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	return out, nil
}

const defaultPage, maxPage = 50, 200

// Page turns a page size and token (an offset) into a limit and offset.
func Page(size int32, token string) (limit, offset int32, err error) {
	limit = size
	if limit <= 0 {
		limit = defaultPage
	}
	limit = min(limit, maxPage)
	if token != "" {
		n, perr := strconv.ParseInt(token, 10, 32)
		if perr != nil || n < 0 {
			return 0, 0, errkey.New(codes.InvalidArgument, "affiliate.bad_page_token", "page_token is not one this API gave")
		}
		offset = int32(n)
	}
	return limit, offset, nil
}

// NextToken is the token of the page after one that ended at offset+limit, or "" at the end.
func NextToken(offset, limit int32, total int64) string {
	if int64(offset)+int64(limit) >= total {
		return ""
	}
	return strconv.Itoa(int(offset + limit))
}

// NilIfEmpty is a pointer to s, or nil for "".
func NilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// List returns a page of affiliates, newest first, and the total.
func (r *Queries) List(ctx context.Context, req *plansv1.ListAffiliatesRequest) (*plansv1.ListAffiliatesResponse, error) {
	limit, offset, err := Page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	switch req.GetStatus() {
	case "", model.AffiliatePending, model.AffiliateActive, model.AffiliateSuspended:
	default:
		return nil, errkey.New(codes.InvalidArgument, "affiliate.bad_status", "status is pending, active, suspended or empty")
	}
	p := sqlc.ListAffiliatesParams{Status: NilIfEmpty(req.GetStatus()), Search: NilIfEmpty(req.GetSearch()), RowLimit: limit, RowOffset: offset}
	out := &plansv1.ListAffiliatesResponse{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListAffiliates(ctx, p)
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
			out.Affiliates = append(out.Affiliates, Message(Row{ID: row.ID, Code: row.Code, Email: row.Email, Name: row.Name, UserID: row.UserID, Status: row.Status,
				PayoutMethod: row.PayoutMethod, PayoutDetails: row.PayoutDetails, Source: row.Source, ApprovedAt: row.ApprovedAt, CreatedAt: row.CreatedAt,
				Clicks: row.Clicks, Referrals: row.Referrals}))
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	out.Total, out.NextPageToken = int32(total), NextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}

// Referrals lists the customers affiliates brought, newest first, with the e-mail address masked.
func (r *Queries) Referrals(ctx context.Context, req *plansv1.ListAffiliateReferralsRequest) (*plansv1.ListAffiliateReferralsResponse, error) {
	limit, offset, err := Page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	p := sqlc.ListAffiliateReferralsParams{RowLimit: limit, RowOffset: offset}
	if id := req.GetAffiliateId(); id != "" {
		aid, err := uuid.Parse(id)
		if err != nil {
			return nil, NotFound()
		}
		p.AffiliateID = &aid
	}
	out := &plansv1.ListAffiliateReferralsResponse{}
	var total int64
	err = r.db.Tx(ctx, func(q *sqlc.Queries) error {
		rows, err := q.ListAffiliateReferrals(ctx, p)
		if err != nil {
			return err
		}
		for _, row := range rows {
			total = row.Count
			out.Referrals = append(out.Referrals, &plansv1.AffiliateReferral{AffiliateId: row.AffiliateID.String(), AffiliateCode: row.AffiliateCode,
				CustomerExternalId: row.CustomerExternalID, CustomerEmailMasked: model.MaskEmail(row.CustomerEmail), Source: row.Source,
				AttributeTime: timestamppb.New(row.AttributedAt)})
		}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "referral")
	}
	out.Total, out.NextPageToken = int32(total), NextToken(offset, limit, total) //nolint:gosec // a page of at most 200 rows
	return out, nil
}
