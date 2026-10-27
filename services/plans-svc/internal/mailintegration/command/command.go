// Package command connects, tests and removes the tenant's own e-mail provider.
package command

import (
	"context"
	"errors"
	"net/http"
	stdmail "net/mail"
	"strings"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/mailx"
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

// Put checks the key at the provider and stores the connection, replacing a previous one. A key the provider refuses is not stored.
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
	sealed, err := c.box.Seal([]byte(key), mailqry.AAD(t.ID.String()))
	if err != nil {
		return nil, errkey.New(codes.Internal, "plans.mail_integration.seal_failed", "the key could not be stored")
	}
	hint := key
	if len(hint) > 4 {
		hint = hint[len(hint)-4:]
	}
	var out *plansv1.MailIntegration
	err = c.db.Tx(ctx, func(q *sqlc.Queries) error {
		r, err := q.UpsertMailIntegration(ctx, sqlc.UpsertMailIntegrationParams{Provider: req.GetProvider(), BaseUrl: strings.TrimRight(req.GetBaseUrl(), "/"), ApiKeyEnc: sealed, KeyHint: hint, FromAddress: req.GetFrom()})
		if err != nil {
			return err
		}
		out = mailqry.Message(r)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "mail integration")
	}
	return out, nil
}

// Delete removes the connection: the tenant's mail goes through Payrudder's own sender again.
func (c *Commands) Delete(ctx context.Context) error {
	return convert.Err(c.db.Tx(ctx, func(q *sqlc.Queries) error {
		n, err := q.DeleteMailIntegration(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return errkey.New(codes.NotFound, "plans.mail_integration.not_connected", "no e-mail provider is connected")
		}
		return nil
	}), "mail integration")
}

// Test checks the stored key at the provider and sends one test mail to the address through it. A provider problem is the answer (ok false with its kind), not an
// error of the call; what was found is stored, so the status shows it.
func (c *Commands) Test(ctx context.Context, to string) (*plansv1.TestMailIntegrationResponse, error) {
	addr, err := stdmail.ParseAddress(to)
	if err != nil || strings.ContainsAny(to, "\r\n") {
		var v domain.Violations
		v.Add("to", "must be an e-mail address")
		return nil, v.Err()
	}
	sender, err := c.qry.ResolveSender(ctx)
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
	if merr := c.db.Tx(ctx, func(q *sqlc.Queries) error { return q.MarkMailIntegrationChecked(ctx, p) }); merr != nil {
		return nil, convert.Err(merr, "mail integration")
	}
	return &plansv1.TestMailIntegrationResponse{Ok: err == nil, Problem: p, MessageId: id}, nil
}
