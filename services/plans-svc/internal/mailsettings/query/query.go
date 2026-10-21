// Package query reads the tenant's mail switch: which categories of Quotify's outgoing e-mail are on.
package query

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/mailcategory"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Queries reads the mail settings.
type Queries struct{ db *store.DB }

// New returns the mail settings queries.
func New(db *store.DB) *Queries { return &Queries{db: db} }

// Disabled loads the categories the tenant has switched off, in the caller's transaction. A tenant that never saved a setting has none off.
func Disabled(ctx context.Context, q *sqlc.Queries) ([]string, error) {
	d, err := q.GetMailDisabled(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// Messages lists every category with its state, in the fixed order of mailcategory.All.
func Messages(disabled []string) []*plansv1.MailCategorySetting {
	all := mailcategory.All()
	out := make([]*plansv1.MailCategorySetting, 0, len(all))
	for _, c := range all {
		out = append(out, &plansv1.MailCategorySetting{Category: c, Enabled: !slices.Contains(disabled, c)})
	}
	return out
}

// Get returns every category with whether it is on.
func (r *Queries) Get(ctx context.Context) ([]*plansv1.MailCategorySetting, error) {
	var disabled []string
	err := r.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		disabled, err = Disabled(ctx, q)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "mail settings")
	}
	return Messages(disabled), nil
}
