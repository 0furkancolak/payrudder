// Package query reads people: sessions, memberships, and the accounts, apps, tenants and keys a user may see. It never writes.
// Every account-scoped read takes the user id and finds a row only through that user's membership, in SQL: another account's app is "not found".
package query

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Roles of a member.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
	// RoleAffiliate (phase 47) belongs to a partner: it sees the partner portal and nothing of the account. Account-wide reads in SQL leave it out.
	RoleAffiliate = "affiliate"
)

// CanManage reports whether the role may create apps and keys.
func CanManage(role string) bool { return role == RoleOwner || role == RoleAdmin }

// Session timing: the expiry slides 14 days from the last use (written at most every SlideEvery), and no session lives longer than MaxLifetime.
const (
	SessionTTL  = 14 * 24 * time.Hour
	SlideEvery  = 5 * time.Minute
	MaxLifetime = 90 * 24 * time.Hour
)

// Queries reads on the system pool.
type Queries struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns the identity queries.
func New(systemPool *pgxpool.Pool) *Queries { return &Queries{pool: systemPool, now: time.Now} }

func (q *Queries) db() *sqlc.Queries { return sqlc.New(q.pool) }

// ErrNoSession is what every bad token is: malformed, unknown, revoked or expired. One error, so a caller learns nothing about which.
func ErrNoSession() error {
	return errkey.New(codes.Unauthenticated, "identity.session.invalid", "sign in again")
}

// Session is a resolved session token.
type Session struct {
	ID          uuid.UUID
	User        *plansv1.User
	Memberships []*plansv1.Membership
	ExpireTime  time.Time
	// Slide is set when the expiry should be moved on (the session was last used a while ago); TouchSession does it.
	Slide bool
	// NewExpire is the expiry to slide to.
	NewExpire time.Time
}

// Session resolves a session token.
func (q *Queries) Session(ctx context.Context, token string) (Session, error) {
	if !credential.WellFormedToken(token) {
		return Session{}, ErrNoSession()
	}
	db := q.db()
	row, err := db.GetLiveSession(ctx, credential.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNoSession()
	}
	if err != nil {
		return Session{}, convert.Err(err, "session")
	}
	ms, err := Memberships(ctx, db, row.UserID)
	if err != nil {
		return Session{}, err
	}
	now := q.now()
	s := Session{
		ID: row.ID,
		User: &plansv1.User{Id: row.UserID.String(), Email: row.Email, DisplayName: row.DisplayName, CreateTime: timestamppb.New(row.UserCreatedAt),
			EmailVerified: row.EmailVerifiedAt != nil},
		Memberships: ms,
		ExpireTime:  row.ExpiresAt,
	}
	if now.Sub(row.LastSeenAt) >= SlideEvery {
		s.Slide, s.NewExpire = true, SlideTo(now, row.CreatedAt)
		s.ExpireTime = s.NewExpire
	}
	return s, nil
}

// SlideTo is the expiry of a session used at now that was created at created: 14 days on, but never past the absolute lifetime.
func SlideTo(now, created time.Time) time.Time {
	e := now.Add(SessionTTL)
	if limit := created.Add(MaxLifetime); e.After(limit) {
		return limit
	}
	return e
}

// Memberships lists the accounts a user belongs to.
func Memberships(ctx context.Context, db *sqlc.Queries, userID uuid.UUID) ([]*plansv1.Membership, error) {
	rows, err := db.ListUserMemberships(ctx, userID)
	if err != nil {
		return nil, convert.Err(err, "membership")
	}
	out := make([]*plansv1.Membership, 0, len(rows))
	for _, r := range rows {
		out = append(out, &plansv1.Membership{AccountId: r.AccountID.String(), AccountName: r.AccountName, Role: r.Role})
	}
	return out, nil
}

// User is the API shape of a user row.
func User(u sqlc.User) *plansv1.User {
	return &plansv1.User{Id: u.ID.String(), Email: u.Email, DisplayName: u.DisplayName, CreateTime: timestamppb.New(u.CreatedAt), EmailVerified: u.EmailVerifiedAt != nil}
}

// ParseID parses an id from a request; a malformed one finds nothing, like an unknown one.
func ParseID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	return id, err == nil
}

// NotFound is the error for an object that does not exist or is not the caller's: the same answer for both.
func NotFound(what string) error {
	switch what {
	case "account":
		return errkey.New(codes.NotFound, "identity.account.not_found", "no such account")
	case "app":
		return errkey.New(codes.NotFound, "identity.app.not_found", "no such app")
	case "tenant":
		return errkey.New(codes.NotFound, "identity.tenant.not_found", "no such tenant")
	default:
		return errkey.New(codes.NotFound, "identity.api_key.not_found", "no such api key")
	}
}

// Membership is the caller's membership of an account, in the queries db (a transaction, for the commands): the ids parsed and the row with the caller's role
// there. A caller who is not a member of the account, or an id that is not an id, finds no account.
func Membership(ctx context.Context, db *sqlc.Queries, userID, accountID string) (uid, aid uuid.UUID, m sqlc.GetMembershipRow, err error) {
	uid, ok1 := ParseID(userID)
	aid, ok2 := ParseID(accountID)
	if !ok1 || !ok2 {
		return uid, aid, m, NotFound("account")
	}
	m, err = db.GetMembership(ctx, sqlc.GetMembershipParams{AccountID: aid, UserID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return uid, aid, m, NotFound("account")
	}
	if err != nil {
		return uid, aid, m, convert.Err(err, "account")
	}
	return uid, aid, m, nil
}

// Forbidden is the answer to a member whose role does not allow what they asked.
func Forbidden() error {
	return errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account does not allow this")
}

// RequireManager is Membership for what only an owner or an administrator may do: any other role is told so.
func RequireManager(ctx context.Context, db *sqlc.Queries, userID, accountID string) (uid, aid uuid.UUID, m sqlc.GetMembershipRow, err error) {
	uid, aid, m, err = Membership(ctx, db, userID, accountID)
	if err == nil && !CanManage(m.Role) {
		err = Forbidden()
	}
	return uid, aid, m, err
}

// Account returns the account of a member with its member list.
func (q *Queries) Account(ctx context.Context, userID, accountID string) (*plansv1.GetMyAccountResponse, error) {
	db := q.db()
	_, aid, m, err := Membership(ctx, db, userID, accountID)
	if err != nil {
		return nil, err
	}
	rows, err := db.ListAccountMembers(ctx, aid)
	if err != nil {
		return nil, convert.Err(err, "member")
	}
	resp := &plansv1.GetMyAccountResponse{Id: aid.String(), Name: m.AccountName, Role: m.Role, CreateTime: timestamppb.New(m.CreatedAt)}
	for _, r := range rows {
		resp.Members = append(resp.Members, &plansv1.AccountMember{
			UserId: r.UserID.String(), Email: r.Email, DisplayName: r.DisplayName, Role: r.Role, CreateTime: timestamppb.New(r.CreatedAt)})
	}
	return resp, nil
}

// Apps lists the apps of an account the user is a member of, with their tenants.
func (q *Queries) Apps(ctx context.Context, userID, accountID string) ([]*plansv1.App, error) {
	db := q.db()
	_, aid, _, err := Membership(ctx, db, userID, accountID)
	if err != nil {
		return nil, err
	}
	apps, err := db.ListAccountApps(ctx, aid)
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	tenants, err := db.ListAccountTenants(ctx, aid)
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return convert.Apps(apps, tenants), nil
}

// App returns an app of one of the user's accounts with its tenants, and the user's role there.
func (q *Queries) App(ctx context.Context, userID, appID string) (*plansv1.App, string, error) {
	return AppOf(ctx, q.db(), userID, appID)
}

// AppOf is App on a given set of queries (a transaction, for the commands).
func AppOf(ctx context.Context, db *sqlc.Queries, userID, appID string) (*plansv1.App, string, error) {
	uid, ok1 := ParseID(userID)
	aid, ok2 := ParseID(appID)
	if !ok1 || !ok2 {
		return nil, "", NotFound("app")
	}
	row, err := db.GetAppForUser(ctx, sqlc.GetAppForUserParams{UserID: uid, AppID: aid})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", NotFound("app")
	}
	if err != nil {
		return nil, "", convert.Err(err, "app")
	}
	tenants, err := db.ListAppTenants(ctx, row.ID)
	if err != nil {
		return nil, "", convert.Err(err, "tenant")
	}
	return convert.App(sqlc.App{ID: row.ID, AccountID: row.AccountID, Name: row.Name, Slug: row.Slug, CreatedAt: row.CreatedAt}, tenants), row.Role, nil
}

// Tenant returns a tenant of one of the user's accounts and the user's role there. It is what authorises a session to act on a tenant.
func (q *Queries) Tenant(ctx context.Context, userID, tenantID string) (*plansv1.Tenant, string, error) {
	uid, ok1 := ParseID(userID)
	tid, ok2 := ParseID(tenantID)
	if !ok1 || !ok2 {
		return nil, "", NotFound("tenant")
	}
	row, err := q.db().GetTenantForUser(ctx, sqlc.GetTenantForUserParams{UserID: uid, TenantID: tid})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", NotFound("tenant")
	}
	if err != nil {
		return nil, "", convert.Err(err, "tenant")
	}
	return convert.Tenant(sqlc.Tenant{
		ID: row.ID, AccountID: row.AccountID, Environment: row.Environment, Name: row.Name, Tier: row.Tier, Currency: row.Currency, Timezone: row.Timezone,
		GracePeriod: row.GracePeriod, CreatedAt: row.CreatedAt, InvoiceLogoAssetID: row.InvoiceLogoAssetID, AppID: row.AppID, Address: row.Address,
		BankTransfer: row.BankTransfer, Legal: row.Legal, EInvoiceOn: row.EInvoiceOn, Status: row.Status, ActivatedAt: row.ActivatedAt,
		TermsAcceptedAt: row.TermsAcceptedAt, TermsAcceptedBy: row.TermsAcceptedBy,
	}), row.Role, nil
}

// APIKeys lists the keys of both environments of an app, newest first.
func (q *Queries) APIKeys(ctx context.Context, userID, appID string) ([]*plansv1.ApiKey, error) {
	db := q.db()
	app, _, err := AppOf(ctx, db, userID, appID)
	if err != nil {
		return nil, err
	}
	return KeysOf(ctx, db, app)
}

// KeysOf lists the keys of the tenants of app.
func KeysOf(ctx context.Context, db *sqlc.Queries, app *plansv1.App) ([]*plansv1.ApiKey, error) {
	ids := make([]uuid.UUID, 0, len(app.GetTenants()))
	for _, t := range app.GetTenants() {
		if id, err := uuid.Parse(t.GetId()); err == nil {
			ids = append(ids, id)
		}
	}
	rows, err := db.ListApiKeysOfTenants(ctx, ids)
	if err != nil {
		return nil, convert.Err(err, "api key")
	}
	out := make([]*plansv1.ApiKey, 0, len(rows))
	for _, r := range rows {
		out = append(out, convert.APIKey(r))
	}
	return out, nil
}
