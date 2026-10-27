// Package query computes the go-live checklist of an app's live environment (phase 45). It never writes.
//
// Live is earned: a live tenant is created inactive and gets no API keys until every item is true. The list is computed here, on the server, from the data
// that decides it; the console only draws it. Four items are facts of plans-svc's own database. The fifth, a live payment provider, is a fact of
// payments-worker's database: the gateway asks payments-worker and passes the answer in (only the gateway calls this service).
package query

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	idquery "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// The items of the checklist, in the order the console shows them.
const (
	ItemEmailVerified   = "email_verified"
	ItemLegalProfile    = "legal_profile"
	ItemPaymentProvider = "payment_provider"
	ItemPlanPublished   = "plan_published"
	ItemTermsAccepted   = "terms_accepted"
)

// Queries reads on the system pool.
type Queries struct {
	pool   *pgxpool.Pool
	places *placement.Places
}

// New returns the go-live queries. places says where the live tenant's plans are (in a cell, they are not in the pool).
func New(places *placement.Places) *Queries { return &Queries{pool: places.Pool(), places: places} }

// NoLive is the answer for an app whose deployment serves no live environment (PAYRUDDER_ALLOWED_ENVIRONMENTS=sandbox).
func NoLive() error {
	return errkey.New(codes.FailedPrecondition, "identity.go_live.no_live", "this deployment has no live environment")
}

// LiveTenant picks the live tenant of an app.
func LiveTenant(app *plansv1.App) (*plansv1.Tenant, error) {
	for _, t := range app.GetTenants() {
		if t.GetEnvironment() == env.Live.Proto() {
			return t, nil
		}
	}
	return nil, NoLive()
}

// GoLive is the checklist of the app of a member. Any member may read it; only the owner may activate (see the command).
func (q *Queries) GoLive(ctx context.Context, userID, appID string, providerReady bool) (*plansv1.GoLive, error) {
	db := sqlc.New(q.pool)
	app, _, err := idquery.AppOf(ctx, db, userID, appID)
	if err != nil {
		return nil, err
	}
	live, err := LiveTenant(app)
	if err != nil {
		return nil, err
	}
	return Checklist(ctx, q.pool, q.places, live, providerReady)
}

// Checklist computes the items for a live tenant on db (the system pool, or a transaction of it for the command that decides on them). What is about the tenant's own
// rows (a published plan) is read where they are: on db when the tenant is pooled, in the tenant's schema in a cell.
func Checklist(ctx context.Context, dbtx sqlc.DBTX, places *placement.Places, live *plansv1.Tenant, providerReady bool) (*plansv1.GoLive, error) {
	db := sqlc.New(dbtx)
	accountID, err := uuid.Parse(live.GetAccountId())
	if err != nil {
		return nil, idquery.NotFound("account")
	}
	tenantID, err := uuid.Parse(live.GetId())
	if err != nil {
		return nil, idquery.NotFound("tenant")
	}
	verified, err := db.AccountOwnerVerified(ctx, accountID)
	if err != nil {
		return nil, convert.Err(err, "account")
	}
	published, err := hasPublishedPlan(ctx, dbtx, places, tenantID)
	if err != nil {
		return nil, convert.Err(err, "plan")
	}
	out := &plansv1.GoLive{Status: live.GetStatus(), TenantId: live.GetId()}
	add := func(key string, done bool) { out.Items = append(out.Items, &plansv1.GoLiveItem{Key: key, Done: done}) }
	add(ItemEmailVerified, verified)
	add(ItemLegalProfile, domain.LegalProfileComplete(live.GetAddress(), live.GetLegal()))
	add(ItemPaymentProvider, providerReady)
	add(ItemPlanPublished, published)
	add(ItemTermsAccepted, live.GetTermsAcceptTime() != nil)
	out.Ready = true
	for _, it := range out.Items {
		out.Ready = out.Ready && it.GetDone()
	}
	return out, nil
}

// hasPublishedPlan asks the tenant's own rows whether it has published a plan version.
func hasPublishedPlan(ctx context.Context, db sqlc.DBTX, places *placement.Places, tenantID uuid.UUID) (published bool, err error) {
	err = places.OnTenant(ctx, tenantID, db, func(q *sqlc.Queries) (err error) {
		published, err = q.TenantHasPublishedPlan(ctx, tenantID)
		return err
	})
	return published, err
}

// Missing lists the keys of the items that are not done, for a refusal's message.
func Missing(g *plansv1.GoLive) string {
	var keys []string
	for _, it := range g.GetItems() {
		if !it.GetDone() {
			keys = append(keys, it.GetKey())
		}
	}
	return strings.Join(keys, ", ")
}

// Row is the live tenant row as the API message the checklist reads (legal, address and terms come from it).
func Row(t sqlc.Tenant) *plansv1.Tenant { return convert.Tenant(t) }
