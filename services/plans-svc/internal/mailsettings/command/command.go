// Package command changes the tenant's mail switch.
package command

import (
	"context"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/mailcategory"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	mailqry "github.com/0furkancolak/quotify/services/plans-svc/internal/mailsettings/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Commands changes the mail settings.
type Commands struct{ db *store.DB }

// New returns the mail settings commands.
func New(db *store.DB) *Commands { return &Commands{db: db} }

// Set switches the listed categories on or off and keeps the rest as they were. It returns every category with its new state.
func (c *Commands) Set(ctx context.Context, in []*plansv1.MailCategorySetting) ([]*plansv1.MailCategorySetting, error) {
	for _, s := range in {
		if !mailcategory.Valid(s.GetCategory()) {
			return nil, errkey.Newf(codes.InvalidArgument, "plans.mail.unknown_category", "category %q is not one of %v", s.GetCategory(), mailcategory.All())
		}
	}
	var disabled []string
	err := c.db.Tx(ctx, func(q *sqlc.Queries) error {
		current, err := mailqry.Disabled(ctx, q)
		if err != nil {
			return err
		}
		disabled = apply(current, in)
		return q.UpsertMailDisabled(ctx, disabled)
	})
	if err != nil {
		return nil, convert.Err(err, "mail settings")
	}
	return mailqry.Messages(disabled), nil
}

// apply returns the disabled categories after the changes, in the order of mailcategory.All; a category listed twice takes its last value.
func apply(current []string, changes []*plansv1.MailCategorySetting) []string {
	off := map[string]bool{}
	for _, d := range current {
		off[d] = true
	}
	for _, s := range changes {
		off[s.GetCategory()] = !s.GetEnabled()
	}
	out := []string{}
	for _, cat := range mailcategory.All() {
		if off[cat] {
			out = append(out, cat)
		}
	}
	return out
}
