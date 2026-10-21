// Package command changes people: it signs users up and in and out, and creates the apps and keys a member of an account manages.
// Rules live here; the handler only maps messages. Secrets (passwords, session tokens, API key plaintexts) are never written to a log.
package command

import (
	"context"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/admin"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Sign-in throttling: five wrong passwords lock the account for fifteen minutes. The lock is per user; the gateway also limits sign-in attempts per address.
const (
	MaxFailures = 5
	LockFor     = 15 * time.Minute
	// MaxAppsPerAccount bounds self-serve app creation; the operator creates more through the admin API.
	MaxAppsPerAccount = 20
	// MaxActiveKeysPerTenant bounds the live API keys of one environment.
	MaxActiveKeysPerTenant = 10
	// HashSlots is how many argon2 hashes may run at once (19 MiB each).
	HashSlots = 4
)

// AppFactory creates an app with its tenants inside the caller's transaction (admin.Server).
type AppFactory interface {
	InsertApp(ctx context.Context, q *sqlc.Queries, accountID uuid.UUID, settings domain.AccountSettings, slug string) (*plansv1.App, error)
}

// KeyAdmin issues and revokes API keys without the operator check (admin.Server); this package does the membership check first.
type KeyAdmin interface {
	IssueAPIKey(ctx context.Context, tenantID uuid.UUID) (*plansv1.ApiKey, string, error)
	RevokeKey(ctx context.Context, id uuid.UUID) (*plansv1.ApiKey, error)
}

// Commands changes people.
type Commands struct {
	pool  *pgxpool.Pool
	apps  AppFactory
	keys  KeyAdmin
	log   *slog.Logger
	slots chan struct{}
	now   func() time.Time
}

// New returns the identity commands on the system pool.
func New(systemPool *pgxpool.Pool, apps AppFactory, keys KeyAdmin, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, apps: apps, keys: keys, log: log, slots: make(chan struct{}, HashSlots), now: time.Now}
}

// invalidCredentials is every sign-in failure: unknown e-mail, wrong password, locked account. One answer, so it reveals nothing.
func invalidCredentials() error {
	return errkey.New(codes.Unauthenticated, "identity.auth.invalid_credentials", "wrong e-mail or password, or too many attempts: try again later")
}

// withSlot runs fn holding one of the hash slots, so a burst of sign-ins cannot allocate dozens of 19 MiB hashes at once.
func (c *Commands) withSlot(ctx context.Context, fn func()) error {
	select {
	case c.slots <- struct{}{}:
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
	defer func() { <-c.slots }()
	fn()
	return nil
}

// NormalizeEmail trims and lower-cases an address.
func NormalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func validEmail(s string) bool {
	if len(s) > 254 || strings.ContainsAny(s, " \t\r\n<>,;") {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}
	at := strings.LastIndexByte(s, '@')
	return at > 0 && strings.Contains(s[at+1:], ".") && !strings.HasSuffix(s, ".")
}

// Opened is a new session.
type Opened struct {
	User        *plansv1.User
	Memberships []*plansv1.Membership
	Token       string
	ExpireTime  time.Time
}

// SignUp creates the user, the account and the owner membership in one transaction, and opens a session.
func (c *Commands) SignUp(ctx context.Context, req *plansv1.SignUpRequest) (Opened, error) {
	email := NormalizeEmail(req.GetEmail())
	name := strings.TrimSpace(req.GetAccountName())
	display := strings.TrimSpace(req.GetDisplayName())
	var v domain.Violations
	if !validEmail(email) {
		v.Add("email", "must be a plain e-mail address")
	}
	if n := len(req.GetPassword()); n < credential.MinPasswordLen || n > credential.MaxPasswordLen {
		v.Add("password", "must be between 10 and 256 characters")
	}
	if name == "" || len(name) > 200 {
		v.Add("account_name", "required, at most 200 characters")
	}
	if len(display) > 200 {
		v.Add("display_name", "at most 200 characters")
	}
	if err := v.Err(); err != nil {
		return Opened{}, err
	}
	var hash string
	var herr error
	if err := c.withSlot(ctx, func() { hash, herr = credential.Hash(req.GetPassword()) }); err != nil {
		return Opened{}, err
	}
	if herr != nil {
		return Opened{}, status.Error(codes.Internal, "password hashing failed")
	}
	token, tokenHash, err := credential.NewToken()
	if err != nil {
		return Opened{}, status.Error(codes.Internal, "token generation failed")
	}
	var out Opened
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		u, err := q.InsertUser(ctx, sqlc.InsertUserParams{ID: idgen.NewUUIDv7(), Email: email, DisplayName: display, PasswordHash: hash})
		if err != nil {
			return err
		}
		a, err := q.InsertAccount(ctx, sqlc.InsertAccountParams{ID: idgen.NewUUIDv7(), Name: name})
		if err != nil {
			return err
		}
		if err := q.InsertMember(ctx, sqlc.InsertMemberParams{AccountID: a.ID, UserID: u.ID, Role: query.RoleOwner}); err != nil {
			return err
		}
		now := c.now()
		expires := query.SlideTo(now, now)
		if _, err := q.InsertSession(ctx, sqlc.InsertSessionParams{ID: idgen.NewUUIDv7(), UserID: u.ID, TokenHash: tokenHash, ExpiresAt: expires}); err != nil {
			return err
		}
		out = Opened{User: query.User(u), Token: token, ExpireTime: expires,
			Memberships: []*plansv1.Membership{{AccountId: a.ID.String(), AccountName: a.Name, Role: query.RoleOwner}}}
		return nil
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		return Opened{}, errkey.New(codes.AlreadyExists, "identity.signup.email_taken", "an account with this e-mail already exists")
	}
	if err != nil {
		return Opened{}, convert.Err(err, "user")
	}
	c.log.InfoContext(ctx, "user signed up", "user_id", out.User.GetId(), "account_id", out.Memberships[0].GetAccountId())
	return out, nil
}

// SignIn checks the password and opens a session. A failure never says which part was wrong.
func (c *Commands) SignIn(ctx context.Context, req *plansv1.SignInRequest) (Opened, error) {
	email := NormalizeEmail(req.GetEmail())
	password := req.GetPassword()
	if email == "" || password == "" {
		return Opened{}, domainInvalid("email", "e-mail and password are required")
	}
	if len(password) > credential.MaxPasswordLen || len(email) > 254 {
		return Opened{}, invalidCredentials()
	}
	db := sqlc.New(c.pool)
	user, err := db.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, c.burn(ctx, password)
	}
	if err != nil {
		return Opened{}, convert.Err(err, "user")
	}
	if user.LockedUntil != nil && user.LockedUntil.After(c.now()) {
		// Locked: do the work of a verify anyway, so the time does not tell, and do not count the attempt (an attacker could extend the lock forever).
		return Opened{}, c.burn(ctx, password)
	}
	var ok bool
	var verr error
	if err := c.withSlot(ctx, func() { ok, verr = credential.Verify(password, user.PasswordHash) }); err != nil {
		return Opened{}, err
	}
	if verr != nil {
		c.log.ErrorContext(ctx, "stored password hash unreadable", "user_id", user.ID)
		return Opened{}, invalidCredentials()
	}
	if !ok {
		r, err := db.RecordFailedLogin(ctx, sqlc.RecordFailedLoginParams{ID: user.ID, MaxFailures: MaxFailures, LockFor: convert.Interval(LockFor)})
		if err == nil && r.LockedUntil != nil {
			c.log.WarnContext(ctx, "sign-in locked after repeated failures", "user_id", user.ID, "failures", r.FailedLogins)
		}
		return Opened{}, invalidCredentials()
	}
	token, tokenHash, err := credential.NewToken()
	if err != nil {
		return Opened{}, status.Error(codes.Internal, "token generation failed")
	}
	var out Opened
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.RecordSuccessfulLogin(ctx, user.ID); err != nil {
			return err
		}
		if err := q.DeleteStaleSessions(ctx, user.ID); err != nil {
			return err
		}
		now := c.now()
		expires := query.SlideTo(now, now)
		if _, err := q.InsertSession(ctx, sqlc.InsertSessionParams{ID: idgen.NewUUIDv7(), UserID: user.ID, TokenHash: tokenHash, ExpiresAt: expires}); err != nil {
			return err
		}
		ms, err := query.Memberships(ctx, q, user.ID)
		if err != nil {
			return err
		}
		out = Opened{User: query.User(user), Memberships: ms, Token: token, ExpireTime: expires}
		return nil
	})
	if err != nil {
		return Opened{}, convert.Err(err, "session")
	}
	return out, nil
}

// burn spends the time of a password check and returns the sign-in failure.
func (c *Commands) burn(ctx context.Context, password string) error {
	if err := c.withSlot(ctx, func() { credential.BurnTime(password) }); err != nil {
		return err
	}
	return invalidCredentials()
}

func domainInvalid(field, msg string) error { return domain.Invalid(field, msg) }

// SignOut revokes the session of the token. An unknown token is not an error: the caller wanted it gone and it is.
func (c *Commands) SignOut(ctx context.Context, token string) error {
	if !credential.WellFormedToken(token) {
		return nil
	}
	if _, err := sqlc.New(c.pool).RevokeSession(ctx, credential.HashToken(token)); err != nil {
		return convert.Err(err, "session")
	}
	return nil
}

// Slide moves the expiry of a session on. It is best effort: a failure is logged, the request that triggered it still succeeds.
func (c *Commands) Slide(ctx context.Context, sessionID uuid.UUID, expires time.Time) {
	if err := sqlc.New(c.pool).SlideSession(ctx, sqlc.SlideSessionParams{ID: sessionID, ExpiresAt: expires}); err != nil {
		c.log.WarnContext(ctx, "session expiry not slid", "err", err)
	}
}

func forbidden() error {
	return errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account does not allow this")
}

// CreateApp adds an app to an account the user owns or administers. Self-serve apps are free tier with no grace period; the settings that are immutable
// afterwards (currency, time zone) are the user's choice.
func (c *Commands) CreateApp(ctx context.Context, req *plansv1.CreateMyAppRequest) (*plansv1.App, error) {
	uid, ok1 := query.ParseID(req.GetUserId())
	aid, ok2 := query.ParseID(req.GetAccountId())
	if !ok1 || !ok2 {
		return nil, query.NotFound("account")
	}
	settings, err := domain.ValidateCreateAccount(&plansv1.CreateAccountRequest{
		Name: req.GetName(), Currency: req.GetCurrency(), Timezone: req.GetTimezone(),
		GracePeriod: durationpb.New(0), Tier: plansv1.TenantTier_TENANT_TIER_FREE,
	})
	if err != nil {
		return nil, err
	}
	slug, err := admin.ResolveSlug(settings.Name, req.GetSlug())
	if err != nil {
		return nil, err
	}
	var app *plansv1.App
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		m, err := q.GetMembership(ctx, sqlc.GetMembershipParams{AccountID: aid, UserID: uid})
		if errors.Is(err, pgx.ErrNoRows) {
			return query.NotFound("account")
		}
		if err != nil {
			return err
		}
		if !query.CanManage(m.Role) {
			return forbidden()
		}
		existing, err := q.ListAccountApps(ctx, aid)
		if err != nil {
			return err
		}
		if len(existing) >= MaxAppsPerAccount {
			return errkey.Newf(codes.ResourceExhausted, "identity.app.quota", "an account can have at most %d apps", MaxAppsPerAccount)
		}
		app, err = c.apps.InsertApp(ctx, q, aid, settings, slug)
		return err
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		return nil, errkey.New(codes.AlreadyExists, "plans.app.slug_taken", "this account already has an app with that slug")
	}
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	c.log.InfoContext(ctx, "app created by member", "user_id", uid, "account_id", aid, "app_id", app.GetId())
	return app, nil
}

// manageableApp loads the app of the user and requires a role that may manage it.
func (c *Commands) manageableApp(ctx context.Context, db *sqlc.Queries, userID, appID string) (*plansv1.App, error) {
	app, role, err := query.AppOf(ctx, db, userID, appID)
	if err != nil {
		return nil, err
	}
	if !query.CanManage(role) {
		return nil, forbidden()
	}
	return app, nil
}

// CreateAPIKey makes a key for the sandbox or the live tenant of an app. The plaintext is returned once.
func (c *Commands) CreateAPIKey(ctx context.Context, req *plansv1.CreateMyApiKeyRequest) (*plansv1.ApiKey, string, error) {
	db := sqlc.New(c.pool)
	app, err := c.manageableApp(ctx, db, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, "", err
	}
	var target *plansv1.Tenant
	for _, t := range app.GetTenants() {
		if t.GetEnvironment() == req.GetEnvironment() {
			target = t
		}
	}
	if target == nil {
		return nil, "", errkey.New(codes.InvalidArgument, "identity.api_key.bad_environment", "this app has no such environment")
	}
	keys, err := query.KeysOf(ctx, db, app)
	if err != nil {
		return nil, "", err
	}
	active := 0
	for _, k := range keys {
		if k.GetTenantId() == target.GetId() && k.GetStatus() == plansv1.ApiKey_STATUS_ACTIVE {
			active++
		}
	}
	if active >= MaxActiveKeysPerTenant {
		return nil, "", errkey.Newf(codes.ResourceExhausted, "identity.api_key.quota", "at most %d active keys per environment: revoke one first", MaxActiveKeysPerTenant)
	}
	tid, _ := uuid.Parse(target.GetId())
	return c.keys.IssueAPIKey(ctx, tid)
}

// RevokeAPIKey revokes a key of the app; a key of any other app is "not found".
func (c *Commands) RevokeAPIKey(ctx context.Context, req *plansv1.RevokeMyApiKeyRequest) (*plansv1.ApiKey, error) {
	db := sqlc.New(c.pool)
	app, err := c.manageableApp(ctx, db, req.GetUserId(), req.GetAppId())
	if err != nil {
		return nil, err
	}
	keys, err := query.KeysOf(ctx, db, app)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if k.GetId() == req.GetApiKeyId() {
			id, _ := uuid.Parse(k.GetId())
			return c.keys.RevokeKey(ctx, id)
		}
	}
	return nil, query.NotFound("api_key")
}
