package command

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Public serves the public link of the hosted pages. It runs on the system pool because a visitor has no tenant: the affiliate's code, unique across the
// platform, is what finds it. Nothing about the visitor reaches this: a click is a number added to the affiliate's count for the day.
type Public struct {
	db  *sqlc.Queries
	now func() time.Time
	log *slog.Logger
}

// NewPublic returns the public link handler on the system pool.
func NewPublic(systemPool *pgxpool.Pool, now func() time.Time, log *slog.Logger) *Public {
	return &Public{db: sqlc.New(systemPool), now: now, log: log}
}

// TrackClick counts a visit on the link of code and says what the page does about it. A code that is not an active affiliate's of an active program is not
// valid and counts nothing; once the affiliate's daily cap is reached the visitor is still welcome (the cookie is set) but the visit is not counted.
func (p *Public) TrackClick(ctx context.Context, req *plansv1.TrackAffiliateClickRequest) (*plansv1.TrackAffiliateClickResponse, error) {
	code := strings.TrimSpace(req.GetCode())
	if !model.ValidCode(code) {
		return &plansv1.TrackAffiliateClickResponse{}, nil
	}
	row, err := p.db.SystemResolveAffiliateCode(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return &plansv1.TrackAffiliateClickResponse{}, nil
	}
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	if row.AffiliateStatus != model.AffiliateActive || row.ProgramStatus != model.ProgramActive {
		return &plansv1.TrackAffiliateClickResponse{}, nil
	}
	if req.GetSkipCount() {
		// The visitor carries the code in their cookie already: a reload is not a second click.
		return &plansv1.TrackAffiliateClickResponse{Valid: true, Code: row.Code, AttributionWindowDays: row.AttributionWindowDays, LandingUrl: convert.Str(row.LandingUrl)}, nil
	}
	day := p.now().UTC()
	n, err := p.db.SystemCountClick(ctx, sqlc.SystemCountClickParams{TenantID: row.TenantID, AffiliateID: row.ID,
		Day: pgtype.Date{Time: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), Valid: true}, Cap: row.DailyClickCap})
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	if n == 0 {
		p.log.InfoContext(ctx, "affiliate click not counted: daily cap reached", "affiliate_id", row.ID, "tenant_id", row.TenantID)
	}
	return &plansv1.TrackAffiliateClickResponse{Valid: true, Code: row.Code, AttributionWindowDays: row.AttributionWindowDays, LandingUrl: convert.Str(row.LandingUrl), Counted: n == 1}, nil
}
