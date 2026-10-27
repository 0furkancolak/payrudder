package command

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Resolver finds the tenant of an affiliate code. A code is unique across the platform, and a visitor of the public link has no tenant, so the code is what finds it:
// in the pooled database that is one statement over all tenants. A tenant placed in a cell (ADR 0018) has its affiliates in its own schema, which no statement of
// the pooled database sees, so after the pooled database the tenants in cells are asked one after the other (a code found for a tenant is remembered for a while, so
// a busy link costs one query of that tenant, not a visit of all). The database cannot enforce "unique across the platform" over schemas: Taken is the check that
// makes a code given by a seller not shadow another's, best effort (two sellers choosing the same new code at the same moment are not told apart).
type Resolver struct {
	db     *sqlc.Queries
	places *placement.Places
	now    func() time.Time

	mu    sync.Mutex
	known map[string]remembered
}

type remembered struct {
	tenant uuid.UUID
	until  time.Time
}

const (
	rememberFor  = 10 * time.Minute
	rememberMax  = 10000
	placementAge = 10 * time.Second // how old the list of the tenants in cells may be when a remembered code is looked up
)

// NewResolver returns the resolver over the system pool of places.
func NewResolver(places *placement.Places) *Resolver {
	return &Resolver{db: sqlc.New(places.Pool()), places: places, now: time.Now, known: map[string]remembered{}}
}

// Found is an affiliate code that belongs to a tenant: the line the link needs, and where the tenant's rows are (nil: the pooled database).
type Found struct {
	Row  sqlc.SystemResolveAffiliateCodeRow
	Cell *pgxx.PlacedTenant
}

// Resolve finds the affiliate of a code. found is false when no tenant has it.
func (r *Resolver) Resolve(ctx context.Context, code string) (f Found, found bool, err error) {
	row, err := r.db.SystemResolveAffiliateCode(ctx, code)
	switch {
	case err == nil:
		return Found{Row: row}, true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return Found{}, false, err
	}
	if !r.places.CellsOn() {
		return Found{}, false, nil
	}
	key := strings.ToUpper(code)
	if t, hit := r.recall(key); hit {
		f, ok, err := r.inTenant(ctx, t, code)
		if err != nil || ok {
			return f, ok, err
		}
		r.forget(key)
	}
	err = r.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		if found {
			return nil
		}
		var ok bool
		var ferr error
		if f, ok, ferr = r.query(ctx, t, code); ferr != nil {
			return ferr
		}
		if ok {
			found = true
			r.remember(key, t.TenantID)
		}
		return nil
	})
	if found {
		return f, true, nil
	}
	return Found{}, false, err
}

// inTenant looks for the code in the schema of one remembered tenant.
func (r *Resolver) inTenant(ctx context.Context, id uuid.UUID, code string) (Found, bool, error) {
	placed, err := r.places.Router().PlacedWithin(ctx, placementAge)
	if err != nil && placed == nil {
		return Found{}, false, err
	}
	for _, t := range placed {
		if t.TenantID == id && t.Serving() {
			return r.query(ctx, t, code)
		}
	}
	return Found{}, false, nil
}

// query asks one tenant's schema.
func (r *Resolver) query(ctx context.Context, t pgxx.PlacedTenant, code string) (Found, bool, error) {
	var row sqlc.SystemResolveAffiliateCodeRow
	err := r.places.CellTx(ctx, t, func(tx pgx.Tx) (err error) {
		row, err = sqlc.New(tx).SystemResolveAffiliateCode(ctx, code)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Found{}, false, nil
	}
	if err != nil {
		return Found{}, false, err
	}
	if row.TenantID != t.TenantID {
		return Found{}, false, errors.New("affiliate: a record of another tenant is in the schema of tenant " + t.TenantID.String())
	}
	return Found{Row: row, Cell: &t}, true, nil
}

func (r *Resolver) recall(key string) (uuid.UUID, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.known[key]
	if !ok || r.now().After(k.until) {
		delete(r.known, key)
		return uuid.Nil, false
	}
	return k.tenant, true
}

func (r *Resolver) remember(key string, tenant uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.known) >= rememberMax {
		clear(r.known)
	}
	r.known[key] = remembered{tenant: tenant, until: r.now().Add(rememberFor)}
}

func (r *Resolver) forget(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.known, key)
}

// Taken reports whether another tenant than own already has the code. Within own tenant the database says (a unique index on the code), and a tenant that is not
// reachable (moving) is not asked.
func (r *Resolver) Taken(ctx context.Context, code string, own uuid.UUID) (bool, error) {
	f, found, err := r.Resolve(ctx, code)
	if err != nil {
		return false, err
	}
	return found && f.Row.TenantID != own, nil
}

// Public serves the public link of the hosted pages. It runs on the system pool because a visitor has no tenant: the affiliate's code, unique across the
// platform, is what finds it. Nothing about the visitor reaches this: a click is a number added to the affiliate's count for the day.
type Public struct {
	db     *sqlc.Queries
	res    *Resolver
	places *placement.Places
	now    func() time.Time
	log    *slog.Logger
}

// NewPublic returns the public link handler on the system pool.
func NewPublic(systemPool *pgxpool.Pool, now func() time.Time, log *slog.Logger) *Public {
	places := placement.NewPlaces(systemPool, nil)
	return &Public{db: sqlc.New(systemPool), res: NewResolver(places), places: places, now: now, log: log}
}

// WithResolver makes the handler find codes where the tenants' rows are, cells included, and count the click in the tenant's own schema.
func (p *Public) WithResolver(r *Resolver) *Public {
	p.res, p.places = r, r.places
	return p
}

// TrackClick counts a visit on the link of code and says what the page does about it. A code that is not an active affiliate's of an active program is not
// valid and counts nothing; once the affiliate's daily cap is reached the visitor is still welcome (the cookie is set) but the visit is not counted.
func (p *Public) TrackClick(ctx context.Context, req *plansv1.TrackAffiliateClickRequest) (*plansv1.TrackAffiliateClickResponse, error) {
	code := strings.TrimSpace(req.GetCode())
	if !model.ValidCode(code) {
		return &plansv1.TrackAffiliateClickResponse{}, nil
	}
	found, ok, err := p.res.Resolve(ctx, code)
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	if !ok {
		return &plansv1.TrackAffiliateClickResponse{}, nil
	}
	row := found.Row
	if row.AffiliateStatus != model.AffiliateActive || row.ProgramStatus != model.ProgramActive {
		return &plansv1.TrackAffiliateClickResponse{}, nil
	}
	if req.GetSkipCount() {
		// The visitor carries the code in their cookie already: a reload is not a second click.
		return &plansv1.TrackAffiliateClickResponse{Valid: true, Code: row.Code, AttributionWindowDays: row.AttributionWindowDays, LandingUrl: convert.Str(row.LandingUrl)}, nil
	}
	day := p.now().UTC()
	count := sqlc.SystemCountClickParams{TenantID: row.TenantID, AffiliateID: row.ID,
		Day: pgtype.Date{Time: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), Valid: true}, Cap: row.DailyClickCap}
	var n int64
	if found.Cell == nil {
		n, err = p.db.SystemCountClick(ctx, count)
	} else {
		err = p.places.CellTx(ctx, *found.Cell, func(tx pgx.Tx) (err error) {
			n, err = sqlc.New(tx).SystemCountClick(ctx, count)
			return err
		})
	}
	if err != nil {
		return nil, convert.Err(err, "affiliate")
	}
	if n == 0 {
		p.log.InfoContext(ctx, "affiliate click not counted: daily cap reached", "affiliate_id", row.ID, "tenant_id", row.TenantID)
	}
	return &plansv1.TrackAffiliateClickResponse{Valid: true, Code: row.Code, AttributionWindowDays: row.AttributionWindowDays, LandingUrl: convert.Str(row.LandingUrl), Counted: n == 1}, nil
}
