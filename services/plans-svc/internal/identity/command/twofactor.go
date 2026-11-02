package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/totp"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Two-factor sign-in (plan 22, G9). A person with an authenticator app confirmed gets no session from the first step of any sign-in (password, mailed code, Google): they
// get a challenge, and the session comes from VerifyTwoFactor. Every try at the second step counts against the lock of the account like a wrong password (five in a row
// lock it for fifteen minutes) and against the five tries of the challenge; the gateway limits them per address too.
const (
	// ChallengeTTL is how long the second step may take.
	ChallengeTTL = 5 * time.Minute
	// MaxChallengeAttempts is how many codes a challenge survives.
	MaxChallengeAttempts = 5
	// Issuer is the name an authenticator app lists the account under.
	Issuer = "Payrudder"
)

// WithBox sets the box that seals the authenticator secrets (the platform's secrets key). Without one nobody can set two-factor sign-in up.
func (c *Commands) WithBox(b *secretbox.Box) *Commands {
	c.box = b
	return c
}

func totpAAD(userID uuid.UUID) []byte { return []byte("totp|" + userID.String()) }

// invalidSecondFactor is every failure of the second step of a sign-in: a wrong, reused or expired code, a spent challenge, a locked account. One answer.
func invalidSecondFactor() error {
	return errkey.New(codes.Unauthenticated, "identity.two_factor.invalid", "wrong or expired code, or too many attempts: sign in again")
}

// badCode is a wrong code at a step the person is signed in for (turning it off, new recovery codes, confirming the setup).
func badCode() error {
	return errkey.New(codes.InvalidArgument, "identity.two_factor.bad_code", "wrong code: type the six digits the authenticator app shows now, or a recovery code")
}

func (c *Commands) requireBox() error {
	if c.box == nil {
		return errkey.New(codes.Unavailable, "identity.two_factor.not_configured", "two-factor sign-in is not available on this deployment (PAYRUDDER_SECRETS_KEY is not set)")
	}
	return nil
}

// openOrChallenge ends the first step of a sign-in, inside the caller's transaction: a session for a person without two-factor sign-in, a challenge for one with it.
func (c *Commands) openOrChallenge(ctx context.Context, q *sqlc.Queries, user sqlc.User) (Opened, error) {
	t, err := q.GetUserTotp(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.ConfirmedAt == nil) {
		return c.openSession(ctx, q, user)
	}
	if err != nil {
		return Opened{}, err
	}
	token, hash, err := credential.NewPrefixedToken(credential.ChallengePrefix)
	if err != nil {
		return Opened{}, status.Error(codes.Internal, "token generation failed")
	}
	if err := q.DeleteStaleLoginChallenges(ctx, user.ID); err != nil {
		return Opened{}, err
	}
	expires := c.now().Add(ChallengeTTL)
	if err := q.InsertLoginChallenge(ctx, sqlc.InsertLoginChallengeParams{ID: idgen.NewUUIDv7(), UserID: user.ID, TokenHash: hash, ExpiresAt: expires}); err != nil {
		return Opened{}, err
	}
	return Opened{Challenge: token, ChallengeExpire: expires}, nil // no user: nothing about the person is shown before the second step
}

// who is the actor of an audit entry about the user's own sign-in security.
func who(u sqlc.User) *audit.Actor {
	return &audit.Actor{Type: audit.ActorUser, ID: u.ID.String(), Email: u.Email}
}

// VerifyTwoFactor is the second step of a sign-in. The tries are counted before the code is looked at, in statements of their own, so a failure keeps its count.
func (c *Commands) VerifyTwoFactor(ctx context.Context, req *plansv1.VerifyTwoFactorRequest) (Opened, error) {
	if !credential.WellFormed(credential.ChallengePrefix, req.GetChallenge()) || req.GetCode() == "" || len(req.GetCode()) > 64 {
		return Opened{}, invalidSecondFactor()
	}
	db := sqlc.New(c.pool)
	ch, err := db.TryLoginChallenge(ctx, sqlc.TryLoginChallengeParams{TokenHash: credential.HashToken(req.GetChallenge()), MaxAttempts: MaxChallengeAttempts})
	if errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, invalidSecondFactor()
	}
	if err != nil {
		return Opened{}, convert.Err(err, "challenge")
	}
	user, err := db.TryTwoFactor(ctx, sqlc.TryTwoFactorParams{ID: ch.UserID, MaxFailures: MaxFailures, LockFor: convert.Interval(LockFor)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Opened{}, invalidSecondFactor() // locked
	}
	if err != nil {
		return Opened{}, convert.Err(err, "user")
	}
	spend, recovery, err := c.codeSpender(ctx, db, user, req.GetCode())
	if err != nil {
		if errors.Is(err, errBadCode) {
			if user.LockedUntil != nil {
				c.log.WarnContext(ctx, "sign-in locked after repeated failures at the second step", "user_id", user.ID, "failures", user.FailedLogins)
			}
			return Opened{}, invalidSecondFactor()
		}
		return Opened{}, err
	}
	var out Opened
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := spend(ctx, tx); err != nil {
			return err
		}
		if n, err := q.DeleteLoginChallenge(ctx, ch.ID); err != nil {
			return err
		} else if n == 0 {
			return errBadCode // spent by a parallel request
		}
		var err error
		out, err = c.openSession(ctx, q, user)
		return err
	})
	if errors.Is(err, errBadCode) {
		return Opened{}, invalidSecondFactor()
	}
	if err != nil {
		return Opened{}, convert.Err(err, "session")
	}
	c.log.InfoContext(ctx, "signed in with a second factor", "user_id", user.ID)
	if recovery {
		c.tell(ctx, user, usermail.KindRecoveryUsed)
	}
	return out, nil
}

var errBadCode = errors.New("two-factor code refused")

// codeSpender checks a code against the user's confirmed authenticator secret or recovery codes and returns what spends it: a statement that must run in the
// transaction that uses the proof (it is atomic, so a code works once). errBadCode says the code is wrong. A recovery code leaves an entry in the audit log, and
// recovery is true for it: the caller mails the person once the transaction has committed (tell).
func (c *Commands) codeSpender(ctx context.Context, db *sqlc.Queries, user sqlc.User, code string) (spend func(context.Context, pgx.Tx) error, recovery bool, err error) {
	t, err := db.GetUserTotp(ctx, user.ID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t.ConfirmedAt == nil) {
		return nil, false, errBadCode
	}
	if err != nil {
		return nil, false, convert.Err(err, "user")
	}
	if credential.WellFormedCode(code) {
		secret, err := c.openSecret(user.ID, t.SecretEnc)
		if err != nil {
			c.log.ErrorContext(ctx, "authenticator secret cannot be opened", "user_id", user.ID, "err", err)
			return nil, false, errBadCode
		}
		step, ok := totp.Match(secret, code, c.now(), t.LastStep)
		if !ok {
			return nil, false, errBadCode
		}
		return func(ctx context.Context, tx pgx.Tx) error {
			if n, err := sqlc.New(tx).UseTotpStep(ctx, sqlc.UseTotpStepParams{UserID: user.ID, Step: step}); err != nil {
				return err
			} else if n == 0 {
				return errBadCode // the same code, used by a parallel request
			}
			return nil
		}, false, nil
	}
	norm, ok := totp.NormalizeRecovery(code)
	if !ok {
		return nil, false, errBadCode
	}
	hash := credential.HashRecovery(c.pepper, user.ID.String(), norm)
	return func(ctx context.Context, tx pgx.Tx) error {
		q := sqlc.New(tx)
		n, err := q.UseRecoveryCode(ctx, sqlc.UseRecoveryCodeParams{UserID: user.ID, CodeHash: hash})
		if err != nil {
			return err
		}
		if n == 0 {
			return errBadCode
		}
		left, err := q.CountRecoveryCodes(ctx, user.ID)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{By: who(user), Action: "two_factor.recovery_code_used", TargetType: "user", TargetID: user.ID.String(),
			Summary: fmt.Sprintf("signed in with a recovery code (%d left)", left), Detail: map[string]any{"codes_left": left}})
	}, true, nil
}

// tell mails the person that their two-factor sign-in changed: on, off, a recovery code used, or reset by support. The mail has no link and no secret, and goes
// after the change has committed; a lost mail is not a failed change (usermail logs it).
func (c *Commands) tell(ctx context.Context, u sqlc.User, kind usermail.Kind) {
	c.mail.Send(ctx, usermail.Mail{Kind: kind, To: u.Email, Locale: u.Locale})
}

func (c *Commands) openSecret(userID uuid.UUID, sealed []byte) ([]byte, error) {
	if c.box == nil {
		return nil, errors.New("no secrets key")
	}
	return c.box.Open(sealed, totpAAD(userID))
}

// TwoFactor says where the user stands.
func (c *Commands) TwoFactor(ctx context.Context, userID string) (*plansv1.GetTwoFactorResponse, error) {
	uid, ok := query.ParseID(userID)
	if !ok {
		return nil, query.ErrNoSession()
	}
	db := sqlc.New(c.pool)
	st, err := db.GetTwoFactorState(ctx, uid)
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	left, err := db.CountRecoveryCodes(ctx, uid)
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	out := &plansv1.GetTwoFactorResponse{Enabled: st.ConfirmedAt != nil, RecoveryCodesRemaining: int32(left), RequiredByOrganisation: st.OwnerRequires, //nolint:gosec // at most ten
		SetupRequired: st.OwnerRequires && st.ConfirmedAt == nil}
	if st.ConfirmedAt != nil {
		out.EnableTime = timestamppb.New(*st.ConfirmedAt)
	}
	return out, nil
}

// Setup makes a new secret for the user and returns it for the authenticator app. Two-factor sign-in is not on until Confirm. A pending secret is replaced.
func (c *Commands) Setup(ctx context.Context, userID string) (*plansv1.SetupTwoFactorResponse, error) {
	if err := c.requireBox(); err != nil {
		return nil, err
	}
	uid, ok := query.ParseID(userID)
	if !ok {
		return nil, query.ErrNoSession()
	}
	db := sqlc.New(c.pool)
	user, err := db.GetUser(ctx, uid)
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	secret, err := totp.NewSecret()
	if err != nil {
		return nil, status.Error(codes.Internal, "secret generation failed")
	}
	sealed, err := c.box.Seal(secret, totpAAD(uid))
	if err != nil {
		return nil, status.Error(codes.Internal, "sealing failed")
	}
	n, err := db.UpsertPendingTotp(ctx, sqlc.UpsertPendingTotpParams{UserID: uid, SecretEnc: sealed})
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	if n == 0 {
		return nil, errkey.New(codes.FailedPrecondition, "identity.two_factor.already_enabled", "two-factor sign-in is on already: turn it off first to set it up again")
	}
	return &plansv1.SetupTwoFactorResponse{Secret: totp.Encode(secret), OtpauthUri: totp.URI(Issuer, user.Email, secret)}, nil
}

// newRecoveryCodes replaces the user's recovery codes in the caller's transaction and returns the new ones in the clear (they are never stored that way).
func (c *Commands) newRecoveryCodes(ctx context.Context, q *sqlc.Queries, userID uuid.UUID) ([]string, error) {
	codesList, err := totp.NewRecoveryCodes()
	if err != nil {
		return nil, status.Error(codes.Internal, "code generation failed")
	}
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return nil, err
	}
	for _, code := range codesList {
		norm, _ := totp.NormalizeRecovery(code)
		if err := q.InsertRecoveryCode(ctx, sqlc.InsertRecoveryCodeParams{ID: idgen.NewUUIDv7(), UserID: userID, CodeHash: credential.HashRecovery(c.pepper, userID.String(), norm)}); err != nil {
			return nil, err
		}
	}
	return codesList, nil
}

// Confirm turns two-factor sign-in on with the first code of the app and returns the recovery codes, once. Only the gateway's per-address limit applies to wrong
// codes here: nothing is protected until it is on.
func (c *Commands) Confirm(ctx context.Context, req *plansv1.ConfirmTwoFactorRequest) ([]string, error) {
	uid, ok := query.ParseID(req.GetUserId())
	if !ok {
		return nil, query.ErrNoSession()
	}
	if !credential.WellFormedCode(req.GetCode()) {
		return nil, domain.Invalid("code", "six digits")
	}
	db := sqlc.New(c.pool)
	t, err := db.GetUserTotp(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errkey.New(codes.FailedPrecondition, "identity.two_factor.no_setup", "start the setup first")
	}
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	if t.ConfirmedAt != nil {
		return nil, errkey.New(codes.FailedPrecondition, "identity.two_factor.already_enabled", "two-factor sign-in is on already")
	}
	secret, err := c.openSecret(uid, t.SecretEnc)
	if err != nil {
		return nil, errkey.New(codes.FailedPrecondition, "identity.two_factor.no_setup", "start the setup again")
	}
	step, ok := totp.Match(secret, req.GetCode(), c.now(), 0)
	if !ok {
		return nil, badCode()
	}
	var recovery []string
	var person sqlc.User
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if n, err := q.ConfirmTotp(ctx, sqlc.ConfirmTotpParams{UserID: uid, Step: step}); err != nil {
			return err
		} else if n == 0 {
			return errkey.New(codes.FailedPrecondition, "identity.two_factor.already_enabled", "two-factor sign-in is on already")
		}
		var err error
		if recovery, err = c.newRecoveryCodes(ctx, q, uid); err != nil {
			return err
		}
		u, err := q.GetUser(ctx, uid)
		if err != nil {
			return err
		}
		person = u
		return audit.Record(ctx, tx, audit.Event{By: who(u), Action: "two_factor.enabled", TargetType: "user", TargetID: uid.String(), Summary: "turned on two-factor sign-in"})
	})
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	c.log.InfoContext(ctx, "two-factor sign-in turned on", "user_id", uid)
	c.tell(ctx, person, usermail.KindTwoFactorOn)
	return recovery, nil
}

// spendFor is the proof a signed-in person gives to change their second factor: a code that counts against the lock, spent in tx.
func (c *Commands) spendFor(ctx context.Context, uid uuid.UUID, code string) (user sqlc.User, spend func(context.Context, pgx.Tx) error, recovery bool, err error) {
	db := sqlc.New(c.pool)
	user, err = db.TryTwoFactor(ctx, sqlc.TryTwoFactorParams{ID: uid, MaxFailures: MaxFailures, LockFor: convert.Interval(LockFor)})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.User{}, nil, false, badCode() // locked, or no such user
	}
	if err != nil {
		return sqlc.User{}, nil, false, convert.Err(err, "user")
	}
	spend, recovery, err = c.codeSpender(ctx, db, user, code)
	if errors.Is(err, errBadCode) {
		return sqlc.User{}, nil, false, badCode()
	}
	return user, spend, recovery, err
}

// Disable turns two-factor sign-in off. It needs a code, and is refused while an account the user owns requires it.
func (c *Commands) Disable(ctx context.Context, req *plansv1.DisableTwoFactorRequest) error {
	uid, ok := query.ParseID(req.GetUserId())
	if !ok {
		return query.ErrNoSession()
	}
	st, err := sqlc.New(c.pool).GetTwoFactorState(ctx, uid)
	if err != nil {
		return convert.Err(err, "user")
	}
	if st.ConfirmedAt == nil {
		return errkey.New(codes.FailedPrecondition, "identity.two_factor.not_enabled", "two-factor sign-in is not on")
	}
	if st.OwnerRequires {
		return errkey.New(codes.PermissionDenied, "identity.two_factor.required_by_organisation", "an organisation you own requires two-factor sign-in: turn that off first")
	}
	user, spend, recovery, err := c.spendFor(ctx, uid, req.GetCode())
	if err != nil {
		return err
	}
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := spend(ctx, tx); err != nil {
			return err
		}
		if _, err := q.DeleteTotp(ctx, uid); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, uid); err != nil {
			return err
		}
		if err := q.ResetLoginFailures(ctx, uid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{By: who(user), Action: "two_factor.disabled", TargetType: "user", TargetID: uid.String(), Summary: "turned off two-factor sign-in"})
	})
	if errors.Is(err, errBadCode) {
		return badCode()
	}
	if err != nil {
		return convert.Err(err, "user")
	}
	c.log.WarnContext(ctx, "two-factor sign-in turned off", "user_id", uid)
	if recovery {
		c.tell(ctx, user, usermail.KindRecoveryUsed)
	}
	c.tell(ctx, user, usermail.KindTwoFactorOff)
	return nil
}

// RegenerateRecoveryCodes replaces the recovery codes; it needs a code.
func (c *Commands) RegenerateRecoveryCodes(ctx context.Context, req *plansv1.RegenerateRecoveryCodesRequest) ([]string, error) {
	uid, ok := query.ParseID(req.GetUserId())
	if !ok {
		return nil, query.ErrNoSession()
	}
	user, spend, recovery, err := c.spendFor(ctx, uid, req.GetCode())
	if err != nil {
		return nil, err
	}
	var fresh []string
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := spend(ctx, tx); err != nil {
			return err
		}
		var err error
		if fresh, err = c.newRecoveryCodes(ctx, q, uid); err != nil {
			return err
		}
		if err := q.ResetLoginFailures(ctx, uid); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{By: who(user), Action: "two_factor.recovery_codes_replaced", TargetType: "user", TargetID: uid.String(),
			Summary: "made new recovery codes (the old ones stopped working)"})
	})
	if errors.Is(err, errBadCode) {
		return nil, badCode()
	}
	if err != nil {
		return nil, convert.Err(err, "user")
	}
	if recovery {
		c.tell(ctx, user, usermail.KindRecoveryUsed)
	}
	return fresh, nil
}

// ResetTwoFactor is the operator's hand for a person who lost both the device and the recovery codes. It removes the secret, the recovery codes and the open second-step
// challenges, ends every session of the person, clears the lock from failed sign-ins, writes the audit entry (the operator from the context, the person, the reason) in
// the same transaction and mails the person. It cannot check who is asking: the operator did, by other means, before calling it. It says whether two-factor sign-in was
// on; when it was not, nothing is changed, recorded or mailed. An owner of an account that requires two-factor sign-in is sent to the setup page by the gateway when
// they sign in again.
func (c *Commands) ResetTwoFactor(ctx context.Context, email, reason string) (bool, error) {
	reason = strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(reason); n == 0 || n > 500 {
		return false, domain.Invalid("reason", "required, at most 500 characters")
	}
	email = NormalizeEmail(email)
	if email == "" {
		return false, domain.Invalid("email", "required")
	}
	user, err := sqlc.New(c.pool).GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, errkey.New(codes.NotFound, "identity.user.not_found", "no person signs in with that address")
	}
	if err != nil {
		return false, convert.Err(err, "user")
	}
	var was bool
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		n, err := q.DeleteTotp(ctx, user.ID)
		if err != nil {
			return err
		}
		if was = n > 0; !was {
			return nil
		}
		if err := q.DeleteRecoveryCodes(ctx, user.ID); err != nil {
			return err
		}
		if err := q.DeleteLoginChallengesOfUser(ctx, user.ID); err != nil {
			return err
		}
		if err := q.RevokeUserSessions(ctx, user.ID); err != nil {
			return err
		}
		if err := q.ResetLoginFailures(ctx, user.ID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "two_factor.reset_by_operator", TargetType: "user", TargetID: user.ID.String(),
			Summary: "the operator turned off two-factor sign-in of " + user.Email + " (lost device and recovery codes)", Detail: map[string]any{"reason": reason, "email": user.Email, "sessions_revoked": true}})
	})
	if err != nil {
		return false, convert.Err(err, "user")
	}
	if was {
		c.log.WarnContext(ctx, "two-factor sign-in reset by the operator", "user_id", user.ID)
		c.tell(ctx, user, usermail.KindTwoFactorReset)
	}
	return was, nil
}

// SetAccountSecurity turns the requirement that the owners of an account have two-factor sign-in on or off. An owner only, and an owner who turns it on must have it
// themselves (otherwise they would lock themselves out of everything but the security page at once).
func (c *Commands) SetAccountSecurity(ctx context.Context, req *plansv1.SetAccountSecurityRequest) (bool, error) {
	var out bool
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, aid, m, err := query.Membership(ctx, q, req.GetUserId(), req.GetAccountId())
		if err != nil {
			return err
		}
		if m.Role != query.RoleOwner {
			return forbidden()
		}
		if req.GetRequireTwoFactorForOwners() {
			if st, err := q.GetTwoFactorState(ctx, uid); err != nil {
				return err
			} else if st.ConfirmedAt == nil {
				return errkey.New(codes.FailedPrecondition, "identity.two_factor.not_enabled", "turn on two-factor sign-in for yourself first")
			}
		}
		if out, err = q.SetAccountRequireTwoFactor(ctx, sqlc.SetAccountRequireTwoFactorParams{ID: aid, Required: req.GetRequireTwoFactorForOwners()}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{AccountID: aid, Action: "account.two_factor_policy_changed", TargetType: "account", TargetID: aid.String(),
			Summary: fmt.Sprintf("two-factor sign-in of the owners is now %s", map[bool]string{true: "required", false: "not required"}[out]), Detail: map[string]any{"required": out}})
	})
	if err != nil {
		return false, convert.Err(err, "account")
	}
	return out, nil
}
