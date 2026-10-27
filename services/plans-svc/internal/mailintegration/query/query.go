// Package query reads the tenant's own e-mail provider connection.
package query

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Queries reads the connection. box opens the sealed key for ResolveSender; nil means no key can be read.
type Queries struct {
	db  *store.DB
	box *secretbox.Box
}

// New returns the mail integration queries.
func New(db *store.DB, box *secretbox.Box) *Queries { return &Queries{db: db, box: box} }

// AAD is what binds a sealed key to its tenant: a row copied to another tenant does not open.
func AAD(tenantID string) []byte { return []byte("mail-integration:" + tenantID) }

// Message maps a row (never the key).
func Message(r sqlc.TenantMailIntegration) *plansv1.MailIntegration {
	out := &plansv1.MailIntegration{Provider: r.Provider, BaseUrl: r.BaseUrl, From: r.FromAddress, KeyHint: r.KeyHint, UpdateTime: timestamppb.New(r.UpdatedAt), CheckProblem: r.CheckProblem}
	if r.CheckedAt != nil {
		out.CheckedTime = timestamppb.New(*r.CheckedAt)
	}
	return out
}

// Get returns the connection without its key.
func (q *Queries) Get(ctx context.Context) (*plansv1.MailIntegration, error) {
	var out *plansv1.MailIntegration
	err := q.db.Tx(ctx, func(s *sqlc.Queries) error {
		r, err := s.GetMailIntegration(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = Message(r)
		return nil
	})
	return out, convert.Err(err, "mail integration")
}

// Sender is a connection with its key opened.
type Sender struct {
	Provider, BaseURL, APIKey, From string
}

// ResolveSender returns the connection with its key in plaintext, or nil when the tenant has none. It is the notifier's lookup.
func (q *Queries) ResolveSender(ctx context.Context) (*Sender, error) {
	t, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *Sender
	err = q.db.Tx(ctx, func(s *sqlc.Queries) error {
		r, err := s.GetMailIntegration(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		key, err := q.open(r.ApiKeyEnc, t.ID.String())
		if err != nil {
			return err
		}
		out = &Sender{Provider: r.Provider, BaseURL: r.BaseUrl, APIKey: key, From: r.FromAddress}
		return nil
	})
	return out, convert.Err(err, "mail integration")
}

// Open returns the plaintext key of a stored row.
func (q *Queries) open(sealed []byte, tenantID string) (string, error) {
	if q.box == nil {
		return "", errors.New("mail integration: PAYRUDDER_SECRETS_KEY is not set, the stored key cannot be opened")
	}
	b, err := q.box.Open(sealed, AAD(tenantID))
	return string(b), err
}
