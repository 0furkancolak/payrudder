// Package admin implements AdminService. Its operations are cross-tenant by nature (creating accounts, resolving keys),
// so it runs on the system pool (role plans_system, BYPASSRLS). It is the only request-handling package allowed to
// hold that pool; access is restricted to operator calls, except ResolveApiKey (gateway only).
package admin

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// CacheEvictor removes a key from the gateway's API-key cache (Redis gw:apikey:{prefix}).
type CacheEvictor interface {
	EvictAPIKey(ctx context.Context, prefix string) error
}

// Server implements plansv1.AdminServiceServer.
type Server struct {
	plansv1.UnimplementedAdminServiceServer
	pool        *pgxpool.Pool
	evict       CacheEvictor
	log         *slog.Logger
	allowedEnvs []string
}

// New returns an AdminService on the system pool. allowedEnvs is QUOTIFY_ALLOWED_ENVIRONMENTS ("sandbox", "live").
func New(systemPool *pgxpool.Pool, evict CacheEvictor, allowedEnvs []string, log *slog.Logger) *Server {
	return &Server{pool: systemPool, evict: evict, log: log, allowedEnvs: allowedEnvs}
}

func (s *Server) q() *sqlc.Queries { return sqlc.New(s.pool) }

func (s *Server) CreateAccount(ctx context.Context, req *plansv1.CreateAccountRequest) (*plansv1.CreateAccountResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	settings, err := domain.ValidateCreateAccount(req)
	if err != nil {
		return nil, err
	}
	acct := &plansv1.Account{}
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		a, err := q.InsertAccount(ctx, sqlc.InsertAccountParams{ID: idgen.NewUUIDv7(), Name: settings.Name})
		if err != nil {
			return err
		}
		acct.Id, acct.Name, acct.CreateTime = a.ID.String(), a.Name, convertTS(a.CreatedAt)
		app, err := s.insertApp(ctx, q, a.ID, settings, defaultSlug)
		if err != nil {
			return err
		}
		acct.Apps = append(acct.Apps, app)
		acct.Tenants = app.Tenants
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "account")
	}
	s.log.InfoContext(ctx, "account created", "account_id", acct.GetId())
	return &plansv1.CreateAccountResponse{Account: acct}, nil
}

// defaultSlug is the slug of the app an account is created with.
const defaultSlug = "default"

// insertApp makes an app of the account with its sandbox tenant and, when the deployment allows live, its live tenant.
func (s *Server) insertApp(ctx context.Context, q *sqlc.Queries, accountID uuid.UUID, settings domain.AccountSettings, slug string) (*plansv1.App, error) {
	envs := []string{"sandbox"}
	if slices.Contains(s.allowedEnvs, "live") {
		envs = append(envs, "live")
	}
	app, err := q.InsertApp(ctx, sqlc.InsertAppParams{ID: idgen.NewUUIDv7(), AccountID: accountID, Name: settings.Name, Slug: slug})
	if err != nil {
		return nil, err
	}
	var tenants []sqlc.Tenant
	for _, env := range envs {
		name := settings.Name
		if env == "sandbox" {
			name += " (sandbox)"
		}
		t, err := q.InsertTenant(ctx, sqlc.InsertTenantParams{
			ID: idgen.NewUUIDv7(), AccountID: accountID, AppID: app.ID, Environment: env, Name: name, Tier: settings.Tier,
			Currency: settings.Currency, Timezone: settings.Timezone, GracePeriod: convert.Interval(settings.Grace),
		})
		if err != nil {
			return nil, err
		}
		tenants = append(tenants, t)
	}
	return convert.App(app, tenants), nil
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// CreateApp adds an application to an account.
func (s *Server) CreateApp(ctx context.Context, req *plansv1.CreateAppRequest) (*plansv1.CreateAppResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	accountID, err := parseID("account_id", req.GetAccountId())
	if err != nil {
		return nil, err
	}
	settings, err := domain.ValidateCreateAccount(&plansv1.CreateAccountRequest{Name: req.GetName(), Currency: req.GetCurrency(), Timezone: req.GetTimezone(), GracePeriod: req.GetGracePeriod(), Tier: req.GetTier()})
	if err != nil {
		return nil, err
	}
	slug := req.GetSlug()
	if slug == "" {
		slug = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(settings.Name), "-"), "-")
	}
	if !slugRe.MatchString(slug) || len(slug) > 60 {
		return nil, errkey.New(codes.InvalidArgument, "plans.app.bad_slug", "slug: lower-case letters, digits and single dashes, at most 60 characters")
	}
	var app *plansv1.App
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetAccount(ctx, accountID); err != nil {
			return err
		}
		var err error
		app, err = s.insertApp(ctx, q, accountID, settings, slug)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		return nil, errkey.New(codes.AlreadyExists, "plans.app.slug_taken", "this account already has an app with that slug")
	}
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	return &plansv1.CreateAppResponse{App: app}, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// ListApps lists the apps of an account with their tenants.
func (s *Server) ListApps(ctx context.Context, req *plansv1.ListAppsRequest) (*plansv1.ListAppsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	accountID, err := parseID("account_id", req.GetAccountId())
	if err != nil {
		return nil, err
	}
	q := s.q()
	apps, err := q.ListAccountApps(ctx, accountID)
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	tenants, err := q.ListAccountTenants(ctx, accountID)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	resp := &plansv1.ListAppsResponse{}
	for _, a := range apps {
		var mine []sqlc.Tenant
		for _, t := range tenants {
			if t.AppID == a.ID {
				mine = append(mine, t)
			}
		}
		resp.Apps = append(resp.Apps, convert.App(a, mine))
	}
	return resp, nil
}

func parseID(field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, domain.Invalid(field, "must be a UUID")
	}
	return id, nil
}

func (s *Server) GetTenant(ctx context.Context, req *plansv1.GetTenantRequest) (*plansv1.GetTenantResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	t, err := s.q().GetTenant(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return &plansv1.GetTenantResponse{Tenant: convert.Tenant(t)}, nil
}

func (s *Server) ListTenants(ctx context.Context, req *plansv1.ListTenantsRequest) (*plansv1.ListTenantsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	size := req.GetPageSize()
	if size <= 0 {
		size = 500
	}
	if size > 1000 {
		return nil, domain.Invalid("page_size", "must be 1..1000")
	}
	after := uuid.Nil
	if tok := req.GetPageToken(); tok != "" {
		var err error
		if after, err = uuid.Parse(tok); err != nil {
			return nil, domain.Invalid("page_token", "invalid")
		}
	}
	rows, err := s.q().ListTenants(ctx, sqlc.ListTenantsParams{After: after, PageSize: size + 1})
	if err != nil {
		return nil, convert.Err(err, "tenants")
	}
	resp := &plansv1.ListTenantsResponse{}
	for i, t := range rows {
		if int32(i) == size { //nolint:gosec // bounded by page size
			resp.NextPageToken = rows[i-1].ID.String()
			break
		}
		resp.Tenants = append(resp.Tenants, convert.Tenant(t))
	}
	return resp, nil
}

func (s *Server) UpdateTenantSettings(ctx context.Context, req *plansv1.UpdateTenantSettingsRequest) (*plansv1.UpdateTenantSettingsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	params := sqlc.UpdateTenantSettingsParams{ID: id}
	if req.GetGracePeriod() != nil {
		g, err := domain.ValidateGrace(req.GetGracePeriod().AsDuration())
		if err != nil {
			return nil, domain.Invalid("grace_period", err.Error())
		}
		params.GracePeriod = convert.Interval(g)
	} else {
		params.GracePeriod = pgtype.Interval{}
	}
	if req.GetTier() != plansv1.TenantTier_TENANT_TIER_UNSPECIFIED {
		name, ok := domain.TierToDB(req.GetTier())
		if !ok {
			return nil, domain.Invalid("tier", "unknown tier")
		}
		params.Tier = &name
	}
	if logo := req.GetInvoiceLogoAssetId(); logo != "" {
		if req.GetClearInvoiceLogo() {
			return nil, domain.Invalid("clear_invoice_logo", "cannot be combined with invoice_logo_asset_id")
		}
		lid, err := uuid.Parse(logo)
		if err != nil {
			return nil, domain.Invalid("invoice_logo_asset_id", "must be a UUID")
		}
		params.InvoiceLogoAssetID = &lid
	}
	params.ClearLogo = req.GetClearInvoiceLogo()
	if req.GetAddress() != nil {
		if err := domain.ValidateAddress("address", req.GetAddress()); err != nil {
			return nil, err
		}
		params.Address = convert.AddressJSON(req.GetAddress())
	}
	if req.GetLegal() != nil {
		if err := domain.ValidateLegal(req.GetLegal()); err != nil {
			return nil, err
		}
		params.Legal = convert.LegalJSON(req.GetLegal())
	}
	if v := req.GetEInvoiceOn(); v != "" {
		if err := domain.ValidateEInvoiceOn(v); err != nil {
			return nil, err
		}
		params.EInvoiceOn = &v
	}
	t, err := s.q().UpdateTenantSettings(ctx, params)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return &plansv1.UpdateTenantSettingsResponse{Tenant: convert.Tenant(t)}, nil
}

func (s *Server) CreateApiKey(ctx context.Context, req *plansv1.CreateApiKeyRequest) (*plansv1.CreateApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	t, err := s.q().GetTenant(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	// The key environment always follows the tenant: test ↔ sandbox, live ↔ live (ADR 0011).
	env := apikey.EnvLive
	if t.Environment == "sandbox" {
		env = apikey.EnvTest
	}
	for attempt := 0; attempt < 3; attempt++ {
		key, err := apikey.Generate(env)
		if err != nil {
			return nil, status.Error(codes.Internal, "key generation failed")
		}
		row, err := s.q().InsertApiKey(ctx, sqlc.InsertApiKeyParams{
			ID: idgen.NewUUIDv7(), TenantID: t.ID, Prefix: key.Prefix, KeyHash: key.Hash(), Env: string(env),
		})
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
			continue // prefix collision (62^8 space): draw again
		}
		if err != nil {
			return nil, convert.Err(err, "api key")
		}
		s.log.InfoContext(ctx, "api key created", "tenant_id", t.ID, "api_key_id", row.ID, "prefix", row.Prefix)
		return &plansv1.CreateApiKeyResponse{ApiKey: convert.APIKey(row), Plaintext: key.String()}, nil
	}
	return nil, status.Error(codes.Aborted, "could not allocate a unique key prefix; retry")
}

func (s *Server) RevokeApiKey(ctx context.Context, req *plansv1.RevokeApiKeyRequest) (*plansv1.RevokeApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("api_key_id", req.GetApiKeyId())
	if err != nil {
		return nil, err
	}
	row, err := s.q().RevokeApiKey(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	// Revocation takes effect immediately by evicting the gateway cache entry. If that fails, the 60 s cache TTL
	// bounds how long the key keeps working.
	if s.evict != nil {
		if err := s.evict.EvictAPIKey(ctx, row.Prefix); err != nil {
			s.log.WarnContext(ctx, "api key cache eviction failed; bounded by cache TTL", "prefix", row.Prefix, "error", err)
		}
	}
	return &plansv1.RevokeApiKeyResponse{ApiKey: convert.APIKey(row)}, nil
}

func (s *Server) ResolveApiKey(ctx context.Context, req *plansv1.ResolveApiKeyRequest) (*plansv1.ResolveApiKeyResponse, error) {
	if len(req.GetPrefix()) != apikey.PrefixLen {
		return nil, domain.Invalid("prefix", "must be 8 characters")
	}
	r, err := s.q().ResolveApiKey(ctx, req.GetPrefix())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "api key not found")
	}
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	return &plansv1.ResolveApiKeyResponse{
		ApiKeyId: r.ID.String(), TenantId: r.TenantID.String(), Status: convert.APIKeyStatus(r.Status), KeyHash: r.KeyHash,
		Environment: domain.EnvironmentFromDB(r.Environment), Tier: domain.TierFromDB(r.Tier), Timezone: r.Timezone,
	}, nil
}
