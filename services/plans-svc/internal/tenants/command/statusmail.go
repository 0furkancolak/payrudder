package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// WithMail makes a change of a live tenant's status tell the owners of its account (plan 14). Without it nobody is mailed.
func (c *Commands) WithMail(m usermail.Sender, appURL string) *Commands {
	c.mail, c.appURL = m, appURL
	return c
}

// statusMailKind is the mail that goes with a change of status: what the app became when it was taken out of service, "reinstated" when it was put back from
// being out of service. Activating an inactive tenant is not one (it was never in service), and a change that leaves the status as it was sends nothing.
func statusMailKind(from, to string) (usermail.Kind, bool) {
	switch {
	case from == to:
		return "", false
	case to == domain.StatusSuspended:
		return usermail.KindTenantSuspended, true
	case to == domain.StatusFrozen:
		return usermail.KindTenantFrozen, true
	case to == domain.StatusBanned:
		return usermail.KindTenantBanned, true
	case to == domain.StatusActive && held(from):
		return usermail.KindTenantReinstated, true
	}
	return "", false
}

// notifyOwners mails every owner of the tenant's account whose address is proven, in their language. It runs after the change committed and never fails it: a mail
// that cannot be composed is logged, one that cannot be sent is the sender's to log. The operator's category and reason are not in the mail (default, not agreed:
// they are the operator's notes; the mail says what stops and where to write).
func (c *Commands) notifyOwners(ctx context.Context, tenantID uuid.UUID, from, to string) {
	kind, ok := statusMailKind(from, to)
	if !ok || c.mail == nil {
		return
	}
	owners, err := sqlc.New(c.pool).TenantStatusMailTargets(ctx, tenantID)
	if err != nil {
		c.log.WarnContext(ctx, "tenant status mail not composed", "tenant_id", tenantID, "error", err)
		return
	}
	link := ""
	if c.appURL != "" {
		link = strings.TrimRight(c.appURL, "/") + "/app/support"
	}
	for _, o := range owners {
		c.mail.Send(ctx, usermail.Mail{Kind: kind, To: o.Email, Locale: o.Locale, Link: link, Account: o.AccountName, App: o.AppName})
	}
}
