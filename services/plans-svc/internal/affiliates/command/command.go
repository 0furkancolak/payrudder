// Package command changes the affiliate program: its rules, its affiliates, who they brought, what they earn and what is paid out to them.
package command

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes the affiliate program.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the affiliate commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }

var (
	payoutMethods = []string{"manual", "bank_transfer"}
	maxPayoutText = 1000
)

// SetProgram creates or replaces the tenant's program after checking its rules.
func (c *Commands) SetProgram(ctx context.Context, in *plansv1.AffiliateProgram) (*plansv1.AffiliateProgram, error) {
	p, couponCode, err := parseProgram(in)
	if err != nil {
		return nil, err
	}
	var out *plansv1.AffiliateProgram
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		if couponCode != "" {
			cp, err := q.GetCoupon(ctx, couponCode)
			if errors.Is(err, pgx.ErrNoRows) {
				return errkey.New(codes.NotFound, "affiliate.program.coupon_not_found", "the coupon for referred customers does not exist")
			}
			if err != nil {
				return err
			}
			if !cp.Active {
				return errkey.New(codes.FailedPrecondition, "affiliate.program.coupon_inactive", "the coupon for referred customers is not active")
			}
			p.ReferredCouponID = &cp.ID
		}
		if err := q.UpsertAffiliateProgram(ctx, p); err != nil {
			return err
		}
		row, err := q.GetAffiliateProgram(ctx)
		if err != nil {
			return err
		}
		out = query.ProgramMessage(row)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliate program")
	}
	return out, nil
}

func parseProgram(in *plansv1.AffiliateProgram) (sqlc.UpsertAffiliateProgramParams, string, error) {
	var v domain.Violations
	p := sqlc.UpsertAffiliateProgramParams{
		Status: in.GetStatus(), CommissionKind: in.GetCommissionKind(), CommissionValue: in.GetCommissionValue(), CommissionTerm: in.GetCommissionTerm(),
		AttributionWindowDays: in.GetAttributionWindowDays(), HoldDays: in.GetHoldDays(), MinPayoutMinor: in.GetMinPayoutMinor(), DailyClickCap: in.GetDailyClickCap(),
		AutoApprove: in.GetAutoApprove(),
	}
	if p.Status == "" {
		p.Status = model.ProgramDraft
	}
	switch p.Status {
	case model.ProgramDraft, model.ProgramActive, model.ProgramPaused:
	default:
		v.Add("status", "draft, active or paused")
	}
	switch p.CommissionKind {
	case model.KindPercent:
		if p.CommissionValue < 1 || p.CommissionValue > 10000 {
			v.Add("commission_value", "basis points between 1 and 10000 (1000 is 10 %)")
		}
	case model.KindFixed:
		if p.CommissionValue < 1 {
			v.Add("commission_value", "an amount in minor units, at least 1")
		}
		cur := strings.ToUpper(in.GetCurrency())
		if _, ok := money.LookupCurrency(cur); !ok {
			v.Add("currency", "a fixed commission needs a currency (ISO 4217)")
		}
		p.Currency = &cur
	default:
		v.Add("commission_kind", "percent_bps or fixed")
	}
	if p.CommissionKind == model.KindPercent && in.GetCurrency() != "" {
		v.Add("currency", "a percentage follows the currency of each invoice: leave it empty")
	}
	switch p.CommissionTerm {
	case "":
		p.CommissionTerm = model.TermLifetime
		fallthrough
	case model.TermLifetime:
		if in.GetCommissionTermValue() != 0 {
			v.Add("commission_term_value", "a lifetime commission has no number")
		}
	case model.TermFirstInvoices, model.TermMonths:
		if n := in.GetCommissionTermValue(); n < 1 || n > 120 {
			v.Add("commission_term_value", "between 1 and 120")
		} else {
			p.CommissionTermValue = &n
		}
	default:
		v.Add("commission_term", "first_invoices, months or lifetime")
	}
	if p.AttributionWindowDays == 0 {
		p.AttributionWindowDays = 30
	}
	if p.AttributionWindowDays < 1 || p.AttributionWindowDays > 365 {
		v.Add("attribution_window_days", "between 1 and 365")
	}
	if p.HoldDays < 0 || p.HoldDays > 180 {
		v.Add("hold_days", "between 0 and 180")
	}
	if p.MinPayoutMinor < 0 {
		v.Add("min_payout_minor", "zero or more")
	}
	if p.DailyClickCap == 0 {
		p.DailyClickCap = 1000
	}
	if p.DailyClickCap < 1 || p.DailyClickCap > 100000 {
		v.Add("daily_click_cap", "between 1 and 100000")
	}
	if u := in.GetLandingUrl(); u != "" {
		if len(u) > 500 {
			v.Add("landing_url", "at most 500 characters")
		} else if parsed, err := url.Parse(u); err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
			v.Add("landing_url", "an http(s) address")
		} else {
			p.LandingUrl = &u
		}
	}
	return p, in.GetReferredCouponCode(), v.Err()
}

// RandomCode makes a code of the program's alphabet (no 0 O 1 I L).
func RandomCode() (string, error) {
	alphabet := model.CodeAlphabet()
	n := big.NewInt(int64(len(alphabet)))
	b := make([]byte, model.DefaultCodeLength())
	for i := range b {
		x, err := rand.Int(rand.Reader, n)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[x.Int64()]
	}
	return string(b), nil
}

func validEmail(s string) bool {
	if len(s) > 254 || len(s) < 3 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s && strings.Contains(s, "@")
}

// Create adds an affiliate. One the owner makes is active (the owner has approved it by making it) unless pending is asked for.
func (c *Commands) Create(ctx context.Context, req *plansv1.CreateAffiliateRequest) (*plansv1.Affiliate, error) {
	var v domain.Violations
	email := strings.TrimSpace(req.GetEmail())
	if !validEmail(email) {
		v.Add("email", "an e-mail address")
	}
	name := strings.TrimSpace(req.GetName())
	if len(name) > 200 {
		v.Add("name", "at most 200 characters")
	}
	code := strings.TrimSpace(req.GetCode())
	if code != "" && !model.ValidCode(code) {
		v.Add("code", "3 to 32 letters, digits, - or _")
	}
	method, details := req.GetPayoutMethod(), req.GetPayoutDetails()
	if method == "" {
		method = "manual"
	}
	checkPayout(&v, method, details)
	if err := v.Err(); err != nil {
		return nil, err
	}
	status := model.AffiliateActive
	if req.GetPending() {
		status = model.AffiliatePending
	}
	var out *plansv1.Affiliate
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		var err error
		out, err = insert(ctx, q, insertInput{Code: code, Email: email, Name: name, Status: status, Method: method, Details: details, Source: "owner"})
		return err
	})
	if err != nil {
		return nil, mapUnique(convert.Err(err, "affiliate"), err)
	}
	return out, nil
}

func checkPayout(v *domain.Violations, method, details string) {
	ok := false
	for _, m := range payoutMethods {
		ok = ok || m == method
	}
	if !ok {
		v.Add("payout_method", "manual or bank_transfer")
	}
	if len(details) > maxPayoutText {
		v.Add("payout_details", "at most 1000 characters")
	}
}

// insertInput is an affiliate to add.
type insertInput struct {
	Code, Email, Name, Status, Method, Details, Source string
}

// insert adds the affiliate in the caller's transaction. An empty code is made at random; a given one that is taken is affiliate.code_taken.
func insert(ctx context.Context, q *sqlc.Queries, in insertInput) (*plansv1.Affiliate, error) {
	attempts := 1
	random := in.Code == ""
	if random {
		attempts = 8
	}
	for range attempts {
		code := in.Code
		if random {
			var err error
			if code, err = RandomCode(); err != nil {
				return nil, err
			}
		}
		id, err := q.InsertAffiliate(ctx, sqlc.InsertAffiliateParams{Code: code, Email: in.Email, Name: in.Name, Status: in.Status, PayoutMethod: in.Method,
			PayoutDetails: in.Details, Source: in.Source})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		row, err := q.GetAffiliate(ctx, id)
		if err != nil {
			return nil, err
		}
		return query.Message(query.RowOf(row)), nil
	}
	return nil, errkey.New(codes.AlreadyExists, "affiliate.code_taken", "this code is taken: affiliate codes are unique across the platform")
}

// mapUnique turns the unique violation of an e-mail address into its own error.
func mapUnique(mapped, raw error) error {
	if pgxx.ErrCode(raw) == pgxx.CodeUniqueViolation && strings.Contains(pgxx.ConstraintName(raw), "affiliates_email") {
		return errkey.New(codes.AlreadyExists, "affiliate.email_taken", "an affiliate with this e-mail address exists already")
	}
	return mapped
}

// Update changes an affiliate's name, payout details and status. pending → active is an approval; active ↔ suspended is a suspension and a reinstatement;
// nothing returns to pending.
func (c *Commands) Update(ctx context.Context, req *plansv1.UpdateAffiliateRequest) (*plansv1.Affiliate, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, query.NotFound()
	}
	var v domain.Violations
	name := strings.TrimSpace(req.GetName())
	if len(name) > 200 {
		v.Add("name", "at most 200 characters")
	}
	if req.GetStatus() != "" && req.GetStatus() != model.AffiliateActive && req.GetStatus() != model.AffiliateSuspended {
		v.Add("status", "active, suspended or empty")
	}
	if req.GetPayoutMethod() != "" {
		checkPayout(&v, req.GetPayoutMethod(), req.GetPayoutDetails())
	} else if len(req.GetPayoutDetails()) > maxPayoutText {
		v.Add("payout_details", "at most 1000 characters")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	var out *plansv1.Affiliate
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetAffiliate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return query.NotFound()
		}
		if err != nil {
			return err
		}
		// pending → active approves, pending → suspended turns an application down, active ↔ suspended suspends and reinstates; nothing returns to pending.
		next := cur.Status
		if s := req.GetStatus(); s != "" {
			next = s
		}
		method, details := cur.PayoutMethod, cur.PayoutDetails
		if req.GetPayoutMethod() != "" {
			method, details = req.GetPayoutMethod(), req.GetPayoutDetails()
		}
		newName := cur.Name
		if req.GetName() != "" {
			newName = name
		}
		n, err := q.UpdateAffiliate(ctx, sqlc.UpdateAffiliateParams{ID: id, Name: newName, PayoutMethod: method, PayoutDetails: details, Status: next})
		if err != nil {
			return err
		}
		if n == 0 {
			return query.NotFound()
		}
		row, err := q.GetAffiliate(ctx, id)
		if err != nil {
			return err
		}
		out = query.Message(query.RowOf(row))
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	return out, nil
}

// LinkPromotionCode makes a promotion code the affiliate's.
func (c *Commands) LinkPromotionCode(ctx context.Context, req *plansv1.LinkAffiliatePromotionCodeRequest) (*plansv1.Affiliate, error) {
	id, err := uuid.Parse(req.GetAffiliateId())
	if err != nil {
		return nil, query.NotFound()
	}
	if req.GetPromotionCode() == "" {
		return nil, domain.Invalid("promotion_code", "required")
	}
	var out *plansv1.Affiliate
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		row, err := q.GetAffiliate(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return query.NotFound()
		}
		if err != nil {
			return err
		}
		n, err := q.LinkPromotionCodeToAffiliate(ctx, sqlc.LinkPromotionCodeToAffiliateParams{AffiliateID: &id, Code: req.GetPromotionCode()})
		if err != nil {
			return err
		}
		if n == 0 {
			return errkey.New(codes.NotFound, "affiliate.promotion_code_not_found", "no such promotion code")
		}
		out = query.Message(query.RowOf(row))
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	return out, nil
}
