// Package query reads the tenant's own e-mail provider accounts.
package query

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Languages are the languages a mail is in, and so the languages a routing rule can name (the first is the fallback, English: a mail with no language is English).
var Languages = []string{"en", "tr", "de", "fr", "es", "ar"}

// Queries reads the accounts. box opens the sealed key for ResolveSender; nil means no key can be read.
type Queries struct {
	db  *store.DB
	box *secretbox.Box
	// sys reads the organisation's accounts (a global table the app role cannot see); nil means an app's binding to one cannot be opened.
	sys *pgxpool.Pool
}

// WithOrganisations lets ResolveSender open the account an app's binding names, on the system pool.
func (q *Queries) WithOrganisations(sys *pgxpool.Pool) *Queries {
	q.sys = sys
	return q
}

// New returns the mail integration queries.
func New(db *store.DB, box *secretbox.Box) *Queries { return &Queries{db: db, box: box} }

// AAD is what binds a sealed key to its tenant, and for a labelled account to its key: a row copied to another tenant or account does not open. The primary account keeps
// the form every key was sealed with before labels (the reseal job writes it too: pkg/secretbox/reseal/places.go).
func AAD(tenantID, account string) []byte {
	if payments.AccountLabel(account) == "" {
		return []byte("mail-integration:" + tenantID)
	}
	return []byte("mail-integration:" + tenantID + ":" + account)
}

// SharedAAD binds an organisation's key to the organisation and the account key, never to an app: the same box serves every app that uses the account. The reseal job
// writes it too (pkg/secretbox/reseal/places.go).
func SharedAAD(organisationID, account string) []byte {
	return []byte("org-mail-account:" + organisationID + ":" + account)
}

// Message maps a row (never the key).
func Message(r sqlc.TenantMailIntegration) *plansv1.MailIntegration {
	out := &plansv1.MailIntegration{Provider: payments.BaseProvider(r.Provider), BaseUrl: r.BaseUrl, From: r.FromAddress, KeyHint: r.KeyHint, UpdateTime: timestamppb.New(r.UpdatedAt),
		CheckProblem: r.CheckProblem, Account: r.Provider, Label: payments.AccountLabel(r.Provider), IsDefault: r.IsDefault, Inherited: r.SharedAccountID != nil}
	if r.CheckedAt != nil {
		out.CheckedTime = timestamppb.New(*r.CheckedAt)
	}
	return out
}

// List returns every account without its key, the default first.
func (q *Queries) List(ctx context.Context) ([]*plansv1.MailIntegration, error) {
	out := []*plansv1.MailIntegration{}
	err := q.db.Tx(ctx, func(s *sqlc.Queries) error {
		rows, err := s.ListMailIntegrations(ctx)
		for _, r := range rows {
			out = append(out, Message(r))
		}
		return err
	})
	return out, convert.Err(err, "mail integration")
}

// Sender is an account with its key opened.
type Sender struct {
	Account, Provider, BaseURL, APIKey, From string
}

func (q *Queries) sender(ctx context.Context, r sqlc.TenantMailIntegration, tenantID string) (*Sender, error) {
	if r.SharedAccountID != nil {
		return q.sharedSender(ctx, *r.SharedAccountID)
	}
	key, err := q.open(r.ApiKeyEnc, tenantID, r.Provider)
	if err != nil {
		return nil, err
	}
	return &Sender{Account: r.Provider, Provider: payments.BaseProvider(r.Provider), BaseURL: r.BaseUrl, APIKey: key, From: r.FromAddress}, nil
}

// sharedSender opens an organisation's account for one mail. A binding whose account is gone is an error, not "no account": the mail must not silently leave through
// another sender.
func (q *Queries) sharedSender(ctx context.Context, id uuid.UUID) (*Sender, error) {
	if q.sys == nil {
		return nil, errors.New("mail integration: the organisation's accounts are not wired")
	}
	row, err := sqlc.New(q.sys).GetOrganisationMailAccountByID(ctx, id)
	if err != nil {
		return nil, err
	}
	key, err := q.openShared(row)
	if err != nil {
		return nil, err
	}
	return &Sender{Account: row.Provider, Provider: payments.BaseProvider(row.Provider), BaseURL: row.BaseUrl, APIKey: key, From: row.FromAddress}, nil
}

func (q *Queries) openShared(row sqlc.OrganisationMailAccount) (string, error) {
	if q.box == nil {
		return "", errors.New("mail integration: PAYRUDDER_SECRETS_KEY is not set, the stored key cannot be opened")
	}
	b, err := q.box.Open(row.ApiKeyEnc, SharedAAD(row.AccountID.String(), row.Provider))
	return string(b), err
}

// Account returns the account named by key with its key in plaintext, or the default account when key is empty; nil when the tenant has no such account.
func (q *Queries) Account(ctx context.Context, account string) (*Sender, error) {
	t, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *Sender
	err = q.db.Tx(ctx, func(s *sqlc.Queries) error {
		var r sqlc.TenantMailIntegration
		var err error
		if account == "" {
			r, err = s.GetDefaultMailIntegration(ctx)
		} else {
			r, err = s.GetMailIntegration(ctx, account)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, err = q.sender(ctx, r, t.ID.String())
		return err
	})
	return out, convert.Err(err, "mail integration")
}

// ResolveSender is the notifier's lookup: the account a mail in language goes through, with its key in plaintext. The account a rule names for the language, else the
// default; nil when the tenant has none (its mail goes through Payrudder's own sender).
func (q *Queries) ResolveSender(ctx context.Context, language string) (*Sender, error) {
	t, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	if language == "" {
		language = Languages[0]
	}
	var out *Sender
	err = q.db.Tx(ctx, func(s *sqlc.Queries) error {
		var r sqlc.TenantMailIntegration
		rule, err := s.GetMailRoutingFor(ctx, language)
		if err == nil {
			r, err = s.GetMailIntegration(ctx, rule.Account)
		} else if errors.Is(err, pgx.ErrNoRows) {
			r, err = s.GetDefaultMailIntegration(ctx)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out, err = q.sender(ctx, r, t.ID.String())
		return err
	})
	return out, convert.Err(err, "mail integration")
}

// Routing returns the rules, in the order of the languages.
func (q *Queries) Routing(ctx context.Context) ([]*plansv1.MailRoutingRule, error) {
	out := []*plansv1.MailRoutingRule{}
	err := q.db.Tx(ctx, func(s *sqlc.Queries) error {
		rows, err := s.ListMailRouting(ctx)
		for _, r := range rows {
			out = append(out, &plansv1.MailRoutingRule{Language: r.Language, Account: r.Account})
		}
		return err
	})
	slices.SortFunc(out, func(a, b *plansv1.MailRoutingRule) int {
		return slices.Index(Languages, a.Language) - slices.Index(Languages, b.Language)
	})
	return out, convert.Err(err, "mail integration")
}

// Open returns the plaintext key of a stored row.
func (q *Queries) open(sealed []byte, tenantID, account string) (string, error) {
	if q.box == nil {
		return "", errors.New("mail integration: PAYRUDDER_SECRETS_KEY is not set, the stored key cannot be opened")
	}
	b, err := q.box.Open(sealed, AAD(tenantID, account))
	return string(b), err
}
