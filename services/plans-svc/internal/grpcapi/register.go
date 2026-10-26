package grpcapi

import (
	"log/slog"

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
	identityqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/promote"
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
}

// Register serves every gRPC service of plans-svc on srv.
func Register(srv grpc.ServiceRegistrar, d Deps) {
	plansSrv := newPlans(d.AppPool)
	plansv1.RegisterPlansServiceServer(srv, plansSrv)
	plansv1.RegisterWebhookServiceServer(srv, newWebhooks(plansSrv, d.Box, d.WebhookAllowPrivate))
	plansv1.RegisterAffiliateServiceServer(srv, newAffiliates(plansSrv))
	plansv1.RegisterAffiliatePublicServiceServer(srv, newAffiliatesPublic(d.SystemPool, d.Log))
	plansv1.RegisterAffiliatePortalServiceServer(srv, newAffiliatePortal(plansSrv, d.SystemPool))
	admin := newAdmin(d.SystemPool, d.Evict, d.AllowedEnvs, d.Log)
	plansv1.RegisterAdminServiceServer(srv, admin)
	cmd := identitycmd.New(d.SystemPool, admin.accounts, admin.keys, d.Mail, d.AppURL, d.Log).WithPepper(d.OTPPepper)
	goLive := golive.New(golivecmd.New(d.SystemPool, d.Log), goliveqry.New(d.SystemPool))
	plansv1.RegisterIdentityServiceServer(srv, identity.New(cmd, identityqry.New(d.SystemPool), goLive,
		promote.Wire(d.AppPool, d.SystemPool, d.Box, d.WebhookAllowPrivate, d.Log), d.Sandbox))
}
