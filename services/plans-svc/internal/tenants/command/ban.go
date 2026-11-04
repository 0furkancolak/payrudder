package command

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Bans of an account and of a person (plan 14). An account ban makes every live tenant of the account behave as banned (derived on read, see convert.AccountBan: no
// tenant row is rewritten, so lifting restores each tenant as it was); a person ban refuses the person's sign-in and every live session. Both need a reason, both are kept in
// ban_events, and both wait for a second operator when the ban approval is on (the same switch as a tenant's ban).

const (
	subjectAccount = "account"
	subjectUser    = "user"
)

type banTarget struct {
	subject string
	id      uuid.UUID
	banned  bool
}

// lockBanTarget finds the subject named by the request, locked for the change.
func lockBanTarget(ctx context.Context, q *sqlc.Queries, accountID, email string) (banTarget, error) {
	if (accountID == "") == (email == "") {
		return banTarget{}, domain.Invalid("account_id", "name exactly one of account_id and email")
	}
	if accountID != "" {
		id, err := domain.ParseID("account_id", accountID)
		if err != nil {
			return banTarget{}, err
		}
		a, err := q.LockAccountForBan(ctx, id)
		return banTarget{subjectAccount, a.ID, a.BannedAt != nil}, err
	}
	u, err := q.LockUserForBan(ctx, strings.TrimSpace(email))
	return banTarget{subjectUser, u.ID, u.BannedAt != nil}, err
}

// SetBan bans or lifts the ban of an account or a person.
func (c *Commands) SetBan(ctx context.Context, req *plansv1.SetBanRequest) (*plansv1.SetBanResponse, error) {
	reason := strings.TrimSpace(req.GetReason())
	if reason == "" {
		return nil, errkey.New(codes.InvalidArgument, "plans.tenant.reason_required", "a ban and lifting a ban need a reason")
	}
	if utf8.RuneCountInString(reason) > 500 {
		return nil, domain.Invalid("reason", "at most 500 characters")
	}
	if cat := req.GetCategory(); cat != "" && !slices.Contains(StatusCategories, cat) {
		return nil, domain.Invalid("category", "nonpayment, fraud, abuse, legal, request or other")
	}
	actor := strings.TrimSpace(req.GetActor())
	if actor == "" {
		actor = "operator"
	}
	if r := []rune(actor); len(r) > maxActorRunes {
		actor = string(r[:maxActorRunes])
	}
	var (
		target  banTarget
		pending bool
		tenants []uuid.UUID
	)
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var err error
		if target, err = lockBanTarget(ctx, q, req.GetAccountId(), req.GetEmail()); err != nil {
			return err
		}
		if target.banned == req.GetBanned() {
			return nil
		}
		ev := sqlc.InsertBanEventParams{ID: idgen.NewUUIDv7(), Subject: target.subject, SubjectID: target.id, Kind: "lifted", Category: req.GetCategory(), Reason: reason, Actor: actor}
		if req.GetBanned() {
			ev.Kind = "banned"
			if c.banApproval {
				if ev.ApprovedBy, pending, err = c.banApprover(ctx, q, target, actor); err != nil {
					return err
				}
				if pending {
					ev.Kind, ev.ApprovedBy = "requested", ""
				}
			}
		}
		if err := q.InsertBanEvent(ctx, ev); err != nil {
			return err
		}
		if !pending {
			if err := applyBan(ctx, q, target, req.GetBanned()); err != nil {
				return err
			}
			if target.subject == subjectAccount {
				if tenants, err = q.ListAccountTenantIDs(ctx, target.id); err != nil {
					return err
				}
			}
		}
		e := audit.Event{Action: "ban." + ev.Kind, TargetType: target.subject, TargetID: target.id.String(), Summary: ev.Kind + " the " + target.subject,
			Detail: map[string]any{"category": req.GetCategory(), "reason": reason}}
		if target.subject == subjectAccount {
			e.AccountID = target.id
		}
		return audit.Record(ctx, tx, e)
	})
	if err != nil {
		return nil, convert.Err(err, "subject")
	}
	if pending {
		return nil, errkey.New(codes.FailedPrecondition, "plans.tenant.approval_pending", "a ban needs a second operator: the request is recorded, ask another operator to make the same request")
	}
	c.log.WarnContext(ctx, "ban set by an operator", "subject", target.subject, "id", target.id, "banned", req.GetBanned(), "category", req.GetCategory(), "reason", reason, "actor", actor)
	// The gateway resolves the keys of the account's tenants again: a status it had cached is not the status any more.
	for _, id := range tenants {
		if err := c.keys.EvictTenant(ctx, id); err != nil {
			return nil, err
		}
	}
	return &plansv1.SetBanResponse{Banned: req.GetBanned()}, nil
}

// applyBan sets or clears the flag; banning a person also ends every session they have.
func applyBan(ctx context.Context, q *sqlc.Queries, t banTarget, banned bool) error {
	if t.subject == subjectAccount {
		return q.SetAccountBan(ctx, sqlc.SetAccountBanParams{ID: t.id, Banned: banned})
	}
	if err := q.SetUserBan(ctx, sqlc.SetUserBanParams{ID: t.id, Banned: banned}); err != nil || !banned {
		return err
	}
	return q.RevokeUserSessions(ctx, t.id)
}

// banApprover decides whether a ban the actor asks for is the second request for it: the subject's latest event is another operator's request, made within the window.
func (c *Commands) banApprover(ctx context.Context, q *sqlc.Queries, t banTarget, actor string) (approver string, pending bool, err error) {
	last, err := q.LatestBanEvent(ctx, sqlc.LatestBanEventParams{Subject: t.subject, SubjectID: t.id})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if last.Kind == "requested" && last.Actor != actor && c.now().Sub(last.CreatedAt) < approvalWindow {
		return last.Actor, false, nil
	}
	return "", true, nil
}

// BanEvents says whether the account or person is banned and returns its latest bans, lifts and requests, newest first.
func (c *Commands) BanEvents(ctx context.Context, req *plansv1.ListBanEventsRequest) (*plansv1.ListBanEventsResponse, error) {
	if (req.GetAccountId() == "") == (req.GetEmail() == "") {
		return nil, domain.Invalid("account_id", "name exactly one of account_id and email")
	}
	var (
		subject = subjectUser
		id      uuid.UUID
		banned  bool
		err     error
	)
	q := sqlc.New(c.pool)
	if req.GetAccountId() != "" {
		subject = subjectAccount
		if id, err = domain.ParseID("account_id", req.GetAccountId()); err != nil {
			return nil, err
		}
		banned, err = q.AccountIsBanned(ctx, id)
	} else {
		var u sqlc.GetUserBannedByEmailRow
		u, err = q.GetUserBannedByEmail(ctx, strings.TrimSpace(req.GetEmail()))
		id, banned = u.ID, u.BannedAt != nil
	}
	if err != nil {
		return nil, convert.Err(err, subject)
	}
	rows, err := q.ListBanEvents(ctx, sqlc.ListBanEventsParams{Subject: subject, SubjectID: id})
	if err != nil {
		return nil, convert.Err(err, "ban")
	}
	out := &plansv1.ListBanEventsResponse{Banned: banned, Events: make([]*plansv1.BanEvent, 0, len(rows))}
	for _, e := range rows {
		out.Events = append(out.Events, &plansv1.BanEvent{Id: e.ID.String(), Kind: e.Kind, Category: e.Category, Reason: e.Reason, Actor: e.Actor, ApprovedBy: e.ApprovedBy, CreateTime: timestamppb.New(e.CreatedAt)})
	}
	return out, nil
}
