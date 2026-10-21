package command

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/credential"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/query"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// The other two ways to sign in besides e-mail and password (phase 41): a one-time code mailed to the address, and an identity provider (Google).
const (
	// CodeTTL is how long a one-time code works.
	CodeTTL = 10 * time.Minute
	// MaxCodeAttempts is how many tries a code survives: the fifth wrong one burns it.
	MaxCodeAttempts = 5
	// CodeResendEvery is the least time between two codes mailed to one address.
	CodeResendEvery = time.Minute
	// MaxCodesPerHour bounds the codes one address is sent in an hour; together with MaxCodeAttempts that is at most 25 guesses an hour at a million values.
	MaxCodesPerHour = 5

	// ProviderGoogle is the identity provider of the "Continue with Google" button.
	ProviderGoogle = "google"
)

func invalidCode() error {
	return errkey.New(codes.InvalidArgument, "identity.otp.invalid", "this code is wrong, has expired or was already used: ask for a new one")
}

// RequestLoginCode mails a new one-time code to the address. The answer is the same whether the address has an account or not (a code for an unknown address
// creates one when it is verified), and the same when the address is being throttled: only a malformed address is refused, and that depends on the input alone.
func (c *Commands) RequestLoginCode(ctx context.Context, req *plansv1.RequestLoginCodeRequest) error {
	email := NormalizeEmail(req.GetEmail())
	if !validEmail(email) {
		return domain.Invalid("email", "must be a plain e-mail address")
	}
	db := sqlc.New(c.pool)
	if at, err := db.LastLoginCodeAt(ctx, email); err == nil {
		if c.now().Sub(at) < CodeResendEvery {
			return nil
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return convert.Err(err, "code")
	}
	n, err := db.CountRecentLoginCodes(ctx, sqlc.CountRecentLoginCodesParams{Email: email, Within: convert.Interval(time.Hour)})
	if err != nil {
		return convert.Err(err, "code")
	}
	if n >= MaxCodesPerHour {
		return nil
	}
	code, salt, err := credential.NewCode()
	if err != nil {
		return status.Error(codes.Internal, "code generation failed")
	}
	locale := usermail.Locale(req.GetLocale())
	if u, err := db.GetUserByEmail(ctx, email); err == nil {
		locale = u.Locale
	}
	if err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.InvalidateLoginCodes(ctx, email); err != nil {
			return err
		}
		if err := q.DeleteStaleLoginCodes(ctx, email); err != nil {
			return err
		}
		return q.InsertLoginCode(ctx, sqlc.InsertLoginCodeParams{ID: idgen.NewUUIDv7(), Email: email, Salt: salt,
			CodeHash: credential.HashCode(c.pepper, salt, email, code), Ttl: convert.Interval(CodeTTL)})
	}); err != nil {
		return convert.Err(err, "code")
	}
	c.mail.Send(ctx, usermail.Mail{Kind: usermail.KindCode, To: email, Locale: locale, Code: code})
	c.log.InfoContext(ctx, "sign-in code sent")
	return nil
}

// VerifyLoginCode checks a code and opens a session. Wrong, expired, used, burned and never-sent codes are one refusal. The code proves the mailbox, so the
// address counts as verified; an address without an account gets one. A password account whose address was never verified loses its password (and its other
// sessions) at this point: whoever registered the address first may not be its owner, and the person who just proved the mailbox is.
func (c *Commands) VerifyLoginCode(ctx context.Context, req *plansv1.VerifyLoginCodeRequest) (Opened, error) {
	email := NormalizeEmail(req.GetEmail())
	code := strings.TrimSpace(req.GetCode())
	if !validEmail(email) || !credential.WellFormedCode(code) {
		return Opened{}, invalidCode()
	}
	db := sqlc.New(c.pool)
	row, err := db.TryLoginCode(ctx, sqlc.TryLoginCodeParams{Email: email, MaxAttempts: MaxCodeAttempts})
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing to try against: do the work of a check anyway, so the time does not tell a never-sent code from a wrong one.
		_ = credential.EqualHash(credential.HashCode(c.pepper, make([]byte, 16), email, code), make([]byte, 32))
		return Opened{}, invalidCode()
	}
	if err != nil {
		return Opened{}, convert.Err(err, "code")
	}
	if !credential.EqualHash(credential.HashCode(c.pepper, row.Salt, email, code), row.CodeHash) {
		return Opened{}, invalidCode()
	}
	var out Opened
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.ConsumeLoginCode(ctx, row.ID); errors.Is(err, pgx.ErrNoRows) {
			return invalidCode()
		} else if err != nil {
			return err
		}
		user, err := q.GetUserByEmail(ctx, email)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			user, err = c.createPasswordless(ctx, q, email, req.GetDisplayName(), req.GetAccountName(), usermail.Locale(req.GetLocale()))
			if err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			if user, err = c.claimAddress(ctx, q, user); err != nil {
				return err
			}
		}
		out, err = c.openSession(ctx, q, user)
		return err
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		// Two first-time codes for one address raced; the code is spent, the person asks again.
		return Opened{}, invalidCode()
	}
	if err != nil {
		return Opened{}, convert.Err(err, "session")
	}
	c.log.InfoContext(ctx, "signed in with a code", "user_id", out.User.GetId())
	return out, nil
}

// claimAddress records that the person holding the mailbox has signed in as user. If the address was not proven before, whoever knew the password may have
// been somebody who registered another person's address: the password is cleared and every session revoked, so only the mailbox owner is in.
func (c *Commands) claimAddress(ctx context.Context, q *sqlc.Queries, user sqlc.User) (sqlc.User, error) {
	if user.EmailVerifiedAt == nil {
		if err := q.ClearPassword(ctx, user.ID); err != nil {
			return user, err
		}
		if err := q.RevokeUserSessions(ctx, user.ID); err != nil {
			return user, err
		}
		c.log.WarnContext(ctx, "unverified address claimed through its mailbox: password cleared", "user_id", user.ID)
	}
	if err := q.MarkEmailVerified(ctx, user.ID); err != nil {
		return user, err
	}
	return q.GetUser(ctx, user.ID)
}

// createPasswordless makes a user (address verified, no password) with an organisation it owns, for a person whose first sign-in is a code or a provider.
func (c *Commands) createPasswordless(ctx context.Context, q *sqlc.Queries, email, display, accountName, locale string) (sqlc.User, error) {
	display = oneLineCapped(display, 200)
	name := oneLineCapped(accountName, 200)
	if name == "" {
		name = email[:strings.IndexByte(email, '@')]
	}
	u, err := q.InsertUser(ctx, sqlc.InsertUserParams{ID: idgen.NewUUIDv7(), Email: email, DisplayName: display, PasswordHash: "", Locale: locale})
	if err != nil {
		return sqlc.User{}, err
	}
	a, err := q.InsertAccount(ctx, sqlc.InsertAccountParams{ID: idgen.NewUUIDv7(), Name: name})
	if err != nil {
		return sqlc.User{}, err
	}
	if err := q.InsertMember(ctx, sqlc.InsertMemberParams{AccountID: a.ID, UserID: u.ID, Role: query.RoleOwner}); err != nil {
		return sqlc.User{}, err
	}
	if err := q.MarkEmailVerified(ctx, u.ID); err != nil {
		return sqlc.User{}, err
	}
	c.log.InfoContext(ctx, "user created by sign-in", "user_id", u.ID, "account_id", a.ID)
	return q.GetUser(ctx, u.ID)
}

func oneLineCapped(s string, n int) string {
	s = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
	if len(s) > n {
		s = strings.ToValidUTF8(s[:n], "")
	}
	return s
}

// SignInWithIdentity opens a session for a person an identity provider has authenticated. The caller (the gateway) has verified the provider's ID token; this
// command still refuses an address the provider did not verify, because a provider's unproven address says nothing about who owns the mailbox.
//
// The user is found by (provider, subject) first: that pair is stable, the address is not. Otherwise the identity is linked to the user with the same address,
// or a user and an organisation are created. Linking to an account whose address was never verified clears its password and revokes its sessions (see
// claimAddress): the provider has just proven the mailbox, and whoever registered the address first may not own it.
func (c *Commands) SignInWithIdentity(ctx context.Context, req *plansv1.SignInWithIdentityRequest) (Opened, error) {
	provider, subject := req.GetProvider(), req.GetSubject()
	email := NormalizeEmail(req.GetEmail())
	if provider != ProviderGoogle || subject == "" || len(subject) > 255 {
		return Opened{}, errkey.New(codes.InvalidArgument, "identity.oauth.invalid", "unknown identity provider or subject")
	}
	if !req.GetEmailVerified() || !validEmail(email) {
		return Opened{}, errkey.New(codes.PermissionDenied, "identity.oauth.email_unverified", "the provider has not verified an e-mail address for this account")
	}
	var out Opened
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		var user sqlc.User
		uid, err := q.GetIdentityUser(ctx, sqlc.GetIdentityUserParams{Provider: provider, Subject: subject})
		switch {
		case err == nil:
			if user, err = q.GetUser(ctx, uid); err != nil {
				return err
			}
			if err := q.TouchIdentity(ctx, sqlc.TouchIdentityParams{Provider: provider, Subject: subject}); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			user, err = q.GetUserByEmail(ctx, email)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if user, err = c.createPasswordless(ctx, q, email, req.GetDisplayName(), "", usermail.Locale(req.GetLocale())); err != nil {
					return err
				}
			case err != nil:
				return err
			default:
				if user, err = c.claimAddress(ctx, q, user); err != nil {
					return err
				}
				if err := q.SetDisplayNameIfEmpty(ctx, sqlc.SetDisplayNameIfEmptyParams{ID: user.ID, DisplayName: oneLineCapped(req.GetDisplayName(), 200)}); err != nil {
					return err
				}
			}
			if err := q.InsertIdentity(ctx, sqlc.InsertIdentityParams{ID: idgen.NewUUIDv7(), UserID: user.ID, Provider: provider, Subject: subject, Email: email}); err != nil {
				return err
			}
		default:
			return err
		}
		out, err = c.openSession(ctx, q, user)
		return err
	})
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
		return Opened{}, errkey.New(codes.Aborted, "identity.oauth.conflict", "this sign-in raced with another one: try again")
	}
	if err != nil {
		return Opened{}, convert.Err(err, "session")
	}
	c.log.InfoContext(ctx, "signed in with an identity provider", "user_id", out.User.GetId(), "provider", provider)
	return out, nil
}
