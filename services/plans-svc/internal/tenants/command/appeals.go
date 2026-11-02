package command

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// ListAppeals returns the owners' appeals, newest first (the latest 200), of one tenant or of all, of one status or of all, with the names of the apps.
func (c *Commands) ListAppeals(ctx context.Context, req *plansv1.ListTenantAppealsRequest) (*plansv1.ListTenantAppealsResponse, error) {
	tenantID := uuid.Nil
	if req.GetTenantId() != "" {
		id, err := domain.ParseID("tenant_id", req.GetTenantId())
		if err != nil {
			return nil, err
		}
		tenantID = id
	}
	if s := req.GetStatus(); s != "" && s != "open" && s != "accepted" && s != "rejected" {
		return nil, domain.Invalid("status", "open, accepted or rejected")
	}
	rows, err := sqlc.New(c.pool).ListTenantAppeals(ctx, sqlc.ListTenantAppealsParams{LiveTenantID: tenantID, Status: req.GetStatus()})
	if err != nil {
		return nil, convert.Err(err, "appeal")
	}
	out := &plansv1.ListTenantAppealsResponse{Appeals: make([]*plansv1.TenantAppeal, 0, len(rows)), AppNames: map[string]string{}}
	for _, r := range rows {
		out.Appeals = append(out.Appeals, convert.TenantAppeal(sqlc.TenantAppeal{ID: r.ID, LiveTenantID: r.LiveTenantID, Status: r.Status, HeldStatus: r.HeldStatus, Statement: r.Statement, SubmittedBy: r.SubmittedBy, Answer: r.Answer, DecidedBy: r.DecidedBy, CreatedAt: r.CreatedAt, DecidedAt: r.DecidedAt}))
		out.AppNames[r.LiveTenantID.String()] = r.AppName
	}
	return out, nil
}

// DecideAppeal accepts or rejects an open appeal and tells the owners. Accepting reinstates the tenant first (the owners then get the mail of the appeal, not the mail of
// the reinstatement); if that works and the decision cannot be written, the appeal stays open and deciding it again reinstates nothing more. Rejecting needs an answer: an
// owner whose appeal is refused is owed a sentence. The operator's category and note of the hold are never in the answer unless the operator writes them there.
func (c *Commands) DecideAppeal(ctx context.Context, req *plansv1.DecideTenantAppealRequest) (*plansv1.TenantAppeal, error) {
	id, err := domain.ParseID("appeal_id", req.GetAppealId())
	if err != nil {
		return nil, err
	}
	decision := req.GetDecision()
	if decision != "accepted" && decision != "rejected" {
		return nil, domain.Invalid("decision", "accepted or rejected")
	}
	answer := strings.TrimSpace(req.GetAnswer())
	if utf8.RuneCountInString(answer) > 1000 {
		return nil, domain.Invalid("answer", "at most 1000 characters")
	}
	if decision == "rejected" && answer == "" {
		return nil, domain.Invalid("answer", "required to reject an appeal")
	}
	actor := actorOf(req.GetActor())
	cur, err := sqlc.New(c.pool).GetTenantAppeal(ctx, id)
	if err != nil {
		return nil, convert.Err(err, "appeal")
	}
	if cur.Status != "open" {
		return nil, errkey.New(codes.FailedPrecondition, "plans.appeal.not_open", "this appeal was decided already")
	}
	if decision == "accepted" {
		if _, err := c.setStatus(ctx, &plansv1.SetTenantStatusRequest{TenantId: cur.LiveTenantID.String(), Status: plansv1.TenantStatus_TENANT_STATUS_ACTIVE, Category: "request",
			Reason: "appeal accepted", Actor: actor}, "", true); err != nil {
			return nil, err
		}
	}
	var out sqlc.TenantAppeal
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var err error
		if out, err = q.DecideTenantAppeal(ctx, sqlc.DecideTenantAppealParams{ID: id, Status: decision, Answer: answer, DecidedBy: actor}); err != nil {
			return err // no row: another operator decided it first
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: out.LiveTenantID, Action: "tenant_appeal.decided", TargetType: "tenant_appeal", TargetID: id.String(),
			Summary: decision + " the appeal against the status " + out.HeldStatus, Detail: map[string]any{"decision": decision, "held_status": out.HeldStatus}})
	})
	if err != nil {
		return nil, convert.Err(err, "appeal")
	}
	kind := usermail.KindTenantAppealRejected
	if decision == "accepted" {
		kind = usermail.KindTenantAppealAccepted
	}
	c.notifyAppeal(ctx, out.LiveTenantID, kind, answer)
	return convert.TenantAppeal(out), nil
}

// notifyAppeal mails the owners of the tenant's account (the proven addresses, in their language) how their appeal was decided. It never fails the decision.
func (c *Commands) notifyAppeal(ctx context.Context, tenantID uuid.UUID, kind usermail.Kind, answer string) {
	if c.mail == nil {
		return
	}
	owners, err := sqlc.New(c.pool).TenantStatusMailTargets(ctx, tenantID)
	if err != nil {
		c.log.WarnContext(ctx, "appeal mail not composed", "tenant_id", tenantID, "error", err)
		return
	}
	link := ""
	if c.appURL != "" {
		link = strings.TrimRight(c.appURL, "/") + "/app"
		if kind == usermail.KindTenantAppealRejected {
			link += "/support"
		}
	}
	for _, o := range owners {
		c.mail.Send(ctx, usermail.Mail{Kind: kind, To: o.Email, Locale: o.Locale, Link: link, Account: o.AccountName, App: o.AppName, Answer: answer})
	}
}
