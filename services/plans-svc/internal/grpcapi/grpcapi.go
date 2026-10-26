// Package grpcapi wires the modules into the gRPC services plans-svc registers: PlansService (whose methods the modules serve, each its own)
// and WebhookService. It holds no logic. Every handler runs in pgxx.InTenantTx on the app pool (role plans_app), so RLS scopes every
// statement to the caller's tenant.
package grpcapi

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	affiliatesapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	affiliatesportal "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/portal"
	portalcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/portal/command"
	portalqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/portal/query"
	affiliatesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/query"
	changesapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes"
	changescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/command"
	changesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/changes/query"
	couponsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons"
	couponscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/command"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	customersapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/customers"
	customerscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/customers/command"
	customersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/customers/query"
	feesapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/fees"
	feescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/fees/command"
	feesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/fees/query"
	mailsettingsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings"
	mailsettingscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/command"
	mailsettingsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/query"
	metersapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters"
	meterscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/command"
	metersqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/meters/query"
	overridesapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/overrides"
	overridescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/overrides/command"
	overridesqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/overrides/query"
	plansapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans"
	planscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/command"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	provmigapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/providermigrations"
	provmigcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/providermigrations/command"
	provmigqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/providermigrations/query"
	reportsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/reports"
	reportsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/reports/query"
	routingapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/routing"
	routingcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/routing/command"
	routingqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/routing/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	subscriptionsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions"
	subscriptionscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/command"
	subscriptionsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	taxapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax"
	taxcmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/command"
	taxqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tax/query"
	webhookendpointsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints"
	webhookendpointscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints/command"
	webhookendpointsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/webhookendpoints/query"
)

// The module handlers under names that differ, so that they can be embedded side by side.
type (
	customersHandler     = customersapi.Handler
	metersHandler        = metersapi.Handler
	overridesHandler     = overridesapi.Handler
	plansHandler         = plansapi.Handler
	subscriptionsHandler = subscriptionsapi.Handler
	changesHandler       = changesapi.Handler
	couponsHandler       = couponsapi.Handler
	feesHandler          = feesapi.Handler
	migrationsHandler    = provmigapi.Handler
	routingHandler       = routingapi.Handler
	taxHandler           = taxapi.Handler
	reportsHandler       = reportsapi.Handler
	mailSettingsHandler  = mailsettingsapi.Handler
)

// unimplemented answers Unimplemented for a method of PlansService that no module serves yet. It is embedded one level deeper than the
// module handlers, so that their methods win over its.
type unimplemented struct {
	plansv1.UnimplementedPlansServiceServer
}

// Server implements plansv1.PlansServiceServer by delegating each method to the module that owns it.
type Server struct {
	unimplemented
	*customersHandler
	*metersHandler
	*overridesHandler
	*plansHandler
	*subscriptionsHandler
	*changesHandler
	*couponsHandler
	*feesHandler
	*migrationsHandler
	*routingHandler
	*taxHandler
	*reportsHandler
	*mailSettingsHandler

	db  *store.DB
	now func() time.Time
}

// newPlans returns a Server on the app pool.
func newPlans(appPool *pgxpool.Pool) *Server {
	db := store.New(appPool)
	now := time.Now

	subscriptionsQ := subscriptionsqry.New(db, now)
	return &Server{
		customersHandler:     customersapi.New(customerscmd.New(db).WithReferral(affiliatescmd.Attribute, now), customersqry.New(db)),
		metersHandler:        metersapi.New(meterscmd.New(db), metersqry.New(db)),
		overridesHandler:     overridesapi.New(overridescmd.New(db, now), overridesqry.New(db)),
		plansHandler:         plansapi.New(planscmd.New(db), plansqry.New(db)),
		subscriptionsHandler: subscriptionsapi.New(subscriptionscmd.New(db, now), subscriptionsQ),
		changesHandler:       changesapi.New(changescmd.New(db, now), changesqry.New(db, now)),
		couponsHandler:       couponsapi.New(couponscmd.New(db, now).WithRedeemHook(affiliatescmd.AttributeByPromotionCode), couponsqry.New(db)),
		feesHandler:          feesapi.New(feescmd.New(db), feesqry.New(db, now)),
		migrationsHandler:    provmigapi.New(provmigcmd.New(db, now), provmigqry.New(db)),
		routingHandler:       routingapi.New(routingcmd.New(db), routingqry.New(db)),
		taxHandler:           taxapi.New(taxcmd.New(db), taxqry.New(db)),
		reportsHandler:       reportsapi.New(reportsqry.New(db, now)),
		mailSettingsHandler:  mailsettingsapi.New(mailsettingscmd.New(db), mailsettingsqry.New(db)),
		db:                   db,
		now:                  now,
	}
}

// newWebhooks returns the webhook service on the same database as s. box seals the signing secrets (nil: endpoints cannot be created).
// allowPrivate lets endpoints be http:// and private addresses (development and tests only).
func newWebhooks(s *Server, box *secretbox.Box, allowPrivate bool) *webhookendpointsapi.Handler {
	return webhookendpointsapi.New(webhookendpointscmd.New(s.db, s.now, box, allowPrivate).WithEventHook(affiliatescmd.OnTenantEvent), webhookendpointsqry.New(s.db))
}

// newAffiliates returns the affiliate program service on the same database as s.
func newAffiliates(s *Server) *affiliatesapi.Handler {
	return affiliatesapi.New(affiliatescmd.New(s.db, s.now), affiliatesqry.New(s.db))
}

// newAffiliatesPublic returns the public link service on the system pool: a visitor has no tenant.
func newAffiliatesPublic(systemPool *pgxpool.Pool, log *slog.Logger) *affiliatesapi.PublicHandler {
	return affiliatesapi.NewPublic(affiliatescmd.NewPublic(systemPool, time.Now, log))
}

// newAffiliatePortal returns what a signed-in partner sees of their own affiliations: the person is found on the system pool, the figures are read through the tenant's
// own queries on the same database as s.
func newAffiliatePortal(s *Server, systemPool *pgxpool.Pool) *affiliatesportal.Handler {
	return affiliatesportal.New(portalcmd.New(systemPool, s.now), portalqry.New(systemPool, affiliatesqry.New(s.db), s.now))
}
