package query

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Invitations lists the open invitations of an account. Only an owner or administrator sees them: a viewer is told so, a stranger finds no account.
// An invitation whose time has run out is still listed (the page shows it as expired, with a way to send it again) until somebody revokes it.
func (q *Queries) Invitations(ctx context.Context, userID, accountID string) ([]*plansv1.Invitation, error) {
	uid, ok1 := ParseID(userID)
	aid, ok2 := ParseID(accountID)
	if !ok1 || !ok2 {
		return nil, NotFound("account")
	}
	db := q.db()
	m, err := db.GetMembership(ctx, sqlc.GetMembershipParams{AccountID: aid, UserID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, NotFound("account")
	}
	if err != nil {
		return nil, convert.Err(err, "account")
	}
	if !CanManage(m.Role) {
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account does not allow this")
	}
	rows, err := db.ListOpenInvites(ctx, aid)
	if err != nil {
		return nil, convert.Err(err, "invitation")
	}
	out := make([]*plansv1.Invitation, 0, len(rows))
	for _, r := range rows {
		out = append(out, &plansv1.Invitation{Id: r.ID.String(), Email: r.Email, Role: r.Role, InvitedByEmail: r.InviterEmail, InvitedByName: r.InviterName,
			CreateTime: timestamppb.New(r.CreatedAt), SendTime: timestamppb.New(r.SentAt), ExpireTime: timestamppb.New(r.ExpiresAt)})
	}
	return out, nil
}
