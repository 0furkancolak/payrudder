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
	saasmodel "github.com/0furkancolak/payrudder/services/plans-svc/internal/saas/model"
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
	uid, aid, m, err := query.Membership(ctx, q, userID, accountID)
	return uid, aid, m.Role, m.AccountName, err
}

// mayHandle reports whether a caller of this role may create or cancel an invitation, or change or remove a member, whose role is target: owners only for
// owner-level targets.
func mayHandle(actorRole, target string) bool {
	if !query.CanManage(actorRole) || target == query.RoleAffiliate {
		// A partner is managed in the affiliate program, not as a member.
		return false
	}
	return target != query.RoleOwner || actorRole == query.RoleOwner
}

// MaxOwnedAccounts bounds how many accounts one person may create and own.
const MaxOwnedAccounts = 20

// CreateAccount makes another account that the user owns. The person must be a user already; the new account starts empty (no apps).
func (c *Commands) CreateAccount(ctx context.Context, req *plansv1.CreateMyAccountRequest) (*plansv1.Membership, error) {
	name := strings.TrimSpace(req.GetName())
	if name == "" || len(name) > 200 {
		return nil, domain.Invalid("name", "required, at most 200 characters")
	}
	uid, ok := query.ParseID(req.GetUserId())
	if !ok {
		return nil, query.NotFound("account")
	}
	var out *plansv1.Membership
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.GetUser(ctx, uid); err != nil {
			return err
		}
		owned, err := q.ListUserMemberships(ctx, uid)
		if err != nil {
			return err
		}
		n := 0
		for _, m := range owned {
			if m.Role == query.RoleOwner {
				n++
			}
		}
		if n >= MaxOwnedAccounts {
			return errkey.Newf(codes.ResourceExhausted, "identity.account.quota", "a person can own at most %d accounts", MaxOwnedAccounts)
		}
		a, err := q.InsertAccount(ctx, sqlc.InsertAccountParams{ID: idgen.NewUUIDv7(), Name: name})
		if err != nil {
			return err
		}
		if err := q.InsertMember(ctx, sqlc.InsertMemberParams{AccountID: a.ID, UserID: uid, Role: query.RoleOwner}); err != nil {
			return err
		}
		out = &plansv1.Membership{AccountId: a.ID.String(), AccountName: name, Role: query.RoleOwner}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "account")
	}
	c.log.InfoContext(ctx, "account created by member", "user_id", uid, "account_id", out.GetAccountId())
	return out, nil
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
		inv, err = c.placeInvite(ctx, tx, newInvite{account: aid, inviter: uid, email: email, role: req.GetRole(), tokenHash: hash, expires: c.now().Add(InviteTTL)})
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
		Account: accountName, Inviter: query.DisplayOf(inviter.DisplayName, inviter.Email), Role: inv.Role})
	c.log.InfoContext(ctx, "member invited", "account_id", inv.AccountID, "invitation_id", inv.ID, "by", inviter.ID)
	return invitationOf(inv, inviter.Email, inviter.DisplayName), nil
}

// newInvite is an invitation about to be stored.
type newInvite struct {
	account, inviter uuid.UUID
	email, role      string
	tokenHash        []byte
	expires          time.Time
}

// placeInvite stores the invitation: a fresh link on the address's open one (at most once a minute), or, when there is none, a new one that holds a seat of the
// plan and counts against the limits on open and daily invitations.
func (c *Commands) placeInvite(ctx context.Context, tx pgx.Tx, in newInvite) (sqlc.AccountInvite, error) {
	q := sqlc.New(tx)
	open, err := q.GetOpenInvite(ctx, sqlc.GetOpenInviteParams{AccountID: in.account, Email: in.email})
	switch {
	case err == nil:
		if c.now().Sub(open.SentAt) < ResendEvery {
			return sqlc.AccountInvite{}, throttledInvite()
		}
		return q.RefreshInvite(ctx, sqlc.RefreshInviteParams{ID: open.ID, Role: in.role, InvitedBy: in.inviter, TokenHash: in.tokenHash, ExpiresAt: in.expires})
	case !errors.Is(err, pgx.ErrNoRows):
		return sqlc.AccountInvite{}, err
	}
	// A new invitation holds a seat of the plan; refreshing one (above) does not take another.
	if err := c.room(ctx, tx, in.account, saasmodel.Members); err != nil {
		return sqlc.AccountInvite{}, err
	}
	openCount, err := q.CountOpenInvites(ctx, in.account)
	if err != nil {
		return sqlc.AccountInvite{}, err
	}
	today, err := q.CountInvitesSince(ctx, in.account)
	if err != nil {
		return sqlc.AccountInvite{}, err
	}
	if openCount >= MaxOpenInvites || today >= MaxInvitesPerDay {
		return sqlc.AccountInvite{}, errkey.New(codes.ResourceExhausted, "identity.invite.quota", "too many invitations: revoke some or wait a day")
	}
	return q.InsertInvite(ctx, sqlc.InsertInviteParams{ID: idgen.NewUUIDv7(), AccountID: in.account, Email: in.email, Role: in.role, InvitedBy: in.inviter, TokenHash: in.tokenHash, ExpiresAt: in.expires})
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
		Account: accountName, Inviter: query.DisplayOf(inviter.DisplayName, inviter.Email), Role: inv.Role})
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

// lockedRoles locks the account against other changes of its members and returns the caller's role and the role of the member they act on, both read under
// the lock (the caller's may have changed since actor loaded it). A caller who is not a member finds no account, a target who is not one finds no member.
func lockedRoles(ctx context.Context, q *sqlc.Queries, userID, accountID string, target uuid.UUID) (uid, aid uuid.UUID, role, targetRole string, err error) {
	uid, aid, _, _, err = actor(ctx, q, userID, accountID)
	if err != nil {
		return uid, aid, "", "", err
	}
	if err := q.LockAccount(ctx, aid.String()); err != nil {
		return uid, aid, "", "", err
	}
	role, err = q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: aid, UserID: uid})
	if errors.Is(err, pgx.ErrNoRows) {
		return uid, aid, "", "", query.NotFound("account")
	}
	if err != nil {
		return uid, aid, "", "", err
	}
	targetRole, err = q.GetMemberRole(ctx, sqlc.GetMemberRoleParams{AccountID: aid, UserID: target})
	if errors.Is(err, pgx.ErrNoRows) {
		return uid, aid, role, "", memberNotFound()
	}
	return uid, aid, role, targetRole, err
}

// requireAnotherOwner refuses a change that would leave the account without an owner: the member it is about is the last one.
func requireAnotherOwner(ctx context.Context, q *sqlc.Queries, aid uuid.UUID) error {
	owners, err := q.CountOwners(ctx, aid)
	if err != nil {
		return err
	}
	if owners <= 1 {
		return lastOwner()
	}
	return nil
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
		_, aid, role, current, err := lockedRoles(ctx, q, req.GetUserId(), req.GetAccountId(), target)
		if err != nil {
			return err
		}
		if !mayHandle(role, current) || !mayHandle(role, req.GetRole()) {
			return forbidden()
		}
		if current == query.RoleOwner && req.GetRole() != query.RoleOwner {
			if err := requireAnotherOwner(ctx, q, aid); err != nil {
				return err
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
		uid, aid, role, current, err := lockedRoles(ctx, q, req.GetUserId(), req.GetAccountId(), target)
		if err != nil {
			return err
		}
		if target != uid && !mayHandle(role, current) {
			return forbidden()
		}
		if current == query.RoleOwner {
			if err := requireAnotherOwner(ctx, q, aid); err != nil {
				return err
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

func signInRequired() error {
	return errkey.New(codes.FailedPrecondition, "identity.invite.sign_in_required", "this address already has an account: sign in to accept the invitation")
}

// AcceptInvitation makes a new user of the invited address, a member of the account, and opens a session. The address counts as verified: the token
// that proves it came by mail. An address that has a user already is told to sign in.
func (c *Commands) AcceptInvitation(ctx context.Context, req *plansv1.AcceptInvitationRequest) (Opened, error) {
	display := strings.TrimSpace(req.GetDisplayName())
	var v domain.Violations
	checkPassword(&v, "password", req.GetPassword())
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
	pw, err := c.hashPassword(ctx, req.GetPassword())
	if err != nil {
		return Opened{}, err
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
