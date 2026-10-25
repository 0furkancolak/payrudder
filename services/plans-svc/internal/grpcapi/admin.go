package grpcapi

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	accountsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/accounts"
	accountscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/accounts/command"
	accountsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/accounts/query"
	apikeysapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys"
	apikeyscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	apikeysqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/query"
	tenantsapi "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants"
	tenantscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/command"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// The admin module handlers under names that differ, so that they can be embedded side by side.
type (
	accountsHandler = accountsapi.Handler
	tenantsHandler  = tenantsapi.Handler
	apiKeysHandler  = apikeysapi.Handler
)

// unimplementedAdmin answers Unimplemented for a method of AdminService that no module serves yet. It is embedded one level deeper than the module
// handlers, so that their methods win over its.
type unimplementedAdmin struct {
	plansv1.UnimplementedAdminServiceServer
}

// AdminServer implements plansv1.AdminServiceServer by delegating each method to the module that owns it. Its modules run on the system pool
// (role plans_system, BYPASSRLS): an operator reaches every tenant, and the gateway finds a key before it knows the tenant.
type AdminServer struct {
	unimplementedAdmin
	*accountsHandler
	*tenantsHandler
	*apiKeysHandler

	accounts *accountscmd.Commands
	keys     *apikeyscmd.Commands
}

// NewAdmin returns AdminService on the system pool. evict drops revoked keys from the gateway cache (nil: they stay valid there until its TTL).
// allowedEnvs is PAYRUDDER_ALLOWED_ENVIRONMENTS ("sandbox", "live").
func NewAdmin(systemPool *pgxpool.Pool, evict apikeyscmd.Evictor, allowedEnvs []string, log *slog.Logger) *AdminServer {
	accounts := accountscmd.New(systemPool, allowedEnvs, log)
	keys := apikeyscmd.New(systemPool, evict, log)
	return &AdminServer{
		accountsHandler: accountsapi.New(accounts, accountsqry.New(systemPool)),
		tenantsHandler:  tenantsapi.New(tenantscmd.New(systemPool, keys, log), tenantsqry.New(systemPool)),
		apiKeysHandler:  apikeysapi.New(keys, apikeysqry.New(systemPool)),
		accounts:        accounts,
		keys:            keys,
	}
}

// Accounts is the command that makes apps, which the identity module calls for a signed-in owner.
func (s *AdminServer) Accounts() *accountscmd.Commands { return s.accounts }

// Keys is the command that issues and revokes API keys, which the identity module calls for a member of the tenant's account.
func (s *AdminServer) Keys() *apikeyscmd.Commands { return s.keys }
