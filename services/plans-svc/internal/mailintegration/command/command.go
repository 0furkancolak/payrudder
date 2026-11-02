// Package command connects, tests and removes the tenant's own e-mail provider accounts, moves the default and sets which account sends the mail of which language.
package command

import (
	"context"
	"errors"
	"net/http"
	stdmail "net/mail"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/safehttp"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	mailqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailintegration/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// ProviderNafru is the one provider so far: Nafru Email, which speaks the Resend API.
const ProviderNafru = "nafru"

// Problems a check can end with, as stored and answered ("" is none).
const (
	ProblemUnreachable = "unreachable"
	ProblemRefused     = "refused"
)

// Commands changes the connection. client reaches the provider (safehttp.NewClient: it refuses private addresses); allowPrivate lets the base URL be http:// and
// private (development and tests only).
type Commands struct {
	db           *store.DB
	box          *secretbox.Box
	qry          *mailqry.Queries
	client       *http.Client
	allowPrivate bool
}

// New returns the commands. box seals the key (nil: no connection can be made).
func New(db *store.DB, box *secretbox.Box, qry *mailqry.Queries, allowPrivate bool) *Commands {
	return &Commands{db: db, box: box, qry: qry, client: safehttp.NewClient(15*time.Second, allowPrivate), allowPrivate: allowPrivate}
}

// requireLive refuses a sandbox tenant: its mail always goes to the sandbox sink, so a provider connection there would never be used.
func requireLive(ctx context.Context, q *sqlc.Queries) error {
	t, err := tenantsqry.Current(ctx, q)
	if err != nil {
		return err
	}
	if t.Environment != env.Live.String() {
		return errkey.New(codes.FailedPrecondition, "plans.mail_integration.live_only", "the sandbox sends its mail to the sandbox inbox; connect an e-mail provider in the live environment")
	}
	return nil
}

func (c *Commands) validate(req *plansv1.PutMailIntegrationRequest) error {
	var v domain.Violations
	if req.GetProvider() != ProviderNafru {
		v.Add("provider", "must be nafru")
	}
	if !payments.ValidAccountLabel(req.GetLabel()) {
		v.Add("label", "must be lower-case letters, digits and dashes, up to 31 characters")
	}
	if err := safehttp.ValidateURL(req.GetBaseUrl(), c.allowPrivate); err != nil {
		v.Add("base_url", err.Error())
	}
	if k := strings.TrimSpace(req.GetApiKey()); k == "" || len(k) > 512 || strings.ContainsAny(k, " \r\n\t") {
		v.Add("api_key", "is required (no spaces, at most 512 characters)")
	}
	if _, err := stdmail.ParseAddress(req.GetFrom()); err != nil || len(req.GetFrom()) > 320 || strings.ContainsAny(req.GetFrom(), "\r\n") {
		v.Add("from", "must be an e-mail address, or Name <address>")
	}
	return v.Err()
}

// problem classifies what a check or send failed with.
func problem(err error) string {
	var perm *mailx.PermanentError
	if errors.As(err, &perm) {
		return ProblemRefused
	}
	return ProblemUnreachable
}

// Put checks the key at the provider and stores the account, replacing one with the same key. A key the provider refuses is not stored. The first account of a
// tenant is its default, and so is one saved with make_default; saving an existing account keeps its place.
func (c *Commands) Put(ctx context.Context, req *plansv1.PutMailIntegrationRequest) (*plansv1.MailIntegration, error) {
	if c.box == nil {
		return nil, errkey.New(codes.FailedPrecondition, "plans.mail_integration.not_configured", "e-mail providers are not configured on this deployment (PAYRUDDER_SECRETS_KEY)")
	}
	t, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.validate(req); err != nil {
		return nil, err
	}
	var live error
	if err := c.db.Tx(ctx, func(q *sqlc.Queries) error { live = requireLive(ctx, q); return nil }); err != nil {
		return nil, convert.Err(err, "tenant")
	}
	if live != nil {
		return nil, live
	}
	key := strings.TrimSpace(req.GetApiKey())
	if err := (mailx.Resend{BaseURL: req.GetBaseUrl(), APIKey: key, Client: c.client}).Check(ctx); err != nil {
		if problem(err) == ProblemRefused {
			return nil, errkey.New(codes.InvalidArgument, "plans.mail_integration.refused", "the provider did not accept this key")
		}
		return nil, errkey.New(codes.Unavailable, "plans.mail_integration.unreachable", "the provider could not be reached")
	}
	account := payments.AccountKey(req.GetProvider(), req.GetLabel())
	sealed, err := c.box.Seal([]byte(key), mailqry.AAD(t.ID.String(), account))
	if err != nil {
		return nil, errkey.New(codes.Internal, "plans.mail_integration.seal_failed", "the key could not be stored")
	}
	hint := key
	if len(hint) > 4 {
		hint = hint[len(hint)-4:]
	}
	var out *plansv1.MailIntegration
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		existing, err := q.ListMailIntegrations(ctx)
		if err != nil {
			return err
		}
		makeDefault := req.GetMakeDefault() || len(existing) == 0
		if makeDefault {
			if err := q.ClearDefaultMailIntegration(ctx); err != nil {
				return err
			}
		}
		r, err := q.UpsertMailIntegration(ctx, sqlc.UpsertMailIntegrationParams{Provider: account, BaseUrl: strings.TrimRight(req.GetBaseUrl(), "/"), ApiKeyEnc: sealed, KeyHint: hint, FromAddress: req.GetFrom(), IsDefault: makeDefault})
		if err != nil {
			return err
		}
		out = mailqry.Message(r)
		summary := "connected the mail account " + account
		if slices.ContainsFunc(existing, func(e sqlc.TenantMailIntegration) bool { return e.Provider == account }) {
			summary = "replaced the mail account " + account
		}
		// Never the key, nor its hint: the account, the address of its API and the sender.
		return audit.Record(ctx, tx, audit.Event{TenantID: t.ID, Action: "mail_integration.saved", TargetType: "mail_integration", TargetID: account,
			Summary: summary, Detail: map[string]any{"account": account, "provider": req.GetProvider(), "base_url": r.BaseUrl, "from": req.GetFrom(), "default": r.IsDefault}})
	})
	if err != nil {
		return nil, convert.Err(err, "mail integration")
	}
	return out, nil
}

// Delete removes the account named by key (the default when empty): if it was the only one, the tenant's mail goes through Payrudder's own sender again. The
// default cannot be removed while others exist, so a mail never finds accounts and none to use; the rules that name the account go with it.
func (c *Commands) Delete(ctx context.Context, account string) error {
	t, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	if err := checkAccount(account); err != nil {
		return err
	}
	return convert.Err(c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		all, err := q.ListMailIntegrations(ctx)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(all, func(r sqlc.TenantMailIntegration) bool {
			return r.Provider == account || (account == "" && r.IsDefault)
		})
		if i < 0 {
			return errkey.New(codes.NotFound, "plans.mail_integration.not_connected", "no such e-mail account is connected")
		}
		if all[i].IsDefault && len(all) > 1 {
			return errkey.New(codes.FailedPrecondition, "plans.mail_integration.default_in_use", "this is the default account: make another one the default first")
		}
		if _, err := q.DeleteMailIntegration(ctx, all[i].Provider); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: t.ID, Action: "mail_integration.removed", TargetType: "mail_integration", TargetID: all[i].Provider,
			Summary: "disconnected the mail account " + all[i].Provider, Detail: map[string]any{"account": all[i].Provider}})
	}), "mail integration")
}

// checkAccount refuses a request that names an account that is not a key ("" is the default).
func checkAccount(account string) error {
	if account == "" || payments.ValidAccountKey(account) && payments.BaseProvider(account) == ProviderNafru {
		return nil
	}
	var v domain.Violations
	v.Add("account", "an account key such as nafru or nafru~eu")
	return v.Err()
}

// SetDefault makes the account named by key the default. The account is not touched, so no key is sent; recorded.
func (c *Commands) SetDefault(ctx context.Context, account string) error {
	t, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	if account == "" || checkAccount(account) != nil {
		var v domain.Violations
		v.Add("account", "an account key such as nafru or nafru~eu")
		return v.Err()
	}
	return convert.Err(c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.GetDefaultMailIntegration(ctx)
		if err == nil && cur.Provider == account {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err := q.GetMailIntegration(ctx, account); errors.Is(err, pgx.ErrNoRows) {
			return errkey.New(codes.NotFound, "plans.mail_integration.not_connected", "no such e-mail account is connected")
		} else if err != nil {
			return err
		}
		if err := q.ClearDefaultMailIntegration(ctx); err != nil {
			return err
		}
		if _, err := q.SetDefaultMailIntegration(ctx, account); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: t.ID, Action: "mail_integration.default_set", TargetType: "mail_integration", TargetID: account,
			Summary: "made " + account + " the default mail account", Detail: map[string]any{"account": account, "previous": cur.Provider}})
	}), "mail integration")
}

// SetRouting replaces the rules that send the mail of a language through an account. A language may appear once; every account must be connected. An empty list
// leaves everything to the default. Recorded with the rules.
func (c *Commands) SetRouting(ctx context.Context, rules []*plansv1.MailRoutingRule) ([]*plansv1.MailRoutingRule, error) {
	t, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var v domain.Violations
	seen := map[string]bool{}
	for i, r := range rules {
		field := "rules." + strconv.Itoa(i)
		if !slices.Contains(mailqry.Languages, r.GetLanguage()) {
			v.Add(field+".language", "must be one of "+strings.Join(mailqry.Languages, ", "))
		} else if seen[r.GetLanguage()] {
			v.Add(field+".language", "appears twice")
		}
		seen[r.GetLanguage()] = true
		if r.GetAccount() == "" || checkAccount(r.GetAccount()) != nil {
			v.Add(field+".account", "an account key such as nafru or nafru~eu")
		}
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		have, err := q.ListMailIntegrations(ctx)
		if err != nil {
			return err
		}
		for _, r := range rules {
			if !slices.ContainsFunc(have, func(a sqlc.TenantMailIntegration) bool { return a.Provider == r.GetAccount() }) {
				return errkey.New(codes.InvalidArgument, "plans.mail_integration.unknown_account", "a rule names an account that is not connected: "+r.GetAccount())
			}
		}
		if err := q.DeleteMailRouting(ctx); err != nil {
			return err
		}
		detail := map[string]any{}
		for _, r := range rules {
			if err := q.InsertMailRouting(ctx, sqlc.InsertMailRoutingParams{Language: r.GetLanguage(), Account: r.GetAccount()}); err != nil {
				return err
			}
			detail[r.GetLanguage()] = r.GetAccount()
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: t.ID, Action: "mail_integration.routing_changed", TargetType: "mail_integration",
			Summary: "changed which mail account sends which language", Detail: map[string]any{"rules": detail}})
	})
	if err != nil {
		return nil, convert.Err(err, "mail integration")
	}
	return c.qry.Routing(ctx)
}

// Test checks the stored key of an account (the default when account is empty) at the provider and sends one test mail to the address through it. A provider problem is
// the answer (ok false with its kind), not an error of the call; what was found is stored, so the status shows it.
func (c *Commands) Test(ctx context.Context, to, account string) (*plansv1.TestMailIntegrationResponse, error) {
	addr, err := stdmail.ParseAddress(to)
	if err != nil || strings.ContainsAny(to, "\r\n") {
		var v domain.Violations
		v.Add("to", "must be an e-mail address")
		return nil, v.Err()
	}
	if err := checkAccount(account); err != nil {
		return nil, err
	}
	sender, err := c.qry.Account(ctx, account)
	if err != nil {
		return nil, err
	}
	if sender == nil {
		return nil, errkey.New(codes.NotFound, "plans.mail_integration.not_connected", "no e-mail provider is connected")
	}
	rs := mailx.Resend{BaseURL: sender.BaseURL, APIKey: sender.APIKey, Client: c.client}
	var id string
	err = rs.Check(ctx)
	if err == nil {
		id, err = rs.Send(ctx, mailx.Message{From: sender.From, To: addr.Address, Subject: "Payrudder: your e-mail provider is connected",
			Text: "This is a test message from Payrudder. Invoices, receipts and alerts for your customers are sent through this provider.",
			HTML: "<p>This is a test message from Payrudder. Invoices, receipts and alerts for your customers are sent through this provider.</p>"})
	}
	p := ""
	if err != nil {
		p = problem(err)
	}
	if merr := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		return q.MarkMailIntegrationChecked(ctx, sqlc.MarkMailIntegrationCheckedParams{Problem: p, Account: sender.Account})
	}); merr != nil {
		return nil, convert.Err(merr, "mail integration")
	}
	return &plansv1.TestMailIntegrationResponse{Ok: err == nil, Problem: p, MessageId: id}, nil
}
