package command

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// The appeal of a blocked customer (docs/plans/13-customer-ban.md). The customer sends one statement while the block is in force; the seller accepts (the block is lifted by
// liftBlock, so the history row, the audit entry, customer.unblocked and the customer's notice all happen as for the seller's own lift) or rejects with an answer.

const (
	maxAppealStatement = 2000
	maxAppealAnswer    = 1000
)

// SubmitAppeal records the statement of a customer whose block is in force. One appeal is open at a time (a partial unique index); a customer who is not blocked has nothing
// to appeal. The statement is the customer's words and is kept as written (trimmed), never interpreted.
func (c *Commands) SubmitAppeal(ctx context.Context, req *plansv1.SubmitCustomerBlockAppealRequest) (*plansv1.CustomerBlockAppeal, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	statement := strings.TrimSpace(strings.ReplaceAll(req.GetStatement(), "\x00", ""))
	switch n := utf8.RuneCountInString(statement); {
	case n == 0:
		return nil, domain.Invalid("statement", "required")
	case n > maxAppealStatement:
		return nil, domain.Invalid("statement", "at most 2000 characters")
	}
	now := c.now()
	var out sqlc.CustomerBlockAppeal
	err := c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		if !convert.BlockOf(cur).InForce(now) {
			return errkey.New(codes.FailedPrecondition, "plans.customer_appeal.not_blocked", "the customer is not blocked")
		}
		if out, err = q.InsertCustomerBlockAppeal(ctx, sqlc.InsertCustomerBlockAppealParams{ID: idgen.NewUUIDv7(), CustomerID: cur.ID, Statement: statement}); err != nil {
			if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
				return errkey.New(codes.AlreadyExists, "plans.customer_appeal.exists", "an appeal of this customer is open already")
			}
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: cur.TenantID, Action: "customer_block.appeal_submitted", TargetType: "customer", TargetID: cur.ID.String(),
			Summary: "the customer " + cur.ExternalID + " appealed the block", Detail: map[string]any{"external_id": cur.ExternalID, "appeal_id": out.ID.String()}})
	})
	if err != nil {
		return nil, convert.Err(err, "appeal")
	}
	return convert.CustomerBlockAppeal(out), nil
}

// DecideAppeal accepts or rejects an open appeal of the customer. Accepting lifts the block in the same transaction as the decision, so a decision never exists without
// the lift (and the other way round). Rejecting needs an answer: a customer who is refused is owed a sentence. The block stays; the customer may appeal again.
func (c *Commands) DecideAppeal(ctx context.Context, req *plansv1.DecideCustomerBlockAppealRequest) (*plansv1.CustomerBlockAppeal, error) {
	if req.GetExternalId() == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	id, err := domain.ParseID("appeal_id", req.GetAppealId())
	if err != nil {
		return nil, err
	}
	decision := req.GetDecision()
	if decision != "accepted" && decision != "rejected" {
		return nil, domain.Invalid("decision", "accepted or rejected")
	}
	answer := strings.TrimSpace(strings.ReplaceAll(req.GetAnswer(), "\x00", ""))
	if utf8.RuneCountInString(answer) > maxAppealAnswer {
		return nil, domain.Invalid("answer", "at most 1000 characters")
	}
	if decision == "rejected" && answer == "" {
		return nil, domain.Invalid("answer", "required to reject an appeal")
	}
	actor := actorOf(req.GetActor())
	now := c.now()
	var out sqlc.CustomerBlockAppeal
	err = c.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		cur, err := q.LockCustomerByExternalID(ctx, req.GetExternalId())
		if err != nil {
			return err
		}
		ap, err := q.GetCustomerBlockAppealForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if ap.CustomerID != cur.ID {
			return pgx.ErrNoRows // another customer's appeal is not found under this one
		}
		if ap.Status != "open" {
			return errkey.New(codes.FailedPrecondition, "plans.customer_appeal.not_open", "this appeal was decided already")
		}
		if out, err = q.DecideCustomerBlockAppeal(ctx, sqlc.DecideCustomerBlockAppealParams{ID: id, Status: decision, Answer: answer, DecidedBy: actor}); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{TenantID: cur.TenantID, Action: "customer_block.appeal_decided", TargetType: "customer", TargetID: cur.ID.String(),
			Summary: decision + " the appeal of the customer " + cur.ExternalID, Detail: map[string]any{"external_id": cur.ExternalID, "appeal_id": id.String(), "decision": decision}}); err != nil {
			return err
		}
		if decision == "accepted" && cur.BlockedAt != nil {
			_, err = liftBlock(ctx, q, tx, cur, now, actor, "lifted the block on the customer (appeal accepted) ")
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "appeal")
	}
	return convert.CustomerBlockAppeal(out), nil
}
