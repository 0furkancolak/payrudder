// Package command changes the tenant's mail switch.
package command

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/mailcategory"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	mailqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/mailsettings/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
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
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var disabled []string
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		current, err := mailqry.Disabled(ctx, q)
		if err != nil {
			return err
		}
		disabled = apply(current, in)
		// The category that is off until it is switched on is a flag of its own in the row; the others are listed when they are off.
		stored := slices.DeleteFunc(slices.Clone(disabled), func(c string) bool { return c == mailcategory.CustomerNotices })
		if err := q.UpsertMailDisabled(ctx, sqlc.UpsertMailDisabledParams{Disabled: stored, CustomerNoticesOn: !slices.Contains(disabled, mailcategory.CustomerNotices)}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "mail_settings.changed", TargetType: "mail_settings", Before: current, After: disabled,
			Summary: fmt.Sprintf("%d mail categories switched off", len(disabled)), Detail: map[string]any{"disabled": disabled, "previously_disabled": current}})
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
