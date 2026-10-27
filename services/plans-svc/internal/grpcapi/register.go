package grpcapi

import (
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	apikeyscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/golive"
	golivecmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/command"
	goliveqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/golive/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity"
	identitycmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/onboarding"
	identityqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/saas"
	saascmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/command"
	saasqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/query"
	sandboxapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/sandbox/api"
)

// Deps is what the gRPC services of plans-svc are built from. The service and its integration tests both call Register with it, so what a test
// serves is what production serves.
type Deps struct {
	AppPool     *pgxpool.Pool // role plans_app: every statement of a tenant's request is scoped by RLS
	SystemPool  *pgxpool.Pool // role plans_system: the operator, identity and partner modules
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
}

// Register serves every gRPC service of plans-svc on srv and returns the SaaS commands (the platform tenant's bootstrap and the sweep that makes every
// account a customer of it), which the service runs when SaaS is on.
func Register(srv grpc.ServiceRegistrar, d Deps) *saascmd.Commands {
	plansSrv := newPlans(d.AppPool)
	plansv1.RegisterPlansServiceServer(srv, plansSrv)
	plansv1.RegisterWebhookServiceServer(srv, newWebhooks(plansSrv, d.Box, d.WebhookAllowPrivate))
	plansv1.RegisterMailIntegrationServiceServer(srv, newMailIntegration(plansSrv, d.Box, d.WebhookAllowPrivate))
	plansv1.RegisterAffiliateServiceServer(srv, newAffiliates(plansSrv))
	plansv1.RegisterAffiliatePublicServiceServer(srv, newAffiliatesPublic(d.SystemPool, d.Log))
	plansv1.RegisterAffiliatePortalServiceServer(srv, newAffiliatePortal(plansSrv, d.SystemPool))
	admin := newAdmin(d.SystemPool, d.Evict, d.AllowedEnvs, d.Log)
	plansv1.RegisterAdminServiceServer(srv, admin)
	saasQ := saasqry.New(d.SystemPool, d.Saas.TrialDays, time.Now)
	saasC := saascmd.New(d.Saas, d.SystemPool, plansSrv, saasQ, d.Log)
	plansv1.RegisterSaasServiceServer(srv, saas.New(saasC, saasQ, d.SystemPool))
	cmd := identitycmd.New(d.SystemPool, admin.accounts, admin.keys, d.Mail, d.AppURL, d.Log).WithPepper(d.OTPPepper)
	if saasC.Enabled() {
		cmd.WithGuard(saasC) // an interface holding a nil pointer is not nil: the guard is only set when the mode is on
	}
	goLive := golive.New(golivecmd.New(d.SystemPool, d.Log), goliveqry.New(d.SystemPool))
	plansv1.RegisterIdentityServiceServer(srv, identity.New(cmd, identityqry.New(d.SystemPool), goLive,
		promote.Wire(d.AppPool, d.SystemPool, d.Box, d.WebhookAllowPrivate, d.Log), d.Sandbox, onboarding.New(d.SystemPool)))
	return saasC
}
