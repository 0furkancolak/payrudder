package command

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Members and invitations (phase 39).
//
// Who may do what, by role:
//   - viewer: read; may leave the account.
//   - admin: invite and revoke invitations for admins and viewers, change an admin's or viewer's role between those two, remove admins and viewers.
//   - owner: everything an admin may, and also invite owners, make or unmake owners, remove owners and rename the account.
//
// An account always keeps one owner: changes that could remove the last one take a per-account lock (pg_advisory_xact_lock) so two requests cannot each see
// "another owner remains" and both go through.
const (
	// InviteTTL is how long an invitation link works.
	InviteTTL = 7 * 24 * time.Hour
	// MaxOpenInvites bounds the open invitations of one account.
	MaxOpenInvites = 50
	// MaxInvitesPerDay bounds the invitations (mails to strangers) one account makes a day.
	MaxInvitesPerDay = 30
)

func validRole(r string) bool {
	return r == query.RoleOwner || r == query.RoleAdmin || r == query.RoleViewer
}

func lastOwner() error {
	return errkey.New(codes.FailedPrecondition, "identity.member.last_owner", "an account must keep at least one owner: make someone else an owner first")
}

func notVerified() error {
	return errkey.New(codes.PermissionDenied, "identity.email.not_verified", "confirm your e-mail address first")
}

func inviteNotFound() error {
	return errkey.New(codes.NotFound, "identity.invite.not_found", "no such invitation")
}

func memberNotFound() error {
	return errkey.New(codes.NotFound, "identity.member.not_found", "no such member")
}

// actor loads the caller's role in the account inside a transaction; a caller who is not a member finds nothing.
func actor(ctx context.Context, q *sqlc.Queries, userID, accountID string) (uid, aid uuid.UUID, role, accountName string, err error) {
	uid, ok1 := query.ParseID(userID)
	aid, ok2 := query.ParseID(accountID)
	if !ok1 || !ok2 {
		return uid, aid, "", "", query.NotFound("account")
	}
	m, err := q.GetMembership(ctx, sqlc.GetMembershipParams{AccountID: aid, UserID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return uid, aid, "", "", query.NotFound("account")
	}
	if err != nil {
		return uid, aid, "", "", err
	}
	return uid, aid, m.Role, m.AccountName, nil
}

// mayHandle reports whether a caller of this role may create or cancel an invitation, or change or remove a member, whose role is target: owners only for
// owner-level targets.
func mayHandle(actorRole, target string) bool {
	if !query.CanManage(actorRole) {
		return false
	}
	return target != query.RoleOwner || actorRole == query.RoleOwner
}

// RenameAccount renames the account; only an owner may.
func (c *Commands) RenameAccount(ctx context.Context, req *plansv1.RenameMyAccountRequest) (string, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" || len(name) > 200 {
		return "", domain.Invalid("name", "required, at most 200 characters")
	}
	var out string
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		_, aid, role, _, err := actor(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if role != query.RoleOwner {
			return forbidden()
		}
		if _, err := q.RenameAccount(ctx, sqlc.RenameAccountParams{ID: aid, Name: name}); err != nil {
			return err
		}
		out = name
		return nil
	})
	if err != nil {
		return "", convert.Err(err, "account")
	}
	return out, nil
}

func invitationOf(i sqlc.AccountInvite, inviterEmail, inviterName string) *plansv1.Invitation {
	return &plansv1.Invitation{Id: i.ID.String(), Email: i.Email, Role: i.Role, InvitedByEmail: inviterEmail, InvitedByName: inviterName,
		CreateTime: timestamppb.New(i.CreatedAt), SendTime: timestamppb.New(i.SentAt), ExpireTime: timestamppb.New(i.ExpiresAt)}
}

func displayOf(name, email string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return email
}

// InviteMember mails an invitation. A second invitation to an address that has an open one refreshes it (new link, new expiry, the new role).
func (c *Commands) InviteMember(ctx context.Context, req *plansv1.InviteMemberRequest) (*plansv1.Invitation, error) {
	email := NormalizeEmail(req.GetEmail())
	var v domain.Violations
	if !validEmail(email) {
		v.Add("email", "must be a plain e-mail address")
	}
	if !validRole(req.GetRole()) {
		v.Add("role", "owner, admin or viewer")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	var token, accountName string
	var inv sqlc.AccountInvite
	var inviter sqlc.User
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, aid, role, name, err := actor(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if !mayHandle(role, req.GetRole()) {
			return forbidden()
		}
		if inviter, err = q.GetUser(ctx, uid); err != nil {
			return err
		}
		// Mail to strangers only from an address that has itself been proven.
		if inviter.EmailVerifiedAt == nil {
			return notVerified()
		}
		accountName = name
		if err := q.LockAccount(ctx, aid.String()); err != nil {
			return err
		}
		if _, err := q.GetMemberByEmail(ctx, sqlc.GetMemberByEmailParams{AccountID: aid, Email: email}); err == nil {
			return errkey.New(codes.AlreadyExists, "identity.invite.already_member", "this person is a member of the account already")
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := q.ExpireOpenInvites(ctx, sqlc.ExpireOpenInvitesParams{AccountID: aid, Email: email}); err != nil {
			return err
		}
		plain, hash, err := credential.NewPrefixedToken(credential.InvitePrefix)
		if err != nil {
			return status.Error(codes.Internal, "token generation failed")
		}
		token = plain
		expires := c.now().Add(InviteTTL)
		open, err := q.GetOpenInvite(ctx, sqlc.GetOpenInviteParams{AccountID: aid, Email: email})
		switch {
		case err == nil:
			if c.now().Sub(open.SentAt) < ResendEvery {
				return throttledInvite()
			}
			inv, err = q.RefreshInvite(ctx, sqlc.RefreshInviteParams{ID: open.ID, Role: req.GetRole(), InvitedBy: uid, TokenHash: hash, ExpiresAt: expires})
			return err
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		open2, err := q.CountOpenInvites(ctx, aid)
		if err != nil {
			return err
		}
		today, err := q.CountInvitesSince(ctx, aid)
		if err != nil {
			return err
		}
		if open2 >= MaxOpenInvites || today >= MaxInvitesPerDay {
			return errkey.New(codes.ResourceExhausted, "identity.invite.quota", "too many invitations: revoke some or wait a day")
		}
		inv, err = q.InsertInvite(ctx, sqlc.InsertInviteParams{ID: idgen.NewUUIDv7(), AccountID: aid, Email: email, Role: req.GetRole(), InvitedBy: uid, TokenHash: hash, ExpiresAt: expires})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "invitation")
	}
	locale := req.GetLocale()
	if locale == "" {
		locale = inviter.Locale
	}
	c.mail.Send(ctx, usermail.Mail{Kind: usermail.KindInvite, To: email, Locale: usermail.Locale(locale), Link: c.link("invite", token),
		Account: accountName, Inviter: displayOf(inviter.DisplayName, inviter.Email), Role: inv.Role})
	c.log.InfoContext(ctx, "member invited", "account_id", inv.AccountID, "invitation_id", inv.ID, "by", inviter.ID)
	return invitationOf(inv, inviter.Email, inviter.DisplayName), nil
}

func throttledInvite() error {
	return errkey.New(codes.ResourceExhausted, "identity.invite.throttled", "this invitation was sent a moment ago: wait a minute before sending it again")
}

// ResendInvitation mails a new link for an open invitation, at most once a minute; the old link stops working.
func (c *Commands) ResendInvitation(ctx context.Context, req *plansv1.ResendInvitationRequest) (*plansv1.Invitation, error) {
	iid, ok := query.ParseID(req.GetInvitationId())
	if !ok {
		return nil, inviteNotFound()
	}
	var token, accountName string
	var inv sqlc.AccountInvite
	var inviter sqlc.User
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, aid, role, name, err := actor(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if !query.CanManage(role) {
			return forbidden()
		}
		if inviter, err = q.GetUser(ctx, uid); err != nil {
			return err
		}
		if inviter.EmailVerifiedAt == nil {
			return notVerified()
		}
		open, err := q.GetOpenInviteInAccount(ctx, sqlc.GetOpenInviteInAccountParams{ID: iid, AccountID: aid})
		if errors.Is(err, pgx.ErrNoRows) {
			return inviteNotFound()
		}
		if err != nil {
			return err
		}
		if !mayHandle(role, open.Role) {
			return forbidden()
		}
		if c.now().Sub(open.SentAt) < ResendEvery {
			return throttledInvite()
		}
		plain, hash, err := credential.NewPrefixedToken(credential.InvitePrefix)
		if err != nil {
			return status.Error(codes.Internal, "token generation failed")
		}
		token, accountName = plain, name
		inv, err = q.RefreshInvite(ctx, sqlc.RefreshInviteParams{ID: open.ID, Role: open.Role, InvitedBy: uid, TokenHash: hash, ExpiresAt: c.now().Add(InviteTTL)})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "invitation")
	}
	c.mail.Send(ctx, usermail.Mail{Kind: usermail.KindInvite, To: inv.Email, Locale: usermail.Locale(inviter.Locale), Link: c.link("invite", token),
		Account: accountName, Inviter: displayOf(inviter.DisplayName, inviter.Email), Role: inv.Role})
	return invitationOf(inv, inviter.Email, inviter.DisplayName), nil
}

// RevokeInvitation cancels an open invitation: its link stops working.
func (c *Commands) RevokeInvitation(ctx context.Context, req *plansv1.RevokeInvitationRequest) error {
	iid, ok := query.ParseID(req.GetInvitationId())
	if !ok {
		return inviteNotFound()
	}
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		_, aid, role, _, err := actor(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if !query.CanManage(role) {
			return forbidden()
		}
		open, err := q.GetOpenInviteInAccount(ctx, sqlc.GetOpenInviteInAccountParams{ID: iid, AccountID: aid})
		if errors.Is(err, pgx.ErrNoRows) {
			return inviteNotFound()
		}
		if err != nil {
			return err
		}
		if !mayHandle(role, open.Role) {
			return forbidden()
		}
		_, err = q.RevokeInvite(ctx, sqlc.RevokeInviteParams{ID: iid, AccountID: aid})
		return err
	})
	return convert.Err(err, "invitation")
}

// UpdateMemberRole changes a member's role. The last owner cannot be demoted.
func (c *Commands) UpdateMemberRole(ctx context.Context, req *plansv1.UpdateMemberRoleRequest) (string, error) {
	if !validRole(req.GetRole()) {
		return "", domain.Invalid("role", "owner, admin or viewer")
	}
	target, ok := query.ParseID(req.GetMemberUserId())
	if !ok {
		return "", memberNotFound()
	}
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, aid, _, _, err := actor(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if err := q.LockAccount(ctx, aid.String()); err != nil {
			return err
		}
		// The caller's role is read again under the lock: it may have changed since it was first loaded.
		role, err := q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: aid, UserID: uid})
		if errors.Is(err, pgx.ErrNoRows) {
			return query.NotFound("account")
		}
		if err != nil {
			return err
		}
		current, err := q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: aid, UserID: target})
		if errors.Is(err, pgx.ErrNoRows) {
			return memberNotFound()
		}
		if err != nil {
			return err
		}
		if !mayHandle(role, current) || !mayHandle(role, req.GetRole()) {
			return forbidden()
		}
		if current == query.RoleOwner && req.GetRole() != query.RoleOwner {
			owners, err := q.CountOwners(ctx, aid)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return lastOwner()
			}
		}
		_, err = q.SetMemberRole(ctx, sqlc.SetMemberRoleParams{AccountID: aid, UserID: target, Role: req.GetRole()})
		return err
	})
	if err != nil {
		return "", convert.Err(err, "member")
	}
	c.log.InfoContext(ctx, "member role changed", "account_id", req.GetAccountId(), "member", target, "role", req.GetRole(), "by", req.GetUserId())
	return req.GetRole(), nil
}

// RemoveMember removes a member. A member may always remove themselves (leave); the last owner may not go.
func (c *Commands) RemoveMember(ctx context.Context, req *plansv1.RemoveMemberRequest) error {
	target, ok := query.ParseID(req.GetMemberUserId())
	if !ok {
		return memberNotFound()
	}
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, aid, _, _, err := actor(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if err := q.LockAccount(ctx, aid.String()); err != nil {
			return err
		}
		role, err := q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: aid, UserID: uid})
		if errors.Is(err, pgx.ErrNoRows) {
			return query.NotFound("account")
		}
		if err != nil {
			return err
		}
		current, err := q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: aid, UserID: target})
		if errors.Is(err, pgx.ErrNoRows) {
			return memberNotFound()
		}
		if err != nil {
			return err
		}
		if target != uid && !mayHandle(role, current) {
			return forbidden()
		}
		if current == query.RoleOwner {
			owners, err := q.CountOwners(ctx, aid)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return lastOwner()
			}
		}
		_, err = q.DeleteMember(ctx, sqlc.DeleteMemberParams{AccountID: aid, UserID: target})
		return err
	})
	if err != nil {
		return convert.Err(err, "member")
	}
	c.log.InfoContext(ctx, "member removed", "account_id", req.GetAccountId(), "member", target, "by", req.GetUserId())
	return nil
}

// LookupInvitation says what a live invitation token is for.
func (c *Commands) LookupInvitation(ctx context.Context, token string) (*plansv1.LookupInvitationResponse, error) {
	if !credential.WellFormed(credential.InvitePrefix, token) {
		return nil, invalidToken()
	}
	db := sqlc.New(c.pool)
	inv, err := db.GetInviteByToken(ctx, credential.HashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, invalidToken()
	}
	if err != nil {
		return nil, convert.Err(err, "invitation")
	}
	resp := &plansv1.LookupInvitationResponse{AccountName: inv.AccountName, Email: inv.Email, Role: inv.Role}
	if by, err := db.GetUser(ctx, inv.InvitedBy); err == nil {
		resp.InvitedByName = displayOf(by.DisplayName, by.Email)
	}
	if _, err := db.GetUserByEmail(ctx, inv.Email); err == nil {
		resp.HasAccount = true
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, convert.Err(err, "user")
	}
	return resp, nil
}

func signInRequired() error {
	return errkey.New(codes.FailedPrecondition, "identity.invite.sign_in_required", "this address already has an account: sign in to accept the invitation")
}

// AcceptInvitation makes a new user of the invited address, a member of the account, and opens a session. The address counts as verified: the token
// that proves it came by mail. An address that has a user already is told to sign in.
func (c *Commands) AcceptInvitation(ctx context.Context, req *plansv1.AcceptInvitationRequest) (Opened, error) {
	display := strings.TrimSpace(req.GetDisplayName())
	var v domain.Violations
	if n := len(req.GetPassword()); n < credential.MinPasswordLen || n > credential.MaxPasswordLen {
		v.Add("password", "must be between 10 and 256 characters")
	}
	if len(display) > 200 {
		v.Add("display_name", "at most 200 characters")
	}
	if err := v.Err(); err != nil {
		return Opened{}, err
	}
	if !credential.WellFormed(credential.InvitePrefix, req.GetToken()) {
		return Opened{}, invalidToken()
	}
	hashOfToken := credential.HashToken(req.GetToken())
	db := sqlc.New(c.pool)
	inv, err := db.GetInviteByToken(ctx, hashOfToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, invalidToken()
	}
	if err != nil {
		return Opened{}, convert.Err(err, "invitation")
	}
	if _, err := db.GetUserByEmail(ctx, inv.Email); err == nil {
		return Opened{}, signInRequired()
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, convert.Err(err, "user")
	}
	var pw string
	var herr error
	if err := c.withSlot(ctx, func() { pw, herr = credential.Hash(req.GetPassword()) }); err != nil {
		return Opened{}, err
	}
	if herr != nil {
		return Opened{}, status.Error(codes.Internal, "password hashing failed")
	}
	session, sessionHash, err := credential.NewToken()
	if err != nil {
		return Opened{}, status.Error(codes.Internal, "token generation failed")
	}
	var out Opened
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		n, err := q.AcceptInvite(ctx, inv.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return invalidToken()
		}
		u, err := q.InsertVerifiedUser(ctx, sqlc.InsertVerifiedUserParams{ID: idgen.NewUUIDv7(), Email: inv.Email, DisplayName: display, PasswordHash: pw,
			Locale: usermail.Locale(req.GetLocale())})
		if err != nil {
			return err
		}
		if err := q.InsertMember(ctx, sqlc.InsertMemberParams{AccountID: inv.AccountID, UserID: u.ID, Role: inv.Role}); err != nil {
			return err
		}
		now := c.now()
		expires := query.SlideTo(now, now)
		if _, err := q.InsertSession(ctx, sqlc.InsertSessionParams{ID: idgen.NewUUIDv7(), UserID: u.ID, TokenHash: sessionHash, ExpiresAt: expires}); err != nil {
			return err
		}
		ms, err := query.Memberships(ctx, q, u.ID)
		if err != nil {
			return err
		}
		out = Opened{User: query.User(u), Memberships: ms, Token: session, ExpireTime: expires}
		return nil
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		return Opened{}, signInRequired()
	}
	if err != nil {
		return Opened{}, convert.Err(err, "invitation")
	}
	c.log.InfoContext(ctx, "invitation accepted by a new user", "user_id", out.User.GetId(), "account_id", inv.AccountID)
	return out, nil
}

// AcceptInvitationAsUser adds the signed-in user to the account. The invitation must have been sent to the user's own address: a token for somebody else's
// address cannot be used by another account.
func (c *Commands) AcceptInvitationAsUser(ctx context.Context, req *plansv1.AcceptInvitationAsUserRequest) (*plansv1.AcceptInvitationAsUserResponse, error) {
	uid, ok := query.ParseID(req.GetUserId())
	if !ok {
		return nil, query.ErrNoSession()
	}
	if !credential.WellFormed(credential.InvitePrefix, req.GetToken()) {
		return nil, invalidToken()
	}
	var out *plansv1.AcceptInvitationAsUserResponse
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		inv, err := q.GetInviteByToken(ctx, credential.HashToken(req.GetToken()))
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidToken()
		}
		if err != nil {
			return err
		}
		u, err := q.GetUser(ctx, uid)
		if err != nil {
			return err
		}
		if NormalizeEmail(u.Email) != NormalizeEmail(inv.Email) {
			return errkey.New(codes.PermissionDenied, "identity.invite.wrong_user", "this invitation was sent to another address: sign in with that address")
		}
		n, err := q.AcceptInvite(ctx, inv.ID)
		if err != nil {
			return err
		}
		if n == 0 {
			return invalidToken()
		}
		if _, err := q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: inv.AccountID, UserID: uid}); errors.Is(err, pgx.ErrNoRows) {
			if err := q.InsertMember(ctx, sqlc.InsertMemberParams{AccountID: inv.AccountID, UserID: uid, Role: inv.Role}); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := q.MarkEmailVerified(ctx, uid); err != nil {
			return err
		}
		ms, err := query.Memberships(ctx, q, uid)
		if err != nil {
			return err
		}
		out = &plansv1.AcceptInvitationAsUserResponse{Memberships: ms, AccountId: inv.AccountID.String()}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "invitation")
	}
	c.log.InfoContext(ctx, "invitation accepted", "user_id", uid, "account_id", out.GetAccountId())
	return out, nil
}
