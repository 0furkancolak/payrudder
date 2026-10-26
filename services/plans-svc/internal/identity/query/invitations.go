package query

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
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

// InvalidToken is every bad link of a mail: malformed, unknown, used or expired. One error, so a caller learns nothing about which.
func InvalidToken() error {
	return errkey.New(codes.InvalidArgument, "identity.token.invalid", "this link is invalid, has expired or was already used")
}

// DisplayOf is the name shown for a person: their display name, or their e-mail when they gave none.
func DisplayOf(name, email string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return email
}

// LookupInvitation says what a live invitation token is for.
func (q *Queries) LookupInvitation(ctx context.Context, token string) (*plansv1.LookupInvitationResponse, error) {
	if !credential.WellFormed(credential.InvitePrefix, token) {
		return nil, InvalidToken()
	}
	db := q.db()
	inv, err := db.GetInviteByToken(ctx, credential.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, InvalidToken()
	}
	if err != nil {
		return nil, convert.Err(err, "invitation")
	}
	resp := &plansv1.LookupInvitationResponse{AccountName: inv.AccountName, Email: inv.Email, Role: inv.Role}
	if by, err := db.GetUser(ctx, inv.InvitedBy); err == nil {
		resp.InvitedByName = DisplayOf(by.DisplayName, by.Email)
	}
	if _, err := db.GetUserByEmail(ctx, inv.Email); err == nil {
		resp.HasAccount = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, convert.Err(err, "user")
	}
	return resp, nil
}
