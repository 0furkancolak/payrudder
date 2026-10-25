// Package admin implements AdminService. Its operations are cross-tenant by nature (creating accounts, resolving keys),
// so it runs on the system pool (role plans_system, BYPASSRLS). It is the only request-handling package allowed to
// hold that pool; access is restricted to operator calls, except ResolveApiKey (gateway only).
package admin

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/env"
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
		app, err := s.InsertApp(ctx, q, a.ID, settings, defaultSlug)
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

// InsertApp makes an app of the account with its sandbox tenant and, when the deployment allows live, its live tenant.
func (s *Server) InsertApp(ctx context.Context, q *sqlc.Queries, accountID uuid.UUID, settings domain.AccountSettings, slug string) (*plansv1.App, error) {
	envs := []env.Env{env.Sandbox}
	if env.Allowed(s.allowedEnvs, env.Live) {
		envs = append(envs, env.Live)
	}
	app, err := q.InsertApp(ctx, sqlc.InsertAppParams{ID: idgen.NewUUIDv7(), AccountID: accountID, Name: settings.Name, Slug: slug})
	if err != nil {
		return nil, err
	}
	var tenants []sqlc.Tenant
	for _, e := range envs {
		name := settings.Name
		// Live is earned (phase 45): a live tenant starts inactive, with no keys, until its go-live checklist is complete.
		tenantStatus := domain.StatusActive
		if e == env.Sandbox {
			name += " (sandbox)"
		} else {
			tenantStatus = domain.StatusInactive
		}
		t, err := q.InsertTenant(ctx, sqlc.InsertTenantParams{
			ID: idgen.NewUUIDv7(), AccountID: accountID, AppID: app.ID, Environment: e.String(), Name: name, Tier: settings.Tier,
			Currency: settings.Currency, Timezone: settings.Timezone, GracePeriod: convert.Interval(settings.Grace), Status: tenantStatus,
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
	slug, err := ResolveSlug(settings.Name, req.GetSlug())
	if err != nil {
		return nil, err
	}
	var app *plansv1.App
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetAccount(ctx, accountID); err != nil {
			return err
		}
		var err error
		app, err = s.InsertApp(ctx, q, accountID, settings, slug)
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

// ResolveSlug returns the slug an app is stored under: slug itself, or the name in lower case with dashes when slug is empty.
func ResolveSlug(name, slug string) (string, error) {
	if slug == "" {
		slug = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	}
	if !slugRe.MatchString(slug) || len(slug) > 60 {
		return "", errkey.New(codes.InvalidArgument, "plans.app.bad_slug", "slug: lower-case letters, digits and single dashes, at most 60 characters")
	}
	return slug, nil
}

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
	if d := req.GetBankTransfer(); d != nil {
		if req.GetClearBankTransfer() {
			return nil, domain.Invalid("clear_bank_transfer", "cannot be combined with bank_transfer")
		}
		if err := domain.ValidateBankTransfer("bank_transfer", d); err != nil {
			return nil, err
		}
		params.BankTransfer = convert.BankTransferJSON(d)
	}
	params.ClearBankTransfer = req.GetClearBankTransfer()
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
	scope, ok := domain.ScopeToDB(req.GetScope())
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "plans.api_key.bad_scope", "scope: full, ingest or read")
	}
	key, plaintext, err := s.IssueAPIKey(ctx, id, scope)
	if err != nil {
		return nil, err
	}
	return &plansv1.CreateApiKeyResponse{ApiKey: key, Plaintext: plaintext}, nil
}

// IssueAPIKey makes a key of the given scope (domain.ScopeFull, ScopeIngest or ScopeRead) for the tenant and returns it with its plaintext, which is shown
// once. It does no authorisation: the operator RPC and the identity module (a member of the tenant's account) each check theirs before calling it.
// A live tenant that is not active has no keys (the go-live gate), and neither has a suspended one.
func (s *Server) IssueAPIKey(ctx context.Context, tenantID uuid.UUID, scope string) (*plansv1.ApiKey, string, error) {
	t, err := s.q().GetTenant(ctx, tenantID)
	if err != nil {
		return nil, "", convert.Err(err, "tenant")
	}
	if t.Status != domain.StatusActive {
		return nil, "", errkey.New(codes.FailedPrecondition, "plans.api_key.tenant_not_active", "this environment is not active: finish the go-live checklist first")
	}
	// The key environment always follows the tenant: test <-> sandbox, live <-> live (ADR 0011).
	tenantEnv, err := env.Parse(t.Environment)
	if err != nil {
		return nil, "", status.Error(codes.Internal, "tenant has no environment")
	}
	keyEnv := tenantEnv.ForKey()
	for attempt := 0; attempt < 3; attempt++ {
		key, err := apikey.Generate(keyEnv)
		if err != nil {
			return nil, "", status.Error(codes.Internal, "key generation failed")
		}
		row, err := s.q().InsertApiKey(ctx, sqlc.InsertApiKeyParams{
			ID: idgen.NewUUIDv7(), TenantID: t.ID, Prefix: key.Prefix, KeyHash: key.Hash(), Env: string(keyEnv), Scope: scope,
		})
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
			continue // prefix collision (62^8 space): draw again
		}
		if err != nil {
			return nil, "", convert.Err(err, "api key")
		}
		s.log.InfoContext(ctx, "api key created", "tenant_id", t.ID, "api_key_id", row.ID, "prefix", row.Prefix, "scope", scope)
		return convert.APIKey(row), key.String(), nil
	}
	return nil, "", errkey.New(codes.Aborted, "identity.api_key.prefix_unavailable", "could not allocate a unique key prefix; retry")
}

func (s *Server) RevokeApiKey(ctx context.Context, req *plansv1.RevokeApiKeyRequest) (*plansv1.RevokeApiKeyResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("api_key_id", req.GetApiKeyId())
	if err != nil {
		return nil, err
	}
	key, err := s.RevokeKey(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.RevokeApiKeyResponse{ApiKey: key}, nil
}

// RevokeKey revokes the key and evicts it from the gateway cache. Like IssueAPIKey it does no authorisation.
func (s *Server) RevokeKey(ctx context.Context, id uuid.UUID) (*plansv1.ApiKey, error) {
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
	return convert.APIKey(row), nil
}

// ExpireKey makes an active key stop working at until without revoking it: the old key of a rotation. It never extends a key that already ends sooner.
// The gateway checks the time itself on every request, so no cache has to be told.
func (s *Server) ExpireKey(ctx context.Context, id uuid.UUID, until time.Time) (*plansv1.ApiKey, error) {
	row, err := s.q().ExpireApiKey(ctx, sqlc.ExpireApiKeyParams{ID: id, ExpiresAt: until})
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	if s.evict != nil {
		// The cached entry holds the old end time (none): drop it so the next request reads the new one.
		if err := s.evict.EvictAPIKey(ctx, row.Prefix); err != nil {
			s.log.WarnContext(ctx, "api key cache eviction failed; bounded by cache TTL", "prefix", row.Prefix, "error", err)
		}
	}
	return convert.APIKey(row), nil
}

// TouchApiKey records that a key was just used. A key that does not exist is not an error: it was revoked and deleted, or the call is late.
func (s *Server) TouchApiKey(ctx context.Context, req *plansv1.TouchApiKeyRequest) (*plansv1.TouchApiKeyResponse, error) {
	id, err := uuid.Parse(req.GetApiKeyId())
	if err != nil {
		return nil, domain.Invalid("api_key_id", "must be a UUID")
	}
	if err := s.q().TouchApiKey(ctx, id); err != nil {
		return nil, convert.Err(err, "api key")
	}
	return &plansv1.TouchApiKeyResponse{}, nil
}

// SetTenantStatus is the operator's hand on a tenant's life. Suspending evicts every key of the tenant from the gateway cache at once.
func (s *Server) SetTenantStatus(ctx context.Context, req *plansv1.SetTenantStatusRequest) (*plansv1.SetTenantStatusResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := parseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	want, ok := domain.StatusToDB(req.GetStatus())
	if !ok || want == domain.StatusInactive {
		return nil, errkey.New(codes.InvalidArgument, "plans.tenant.bad_status", "status: active or suspended")
	}
	var out sqlc.Tenant
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.LockTenantForGoLive(ctx, id)
		if err != nil {
			return err
		}
		switch {
		case cur.Environment != env.Live.String():
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "a sandbox tenant is always active")
		case cur.Status == want:
			out = cur
			return nil
		case want == domain.StatusSuspended && cur.Status != domain.StatusActive:
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "only an active tenant can be suspended")
		case want == domain.StatusActive && cur.Status == domain.StatusInactive && strings.TrimSpace(req.GetReason()) == "":
			return errkey.New(codes.InvalidArgument, "plans.tenant.reason_required", "activating a tenant without its checklist needs a reason")
		}
		out, err = q.SetTenantStatus(ctx, sqlc.SetTenantStatusParams{ID: id, Status: want})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	s.log.WarnContext(ctx, "tenant status set by an operator", "tenant_id", id, "status", want, "reason", req.GetReason())
	if want == domain.StatusSuspended && s.evict != nil {
		prefixes, err := s.q().ListTenantApiKeyPrefixes(ctx, id)
		if err != nil {
			return nil, convert.Err(err, "api key")
		}
		for _, p := range prefixes {
			if err := s.evict.EvictAPIKey(ctx, p); err != nil {
				s.log.WarnContext(ctx, "api key cache eviction failed; bounded by cache TTL", "prefix", p, "error", err)
			}
		}
	}
	return &plansv1.SetTenantStatusResponse{Tenant: convert.Tenant(out)}, nil
}

func (s *Server) ResolveApiKey(ctx context.Context, req *plansv1.ResolveApiKeyRequest) (*plansv1.ResolveApiKeyResponse, error) {
	if len(req.GetPrefix()) != apikey.PrefixLen {
		return nil, domain.Invalid("prefix", "must be 8 characters")
	}
	r, err := s.q().ResolveApiKey(ctx, req.GetPrefix())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.NotFound, "identity.api_key.not_found", "api key not found")
	}
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	return &plansv1.ResolveApiKeyResponse{
		ApiKeyId: r.ID.String(), TenantId: r.TenantID.String(), Status: convert.APIKeyStatus(r.Status), KeyHash: r.KeyHash,
		Environment: domain.EnvironmentFromDB(r.Environment), Tier: domain.TierFromDB(r.Tier), Timezone: r.Timezone,
		TenantStatus: domain.StatusFromDB(r.TenantStatus), Scope: domain.ScopeFromDB(r.Scope), ExpireTime: expireTime(r.ExpiresAt),
	}, nil
}

func expireTime(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}
