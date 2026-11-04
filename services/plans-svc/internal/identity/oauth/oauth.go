// Package oauth is the grant, code and token side of the OAuth 2.1 authorization server (docs/plans/20-mcp.md, "OAuth design"; default, not agreed). The gateway has already
// checked the request and the client (a Client ID Metadata Document it fetched); this package keeps the person's consent and issues what follows from it. Codes and tokens
// are stored only as hashes. Whoever a token belongs to must still be a member of the tenant's account when it is presented, and the scopes are cut to their role again
// then, so a person who leaves the account (or is made a viewer) takes the assistant's access with them without any revocation having to run.
package oauth

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/oauth"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Service is the authorization server's state. pool is the system pool.
type Service struct {
	pool *pgxpool.Pool
	qry  *query.Queries
	log  *slog.Logger
	now  func() time.Time
}

// New returns the service.
func New(systemPool *pgxpool.Pool, qry *query.Queries, log *slog.Logger) *Service {
	return &Service{pool: systemPool, qry: qry, log: log, now: time.Now}
}

// invalidGrant is every refusal of a code or a refresh token: unknown, expired, used, for another client, a wrong PKCE verifier. One answer, so it reveals nothing.
func invalidGrant() error {
	return errkey.New(codes.InvalidArgument, "identity.oauth.invalid_grant", "the code or refresh token is invalid, expired or was already used")
}

func invalidToken() error {
	return errkey.New(codes.Unauthenticated, "identity.oauth.invalid_token", "the token is invalid, expired or revoked")
}

// CreateGrant records the person's consent and returns the one-time code. The scopes are cut to the person's role in the tenant's account; a live environment needs the
// deployment's switch and a verified address.
func (s *Service) CreateGrant(ctx context.Context, req *plansv1.CreateOAuthGrantRequest) (*plansv1.CreateOAuthGrantResponse, error) {
	uid, tid := parse(req.GetUserId()), parse(req.GetTenantId())
	if uid == uuid.Nil || tid == uuid.Nil {
		return nil, errkey.New(codes.NotFound, "identity.tenant.not_found", "no such tenant")
	}
	tenant, role, err := s.qry.Tenant(ctx, req.GetUserId(), req.GetTenantId())
	if err != nil {
		return nil, err
	}
	live := tenant.GetEnvironment() == plansv1.Environment_ENVIRONMENT_LIVE
	if live && !req.GetAllowLive() {
		return nil, errkey.New(codes.PermissionDenied, "identity.oauth.live_disabled", "assistants may not act on a live environment on this deployment")
	}
	scopes := oauth.Allowed(role, req.GetScopes())
	if len(scopes) == 0 || !oauth.ValidChallenge(req.GetCodeChallenge()) || !oauth.RedirectURI(req.GetRedirectUri()) || req.GetClientId() == "" || req.GetClientName() == "" {
		return nil, errkey.New(codes.InvalidArgument, "identity.oauth.bad_request", "no scope left for your role, or the request is incomplete")
	}
	resource, ok := oauth.Resource(req.GetResource())
	if !ok {
		return nil, errkey.New(codes.InvalidArgument, "identity.oauth.bad_request", "the resource is not a valid URL")
	}
	u, err := sqlc.New(s.pool).GetUser(ctx, uid)
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	if live && u.EmailVerifiedAt == nil {
		return nil, errkey.New(codes.PermissionDenied, "identity.oauth.live_email_unverified", "verify your e-mail address before an assistant may act on a live environment")
	}
	code, hash, err := oauth.NewSecret("")
	if err != nil {
		return nil, errkey.New(codes.Internal, "identity.oauth.internal", "code generation failed")
	}
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.DeleteStaleOAuth(ctx); err != nil {
			return err
		}
		g, err := q.InsertOAuthGrant(ctx, sqlc.InsertOAuthGrantParams{ID: idgen.NewUUIDv7(), UserID: uid, TenantID: tid, ClientID: req.GetClientId(), ClientName: req.GetClientName(), Resource: resource, Scopes: scopes})
		if err != nil {
			return err
		}
		if err := q.InsertOAuthCode(ctx, sqlc.InsertOAuthCodeParams{CodeHash: hash, GrantID: g.ID, RedirectUri: req.GetRedirectUri(), CodeChallenge: req.GetCodeChallenge(), Ttl: convert.Interval(oauth.CodeTTL)}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: tid, Action: "oauth_grant.created", TargetType: "oauth_grant", TargetID: g.ID.String(),
			Summary: u.Email + " let " + req.GetClientName() + " act on this environment", Detail: map[string]any{"client_id": req.GetClientId(), "scopes": scopes, "resource": resource}})
	})
	if err != nil {
		return nil, convert.Err(err, "oauth grant")
	}
	return &plansv1.CreateOAuthGrantResponse{Code: code, ExpireTime: timestamppb.New(s.now().Add(oauth.CodeTTL)), Scopes: scopes}, nil
}

// ExchangeCode redeems a code. The code is spent by the first try, right or wrong; the client, the redirect URI, the resource and the PKCE verifier must all be those of the
// authorization request. A code presented a second time ends the grant (OAuth 2.1: whoever has the code, one of the two uses is not the client's).
func (s *Service) ExchangeCode(ctx context.Context, req *plansv1.ExchangeOAuthCodeRequest) (*plansv1.OAuthTokens, error) {
	if !oauth.WellFormed(req.GetCode(), "") {
		return nil, invalidGrant()
	}
	hash := oauth.Hash(req.GetCode())
	var out *plansv1.OAuthTokens
	var refused error
	err := pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		c, err := q.SpendOAuthCode(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			if gid, err := q.OAuthCodeSpent(ctx, hash); err == nil {
				if _, err := q.RevokeOAuthGrant(ctx, gid); err != nil {
					return err
				}
				s.log.WarnContext(ctx, "oauth code used twice: grant revoked", "grant_id", gid)
			}
			refused = invalidGrant()
			return nil
		}
		if err != nil {
			return err
		}
		g, err := q.GetOAuthGrant(ctx, c.GrantID)
		if err != nil {
			return err
		}
		resource, _ := oauth.Resource(req.GetResource())
		if g.RevokedAt != nil || g.ClientID != req.GetClientId() || c.RedirectUri != req.GetRedirectUri() || (req.GetResource() != "" && resource != g.Resource) || !oauth.VerifyPKCE(req.GetCodeVerifier(), c.CodeChallenge) {
			refused = invalidGrant()
			return nil
		}
		out, err = s.issuePair(ctx, q, g)
		return err
	})
	switch {
	case err != nil:
		return nil, convert.Err(err, "oauth code")
	case refused != nil:
		return nil, refused
	}
	return out, nil
}

// issuePair makes an access token and a refresh token for the grant.
func (s *Service) issuePair(ctx context.Context, q *sqlc.Queries, g sqlc.OauthGrant) (*plansv1.OAuthTokens, error) {
	access, ah, err := oauth.NewSecret(oauth.AccessPrefix)
	if err != nil {
		return nil, err
	}
	refresh, rh, err := oauth.NewSecret(oauth.RefreshPrefix)
	if err != nil {
		return nil, err
	}
	if err := q.InsertOAuthToken(ctx, sqlc.InsertOAuthTokenParams{TokenHash: ah, GrantID: g.ID, Kind: "access", Ttl: convert.Interval(oauth.AccessTTL)}); err != nil {
		return nil, err
	}
	if err := q.InsertOAuthToken(ctx, sqlc.InsertOAuthTokenParams{TokenHash: rh, GrantID: g.ID, Kind: "refresh", Ttl: convert.Interval(oauth.RefreshTTL)}); err != nil {
		return nil, err
	}
	return &plansv1.OAuthTokens{AccessToken: access, RefreshToken: refresh, ExpiresIn: durationpb.New(oauth.AccessTTL), Scopes: g.Scopes}, nil
}

// Refresh replaces a refresh token with a new pair; the old one is spent. One that was spent already ends the grant: a client that kept using the old one is either broken or
// has had the token stolen, and either way the person is asked again. The person must still be a member of the tenant's account.
func (s *Service) Refresh(ctx context.Context, req *plansv1.RefreshOAuthTokenRequest) (*plansv1.OAuthTokens, error) {
	if !oauth.WellFormed(req.GetRefreshToken(), oauth.RefreshPrefix) {
		return nil, invalidGrant()
	}
	hash := oauth.Hash(req.GetRefreshToken())
	var out *plansv1.OAuthTokens
	var refused error
	err := pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		gid, err := q.SpendRefreshToken(ctx, hash)
		if errors.Is(err, pgx.ErrNoRows) {
			if gid, err := q.RefreshTokenSpent(ctx, hash); err == nil {
				if _, err := q.RevokeOAuthGrant(ctx, gid); err != nil {
					return err
				}
				s.log.WarnContext(ctx, "oauth refresh token used twice: grant revoked", "grant_id", gid)
			}
			refused = invalidGrant()
			return nil
		}
		if err != nil {
			return err
		}
		g, err := q.GetOAuthGrant(ctx, gid)
		if err != nil {
			return err
		}
		resource, _ := oauth.Resource(req.GetResource())
		if g.ClientID != req.GetClientId() || (req.GetResource() != "" && resource != g.Resource) {
			refused = invalidGrant()
			return nil
		}
		if _, _, err := s.qry.Tenant(ctx, g.UserID.String(), g.TenantID.String()); err != nil {
			refused = invalidGrant()
			return nil
		}
		out, err = s.issuePair(ctx, q, g)
		return err
	})
	switch {
	case err != nil:
		return nil, convert.Err(err, "oauth token")
	case refused != nil:
		return nil, refused
	}
	return out, nil
}

// Revoke ends the grant the token belongs to, if it was issued to client (RFC 7009: the answer is the same for a token it does not know).
func (s *Service) Revoke(ctx context.Context, req *plansv1.RevokeOAuthTokenRequest) error {
	if !oauth.WellFormed(req.GetToken(), oauth.AccessPrefix) && !oauth.WellFormed(req.GetToken(), oauth.RefreshPrefix) {
		return nil
	}
	err := pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := sqlc.New(tx).RevokeOAuthGrantOfToken(ctx, sqlc.RevokeOAuthGrantOfTokenParams{TokenHash: oauth.Hash(req.GetToken()), ClientID: req.GetClientId()})
		return err
	})
	return convert.Err(err, "oauth token")
}

// inspect resolves a token of kind to its grant, the person's role now and the scopes that role allows now. Anything wrong is an inactive token.
func (s *Service) inspect(ctx context.Context, token, prefix, kind string) (*plansv1.OAuthTokenInfo, error) {
	if !oauth.WellFormed(token, prefix) {
		return &plansv1.OAuthTokenInfo{}, nil
	}
	row, err := sqlc.New(s.pool).GetOAuthToken(ctx, oauth.Hash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return &plansv1.OAuthTokenInfo{}, nil
	}
	if err != nil {
		return nil, convert.Err(err, "oauth token")
	}
	if !row.Live || row.Kind != kind {
		return &plansv1.OAuthTokenInfo{}, nil
	}
	tenant, role, err := s.qry.Tenant(ctx, row.UserID.String(), row.TenantID.String())
	if err != nil {
		if errkey.Of(err) == "identity.tenant.not_found" {
			return &plansv1.OAuthTokenInfo{}, nil // left the account, or the app was deleted
		}
		return nil, err
	}
	scopes := oauth.Allowed(role, row.Scopes)
	if len(scopes) == 0 {
		return &plansv1.OAuthTokenInfo{}, nil
	}
	return &plansv1.OAuthTokenInfo{Active: true, GrantId: row.GrantID.String(), UserId: row.UserID.String(), UserEmail: row.Email, EmailVerified: row.EmailVerifiedAt != nil, Tenant: tenant,
		MemberRole: role, Scopes: scopes, ClientId: row.ClientID, ClientName: row.ClientName, Resource: row.Resource, ExpireTime: timestamppb.New(row.ExpiresAt)}, nil
}

// Introspect is RFC 7662 for an access token that was issued for resource.
func (s *Service) Introspect(ctx context.Context, req *plansv1.IntrospectOAuthTokenRequest) (*plansv1.OAuthTokenInfo, error) {
	info, err := s.inspect(ctx, req.GetToken(), oauth.AccessPrefix, "access")
	if err != nil || !info.GetActive() {
		return &plansv1.OAuthTokenInfo{}, err
	}
	if resource, ok := oauth.Resource(req.GetResource()); !ok || resource != info.GetResource() {
		return &plansv1.OAuthTokenInfo{}, nil // a token issued for another resource is not this resource's
	}
	return info, nil
}

// MintAPIToken exchanges a valid access token for the credential the MCP server uses on the tenant API: valid for minutes and never longer than the access token.
func (s *Service) MintAPIToken(ctx context.Context, req *plansv1.MintOAuthApiTokenRequest) (*plansv1.MintOAuthApiTokenResponse, error) {
	info, err := s.Introspect(ctx, &plansv1.IntrospectOAuthTokenRequest{Token: req.GetAccessToken(), Resource: req.GetResource()})
	if err != nil {
		return nil, err
	}
	if !info.GetActive() {
		return nil, invalidToken()
	}
	ttl := min(oauth.APITTL, info.GetExpireTime().AsTime().Sub(s.now()))
	if ttl <= 0 {
		return nil, invalidToken()
	}
	token, hash, err := oauth.NewSecret(oauth.APIPrefix)
	if err != nil {
		return nil, errkey.New(codes.Internal, "identity.oauth.internal", "token generation failed")
	}
	gid := parse(info.GetGrantId())
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.TouchOAuthGrant(ctx, gid); err != nil {
			return err
		}
		return q.InsertOAuthToken(ctx, sqlc.InsertOAuthTokenParams{TokenHash: hash, GrantID: gid, Kind: "api", Ttl: convert.Interval(ttl)})
	})
	if err != nil {
		return nil, convert.Err(err, "oauth token")
	}
	return &plansv1.MintOAuthApiTokenResponse{Token: token, ExpireTime: timestamppb.New(s.now().Add(ttl)), Info: info}, nil
}

// ResolveAPIToken is what the gateway asks to accept a qm_ credential: who, which tenant, which scopes.
func (s *Service) ResolveAPIToken(ctx context.Context, req *plansv1.ResolveOAuthApiTokenRequest) (*plansv1.OAuthTokenInfo, error) {
	return s.inspect(ctx, req.GetToken(), oauth.APIPrefix, "api")
}

// Grants lists the person's live grants, newest first.
func (s *Service) Grants(ctx context.Context, userID string) ([]*plansv1.OAuthGrant, error) {
	uid := parse(userID)
	rows, err := sqlc.New(s.pool).ListUserOAuthGrants(ctx, uid)
	if err != nil {
		return nil, convert.Err(err, "oauth grant")
	}
	out := make([]*plansv1.OAuthGrant, 0, len(rows))
	for _, r := range rows {
		env := plansv1.Environment_ENVIRONMENT_LIVE
		if r.Environment == "sandbox" {
			env = plansv1.Environment_ENVIRONMENT_SANDBOX
		}
		g := &plansv1.OAuthGrant{Id: r.ID.String(), ClientId: r.ClientID, ClientName: r.ClientName, TenantId: r.TenantID.String(), AppName: r.AppName, Environment: env, Resource: r.Resource,
			Scopes: slices.Clone(r.Scopes), CreateTime: timestamppb.New(r.CreatedAt)}
		if r.LastUsedAt != nil {
			g.LastUsedTime = timestamppb.New(*r.LastUsedAt)
		}
		out = append(out, g)
	}
	return out, nil
}

// RevokeGrant ends one of the person's grants: every token of it stops at once.
func (s *Service) RevokeGrant(ctx context.Context, userID, grantID string) error {
	uid, gid := parse(userID), parse(grantID)
	return convert.Err(pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		g, err := sqlc.New(tx).RevokeUserOAuthGrant(ctx, sqlc.RevokeUserOAuthGrantParams{ID: gid, UserID: uid})
		if errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.NotFound, "identity.oauth.grant_not_found", "no such connection")
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: g.TenantID, Action: "oauth_grant.revoked", TargetType: "oauth_grant", TargetID: gid.String(), Summary: "ended the access of " + g.ClientName})
	}), "oauth grant")
}

func parse(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}
