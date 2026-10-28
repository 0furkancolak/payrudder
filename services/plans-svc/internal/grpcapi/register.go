package grpcapi

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	affiliatescmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/command"
	apikeyscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/directory"
	directorycmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/command"
	directoryqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/golive"
	golivecmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/command"
	goliveqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity"
	identitycmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/onboarding"
	identityqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	saasqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/query"
	sandboxapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox/api"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/support"
)

// Deps is what the gRPC services of plans-svc are built from. The service and its integration tests both call Register with it, so what a test
// serves is what production serves.
type Deps struct {
	AppDB      pgxx.Beginner // role plans_app: every statement of a tenant's request is scoped by RLS. The app pool, or with cells on its router (placement.AppDB)
	SystemPool *pgxpool.Pool // role plans_system: the operator, identity and partner modules
	// Places says where a tenant's own rows are for the system role (ADR 0018). Nil is cells off: plans-svc then builds the pooled one over SystemPool.
	Places      *placement.Places
	Log         *slog.Logger
	Evict       apikeyscmd.Evictor // drops a revoked key from the gateway cache; nil leaves it to the cache TTL
	AllowedEnvs []string           // PAYRUDDER_ALLOWED_ENVIRONMENTS
	Box         *secretbox.Box     // seals webhook signing secrets; nil: endpoints cannot be created
	// WebhookAllowPrivate lets endpoints be http:// and on private addresses (development and tests only).
	WebhookAllowPrivate bool
	Mail                usermail.Sender // sends the verification, reset and invitation mails
	AppURL              string          // the console's base URL, which mailed links point into
	OTPPepper           string          // keys the hash of one-time sign-in codes
	Sandbox             *sandboxapi.Handler
	// Saas is the SaaS mode (PAYRUDDER_SAAS, phase 55): Payrudder's own plans, enforced on every account. The zero value is off.
	Saas saascmd.Config
	// CellsReady says whether a tenant may be placed in a cell (ADR 0018): nil when every cross-tenant job is converted, an error naming what is left otherwise.
	// nil in the Deps means pkg/tenantdir's inventory; a test passes its own.
	CellsReady func() error
}

// Register serves every gRPC service of plans-svc on srv and returns the SaaS commands (the platform tenant's bootstrap and the sweep that makes every
// account a customer of it), which the service runs when SaaS is on.
func Register(srv grpc.ServiceRegistrar, d Deps) *saascmd.Commands {
	if d.Places == nil {
		d.Places = placement.NewPlaces(d.SystemPool, nil)
	}
	resolver := affiliatescmd.NewResolver(d.Places)
	plansSrv := newPlans(d.AppDB, resolver)
	plansv1.RegisterPlansServiceServer(srv, plansSrv)
	plansv1.RegisterWebhookServiceServer(srv, newWebhooks(plansSrv, d.Box, d.WebhookAllowPrivate))
	plansv1.RegisterMailIntegrationServiceServer(srv, newMailIntegration(plansSrv, d.Box, d.WebhookAllowPrivate))
	plansv1.RegisterAffiliateServiceServer(srv, newAffiliates(plansSrv))
	plansv1.RegisterAffiliatePublicServiceServer(srv, newAffiliatesPublic(d.SystemPool, resolver, d.Log))
	plansv1.RegisterAffiliatePortalServiceServer(srv, newAffiliatePortal(plansSrv, d.Places))
	admin := newAdmin(d.SystemPool, d.Evict, d.AllowedEnvs, d.Log)
	plansv1.RegisterAdminServiceServer(srv, admin)
	plansv1.RegisterDirectoryServiceServer(srv, directory.New(directoryqry.New(d.SystemPool), directorycmd.New(d.SystemPool, d.CellsReady, d.Log)))
	plansv1.RegisterSupportServiceServer(srv, support.New(d.SystemPool, d.Log))
	saasQ := saasqry.New(d.SystemPool, d.Saas.TrialDays, time.Now).WithPlaces(d.Places)
	saasC := saascmd.New(d.Saas, d.SystemPool, plansSrv, saasQ, d.Log)
	plansv1.RegisterSaasServiceServer(srv, saas.New(saasC, saasQ, d.SystemPool))
	cmd := identitycmd.New(d.SystemPool, admin.accounts, admin.keys, d.Mail, d.AppURL, d.Log).WithPepper(d.OTPPepper)
	if saasC.Enabled() {
		cmd.WithGuard(saasC) // an interface holding a nil pointer is not nil: the guard is only set when the mode is on
	}
	goLive := golive.New(golivecmd.New(d.Places, d.Log), goliveqry.New(d.Places))
	plansv1.RegisterIdentityServiceServer(srv, identity.New(cmd, identityqry.New(d.SystemPool), goLive,
		promote.Wire(d.AppDB, d.SystemPool, d.Box, d.WebhookAllowPrivate, d.Log), d.Sandbox, onboarding.New(d.SystemPool)))
	return saasC
}
