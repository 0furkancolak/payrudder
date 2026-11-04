package command

import (
	"context"
	"errors"
	stdmail "net/mail"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	identityqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	mailqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailintegration/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Organisation-level e-mail accounts (docs/plans/10, ADR 0026): an account of the organisation that several of its apps use. The account is a row of
// organisation_mail_accounts (pooled database, system role); an app that uses it holds a binding, a row of its own tenant_mail_integrations that names it and holds no
// credential. Owners and administrators write, every other member reads.

func (c *Commands) orgReady() error {
	if c.pool == nil || c.box == nil {
		return errkey.New(codes.FailedPrecondition, "plans.mail_integration.not_configured", "e-mail providers are not configured on this deployment (PAYRUDDER_SECRETS_KEY)")
	}
	return nil
}

// OrganisationMessage maps a row (never the key) with the apps that use it.
func OrganisationMessage(r sqlc.OrganisationMailAccount, used []*plansv1.OrganisationMailApp) *plansv1.OrganisationMailAccount {
	out := &plansv1.OrganisationMailAccount{Id: r.ID.String(), Account: r.Provider, Provider: payments.BaseProvider(r.Provider), Label: payments.AccountLabel(r.Provider), BaseUrl: r.BaseUrl,
		From: r.FromAddress, KeyHint: r.KeyHint, UpdateTime: timestamppb.New(r.UpdatedAt), CheckProblem: r.CheckProblem, UsedBy: used}
	if out.UsedBy == nil {
		out.UsedBy = []*plansv1.OrganisationMailApp{}
	}
	if r.CheckedAt != nil {
		out.CheckedTime = timestamppb.New(*r.CheckedAt)
	}
	return out
}

// usedBy lists, for every account of the organisation, the apps whose live environment has a binding to it. The bindings are in the apps' own tenants (a cell, maybe), so
// this asks each live tenant of the organisation.
func (c *Commands) usedBy(ctx context.Context, aid uuid.UUID) (map[uuid.UUID][]*plansv1.OrganisationMailApp, error) {
	q := sqlc.New(c.pool)
	apps, err := q.ListAccountApps(ctx, aid)
	if err != nil {
		return nil, err
	}
	tenants, err := q.ListAccountTenants(ctx, aid)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID][]*plansv1.OrganisationMailApp{}
	for _, a := range apps {
		i := slices.IndexFunc(tenants, func(t sqlc.Tenant) bool { return t.AppID == a.ID && t.Environment == "live" })
		if i < 0 {
			continue
		}
		tctx := tenant.NewContext(ctx, tenant.Info{ID: tenants[i].ID, Environment: "live"})
		if err := c.db.Tx(tctx, func(tq *sqlc.Queries) error {
			rows, err := tq.ListMailIntegrations(tctx)
			for _, r := range rows {
				if r.SharedAccountID != nil {
					out[*r.SharedAccountID] = append(out[*r.SharedAccountID], &plansv1.OrganisationMailApp{AppId: a.ID.String(), Name: a.Name, IsDefault: r.IsDefault})
				}
			}
			return err
		}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListOrganisation returns the organisation's accounts with the apps that use each. Any member reads; canWrite says whether the caller may change them.
func (c *Commands) ListOrganisation(ctx context.Context, userID, accountID string) ([]*plansv1.OrganisationMailAccount, bool, error) {
	if c.pool == nil {
		return []*plansv1.OrganisationMailAccount{}, false, nil
	}
	_, aid, m, err := identityqry.Membership(ctx, sqlc.New(c.pool), userID, accountID)
	if err != nil {
		return nil, false, err
	}
	rows, err := sqlc.New(c.pool).ListOrganisationMailAccounts(ctx, aid)
	if err != nil {
		return nil, false, convert.Err(err, "mail integration")
	}
	used, err := c.usedBy(ctx, aid)
	if err != nil {
		return nil, false, convert.Err(err, "mail integration")
	}
	out := make([]*plansv1.OrganisationMailAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, OrganisationMessage(r, used[r.ID]))
	}
	return out, identityqry.CanManage(m.Role), nil
}

// PutOrganisation checks the key at the provider and stores the organisation's account, replacing one with the same key (the apps that use it keep using it). A key the
// provider refuses is not stored. Recorded without the key or its hint.
func (c *Commands) PutOrganisation(ctx context.Context, req *plansv1.PutOrganisationMailAccountRequest) (*plansv1.OrganisationMailAccount, error) {
	if err := c.orgReady(); err != nil {
		return nil, err
	}
	_, aid, _, err := identityqry.RequireManager(ctx, sqlc.New(c.pool), req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if err := c.validate(&plansv1.PutMailIntegrationRequest{Provider: req.GetProvider(), BaseUrl: req.GetBaseUrl(), ApiKey: req.GetApiKey(), From: req.GetFrom(), Label: req.GetLabel()}); err != nil {
		return nil, err
	}
	key := strings.TrimSpace(req.GetApiKey())
	if err := (mailx.Resend{BaseURL: req.GetBaseUrl(), APIKey: key, Client: c.client}).Check(ctx); err != nil {
		if problem(err) == ProblemRefused {
			return nil, errkey.New(codes.InvalidArgument, "plans.mail_integration.refused", "the provider did not accept this key")
		}
		return nil, errkey.New(codes.Unavailable, "plans.mail_integration.unreachable", "the provider could not be reached")
	}
	account := payments.AccountKey(req.GetProvider(), req.GetLabel())
	sealed, err := c.box.Seal([]byte(key), mailqry.SharedAAD(aid.String(), account))
	if err != nil {
		return nil, errkey.New(codes.Internal, "plans.mail_integration.seal_failed", "the key could not be stored")
	}
	hint := key
	if len(hint) > 4 {
		hint = hint[len(hint)-4:]
	}
	var out *plansv1.OrganisationMailAccount
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		summary := "connected the organisation's mail account " + account
		if _, err := q.GetOrganisationMailAccount(ctx, sqlc.GetOrganisationMailAccountParams{AccountID: aid, Provider: account}); err == nil {
			summary = "replaced the organisation's mail account " + account
		}
		r, err := q.UpsertOrganisationMailAccount(ctx, sqlc.UpsertOrganisationMailAccountParams{ID: idgen.NewUUIDv7(), AccountID: aid, Provider: account,
			BaseUrl: strings.TrimRight(req.GetBaseUrl(), "/"), ApiKeyEnc: sealed, KeyHint: hint, FromAddress: req.GetFrom()})
		if err != nil {
			return err
		}
		out = OrganisationMessage(r, nil)
		return audit.Record(ctx, tx, audit.Event{AccountID: aid, Action: "mail_integration.org_saved", TargetType: "mail_integration", TargetID: account, Summary: summary,
			Detail: map[string]any{"account": account, "provider": req.GetProvider(), "base_url": r.BaseUrl, "from": req.GetFrom()}})
	})
	if err != nil {
		return nil, convert.Err(err, "mail integration")
	}
	return out, nil
}

// DeleteOrganisation removes the organisation's account. While apps use it nothing is removed: 409 plans.mail_integration.shared_in_use, naming them. The lock that binding
// takes too means an app cannot bind in the instant it is removed.
func (c *Commands) DeleteOrganisation(ctx context.Context, req *plansv1.DeleteOrganisationMailAccountRequest) error {
	if err := c.orgReady(); err != nil {
		return err
	}
	_, aid, _, err := identityqry.RequireManager(ctx, sqlc.New(c.pool), req.GetUserId(), req.GetAccountId())
	if err != nil {
		return err
	}
	if err := checkOrgAccount(req.GetAccount()); err != nil {
		return err
	}
	return convert.Err(pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		r, err := q.LockOrganisationMailAccount(ctx, sqlc.LockOrganisationMailAccountParams{AccountID: aid, Provider: req.GetAccount()})
		if errors.Is(err, pgx.ErrNoRows) {
			return orgNotFound()
		}
		if err != nil {
			return err
		}
		used, err := c.usedBy(ctx, aid)
		if err != nil {
			return err
		}
		if apps := used[r.ID]; len(apps) > 0 {
			names := make([]string, len(apps))
			for i, a := range apps {
				names[i] = a.GetName()
			}
			return errkey.New(codes.FailedPrecondition, "plans.mail_integration.shared_in_use", "apps use this account: "+strings.Join(names, ", ")+"; stop them using it first")
		}
		if _, err := q.DeleteOrganisationMailAccount(ctx, r.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{AccountID: aid, Action: "mail_integration.org_removed", TargetType: "mail_integration", TargetID: r.Provider,
			Summary: "disconnected the organisation's mail account " + r.Provider, Detail: map[string]any{"account": r.Provider}})
	}), "mail integration")
}

// TestOrganisation checks the stored key at the provider and sends one test mail through it; a provider problem is the answer, stored on the account.
func (c *Commands) TestOrganisation(ctx context.Context, req *plansv1.TestOrganisationMailAccountRequest) (*plansv1.TestOrganisationMailAccountResponse, error) {
	if err := c.orgReady(); err != nil {
		return nil, err
	}
	_, aid, _, err := identityqry.RequireManager(ctx, sqlc.New(c.pool), req.GetUserId(), req.GetAccountId())
	if err != nil {
		return nil, err
	}
	addr, err := stdmail.ParseAddress(req.GetTo())
	if err != nil || strings.ContainsAny(req.GetTo(), "\r\n") {
		var v domain.Violations
		v.Add("to", "must be an e-mail address")
		return nil, v.Err()
	}
	if err := checkOrgAccount(req.GetAccount()); err != nil {
		return nil, err
	}
	q := sqlc.New(c.pool)
	r, err := q.GetOrganisationMailAccount(ctx, sqlc.GetOrganisationMailAccountParams{AccountID: aid, Provider: req.GetAccount()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, orgNotFound()
	}
	if err != nil {
		return nil, convert.Err(err, "mail integration")
	}
	b, err := c.box.Open(r.ApiKeyEnc, mailqry.SharedAAD(aid.String(), r.Provider))
	if err != nil {
		return nil, errkey.New(codes.Internal, "plans.mail_integration.seal_failed", "the stored key could not be opened")
	}
	rs := mailx.Resend{BaseURL: r.BaseUrl, APIKey: string(b), Client: c.client}
	var id string
	err = rs.Check(ctx)
	if err == nil {
		id, err = rs.Send(ctx, mailx.Message{From: r.FromAddress, To: addr.Address, Subject: "Payrudder: your e-mail provider is connected",
			Text: "This is a test message from Payrudder. Invoices, receipts and alerts for your customers are sent through this provider.",
			HTML: "<p>This is a test message from Payrudder. Invoices, receipts and alerts for your customers are sent through this provider.</p>"})
	}
	p := ""
	if err != nil {
		p = problem(err)
	}
	if err := q.MarkOrganisationMailAccountChecked(ctx, sqlc.MarkOrganisationMailAccountCheckedParams{Problem: p, ID: r.ID}); err != nil {
		return nil, convert.Err(err, "mail integration")
	}
	return &plansv1.TestOrganisationMailAccountResponse{Ok: err == nil, Problem: p, MessageId: id}, nil
}

// liveTenantOfApp is the live tenant of an app of the organisation; an app that is not the organisation's, or is deleted, is not found.
func (c *Commands) liveTenantOfApp(ctx context.Context, aid uuid.UUID, appID string) (uuid.UUID, error) {
	id, err := uuid.Parse(appID)
	notFound := errkey.New(codes.NotFound, "identity.app.not_found", "app not found")
	if err != nil {
		return uuid.Nil, notFound
	}
	q := sqlc.New(c.pool)
	apps, err := q.ListAccountApps(ctx, aid)
	if err != nil {
		return uuid.Nil, convert.Err(err, "app")
	}
	if !slices.ContainsFunc(apps, func(a sqlc.App) bool { return a.ID == id }) {
		return uuid.Nil, notFound
	}
	tenants, err := q.ListAccountTenants(ctx, aid)
	if err != nil {
		return uuid.Nil, convert.Err(err, "app")
	}
	i := slices.IndexFunc(tenants, func(t sqlc.Tenant) bool { return t.AppID == id && t.Environment == "live" })
	if i < 0 {
		return uuid.Nil, errkey.New(codes.FailedPrecondition, "plans.mail_integration.live_only", "this app has no live environment: connect an e-mail provider where there is one")
	}
	return tenants[i].ID, nil
}

// BindOrganisation makes an app use the organisation's account: a binding in the app's live environment, default when asked (or when it is the app's first account). It
// refuses an app whose own account already has the key. Recorded in the app's trail and the organisation's.
func (c *Commands) BindOrganisation(ctx context.Context, req *plansv1.BindOrganisationMailAccountRequest) error {
	if err := c.orgReady(); err != nil {
		return err
	}
	_, aid, _, err := identityqry.RequireManager(ctx, sqlc.New(c.pool), req.GetUserId(), req.GetAccountId())
	if err != nil {
		return err
	}
	if err := checkOrgAccount(req.GetAccount()); err != nil {
		return err
	}
	tid, err := c.liveTenantOfApp(ctx, aid, req.GetAppId())
	if err != nil {
		return err
	}
	return convert.Err(pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		r, err := sqlc.New(tx).LockOrganisationMailAccount(ctx, sqlc.LockOrganisationMailAccountParams{AccountID: aid, Provider: req.GetAccount()})
		if errors.Is(err, pgx.ErrNoRows) {
			return orgNotFound()
		}
		if err != nil {
			return err
		}
		tctx := tenant.NewContext(ctx, tenant.Info{ID: tid, Environment: "live"})
		return c.db.TxWith(tctx, func(q *sqlc.Queries, ttx pgx.Tx) error {
			if err := requireLive(tctx, q); err != nil {
				return err
			}
			have, err := q.ListMailIntegrations(tctx)
			if err != nil {
				return err
			}
			if i := slices.IndexFunc(have, func(h sqlc.TenantMailIntegration) bool { return h.Provider == r.Provider }); i >= 0 {
				if have[i].SharedAccountID != nil && *have[i].SharedAccountID == r.ID {
					return nil // already bound
				}
				return errkey.New(codes.AlreadyExists, "plans.mail_integration.account_exists", "this app already has its own e-mail account with that key: remove it or give the shared account another label")
			}
			makeDefault := req.GetMakeDefault() || len(have) == 0
			if makeDefault {
				if err := q.ClearDefaultMailIntegration(tctx); err != nil {
					return err
				}
			}
			if _, err := q.InsertSharedMailBinding(tctx, sqlc.InsertSharedMailBindingParams{Provider: r.Provider, SharedAccountID: &r.ID, IsDefault: makeDefault}); err != nil {
				return err
			}
			return audit.Record(tctx, ttx, audit.Event{TenantID: tid, AccountID: aid, Action: "mail_integration.org_bound", TargetType: "mail_integration", TargetID: r.Provider,
				Summary: "uses the organisation's mail account " + r.Provider, Detail: map[string]any{"account": r.Provider, "default": makeDefault}})
		})
	}), "mail integration")
}

// UnbindOrganisation stops an app from using the organisation's account. The app's default cannot be unbound while it has other accounts, as for its own.
func (c *Commands) UnbindOrganisation(ctx context.Context, req *plansv1.UnbindOrganisationMailAccountRequest) error {
	if err := c.orgReady(); err != nil {
		return err
	}
	_, aid, _, err := identityqry.RequireManager(ctx, sqlc.New(c.pool), req.GetUserId(), req.GetAccountId())
	if err != nil {
		return err
	}
	if err := checkOrgAccount(req.GetAccount()); err != nil {
		return err
	}
	tid, err := c.liveTenantOfApp(ctx, aid, req.GetAppId())
	if err != nil {
		return err
	}
	tctx := tenant.NewContext(ctx, tenant.Info{ID: tid, Environment: "live"})
	return convert.Err(c.db.TxWith(tctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		all, err := q.ListMailIntegrations(tctx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(all, func(r sqlc.TenantMailIntegration) bool {
			return r.Provider == req.GetAccount() && r.SharedAccountID != nil
		})
		if i < 0 {
			return errkey.New(codes.NotFound, "plans.mail_integration.not_connected", "this app does not use that account")
		}
		if all[i].IsDefault && len(all) > 1 {
			return errkey.New(codes.FailedPrecondition, "plans.mail_integration.default_in_use", "this is the default account: make another one the default first")
		}
		if _, err := q.DeleteMailIntegration(tctx, all[i].Provider); err != nil {
			return err
		}
		return audit.Record(tctx, tx, audit.Event{TenantID: tid, AccountID: aid, Action: "mail_integration.org_unbound", TargetType: "mail_integration", TargetID: all[i].Provider,
			Summary: "stopped using the organisation's mail account " + all[i].Provider, Detail: map[string]any{"account": all[i].Provider}})
	}), "mail integration")
}

func orgNotFound() error {
	return errkey.New(codes.NotFound, "plans.mail_integration.not_connected", "no such e-mail account is connected")
}

// checkOrgAccount refuses a request that does not name an account by its key (unlike an app's, there is no default to fall back on).
func checkOrgAccount(account string) error {
	if account == "" {
		var v domain.Violations
		v.Add("account", "an account key such as nafru or nafru~eu")
		return v.Err()
	}
	return checkAccount(account)
}
