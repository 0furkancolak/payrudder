// Package command creates accounts and their apps. An app is made with its sandbox tenant and, when the deployment allows live, its live tenant. Accounts
// are cross-tenant by nature, so the commands run on the system pool (role plans_system, BYPASSRLS); the operator RPC and the identity module (a
// signed-in owner) each check their own authorisation before calling.
package command

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// defaultSlug is the slug of the app an account is created with.
const defaultSlug = "default"

// maxSlugLen bounds an app slug.
const maxSlugLen = 60

var (
	slugRe  = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	nonSlug = regexp.MustCompile(`[^a-z0-9]+`)
)

// Commands creates accounts and apps.
type Commands struct {
	pool        *pgxpool.Pool
	allowedEnvs []string
	log         *slog.Logger
}

// New returns the account commands on the system pool. allowedEnvs is PAYRUDDER_ALLOWED_ENVIRONMENTS ("sandbox", "live").
func New(systemPool *pgxpool.Pool, allowedEnvs []string, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, allowedEnvs: allowedEnvs, log: log}
}

// CreateAccount makes an account with its first app (slug "default").
func (c *Commands) CreateAccount(ctx context.Context, req *plansv1.CreateAccountRequest) (*plansv1.Account, error) {
	settings, err := domain.ValidateCreateAccount(req)
	if err != nil {
		return nil, err
	}
	acct := &plansv1.Account{}
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		a, err := q.InsertAccount(ctx, sqlc.InsertAccountParams{ID: idgen.NewUUIDv7(), Name: settings.Name})
		if err != nil {
			return err
		}
		acct.Id, acct.Name, acct.CreateTime = a.ID.String(), a.Name, timestamppb.New(a.CreatedAt)
		app, err := c.InsertApp(ctx, q, a.ID, settings, defaultSlug)
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
	c.log.InfoContext(ctx, "account created", "account_id", acct.GetId())
	return acct, nil
}

// CreateApp adds an application to an account.
func (c *Commands) CreateApp(ctx context.Context, req *plansv1.CreateAppRequest) (*plansv1.App, error) {
	accountID, err := domain.ParseID("account_id", req.GetAccountId())
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
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetAccount(ctx, accountID); err != nil {
			return err
		}
		var err error
		app, err = c.InsertApp(ctx, q, accountID, settings, slug)
		return err
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		return nil, SlugTaken()
	}
	if err != nil {
		return nil, convert.Err(err, "app")
	}
	return app, nil
}

// SlugTaken is the answer to a second app with the same slug in an account.
func SlugTaken() error {
	return errkey.New(codes.AlreadyExists, "plans.app.slug_taken", "this account already has an app with that slug")
}

// InsertApp makes an app of the account with its sandbox tenant and, when the deployment allows live, its live tenant, inside the caller's transaction.
func (c *Commands) InsertApp(ctx context.Context, q *sqlc.Queries, accountID uuid.UUID, settings domain.AccountSettings, slug string) (*plansv1.App, error) {
	envs := []env.Env{env.Sandbox}
	if env.Allowed(c.allowedEnvs, env.Live) {
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

// ResolveSlug returns the slug an app is stored under: slug itself, or the name in lower case with dashes when slug is empty.
func ResolveSlug(name, slug string) (string, error) {
	if slug == "" {
		slug = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
	}
	if !slugRe.MatchString(slug) || len(slug) > maxSlugLen {
		return "", errkey.New(codes.InvalidArgument, "plans.app.bad_slug", "slug: lower-case letters, digits and single dashes, at most 60 characters")
	}
	return slug, nil
}
