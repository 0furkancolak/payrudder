package command

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

// E-mail verification and password reset (phase 39). Both are a random single-use token mailed to the address: only its SHA-256 is stored, it is looked up by
// that hash, used with one atomic UPDATE and expires. Neither endpoint says whether an address has an account.
const (
	// VerifyTTL is how long a verification link works.
	VerifyTTL = 24 * time.Hour
	// ResetTTL is how long a password reset link works.
	ResetTTL = time.Hour
	// ResendEvery is the least time between two mails of one kind to one person.
	ResendEvery = time.Minute

	kindVerify = "verify_email"
	kindReset  = "reset_password"
)

func invalidToken() error { return query.InvalidToken() }

// link is the console URL a mail points to. The token travels in the fragment: it is never sent to a server, so it is in no proxy log and no Referer.
func (c *Commands) link(page, token string) string { return c.appURL + "/" + page + "#token=" + token }

// issueToken stores a new token of kind for the user (older unused ones of the kind stop working) and returns the plaintext to mail.
func issueToken(ctx context.Context, q *sqlc.Queries, userID uuid.UUID, kind, prefix string, ttl time.Duration, now time.Time) (string, error) {
	token, hash, err := credential.NewPrefixedToken(prefix)
	if err != nil {
		return "", status.Error(codes.Internal, "token generation failed")
	}
	if err := q.InvalidateUserTokens(ctx, sqlc.InvalidateUserTokensParams{UserID: userID, Kind: kind}); err != nil {
		return "", err
	}
	if err := q.DeleteStaleUserTokens(ctx, userID); err != nil {
		return "", err
	}
	if err := q.InsertUserToken(ctx, sqlc.InsertUserTokenParams{ID: idgen.NewUUIDv7(), UserID: userID, Kind: kind, TokenHash: hash, ExpiresAt: now.Add(ttl)}); err != nil {
		return "", err
	}
	return token, nil
}

// recentlySent reports whether a token of kind was issued to the user less than ResendEvery ago.
func (c *Commands) recentlySent(ctx context.Context, db *sqlc.Queries, userID uuid.UUID, kind string) (bool, error) {
	at, err := db.LastUserTokenAt(ctx, sqlc.LastUserTokenAtParams{UserID: userID, Kind: kind})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, convert.Err(err, "token")
	}
	return c.now().Sub(at) < ResendEvery, nil
}

// VerifyEmail uses a verification token and marks the address as proven. A second use, an expired or an unknown token is the same refusal.
func (c *Commands) VerifyEmail(ctx context.Context, token string) (*plansv1.User, error) {
	if !credential.WellFormed(credential.VerifyPrefix, token) {
		return nil, invalidToken()
	}
	var user sqlc.User
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, err := q.ConsumeUserToken(ctx, sqlc.ConsumeUserTokenParams{TokenHash: credential.HashToken(token), Kind: kindVerify})
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidToken()
		}
		if err != nil {
			return err
		}
		if err := q.MarkEmailVerified(ctx, uid); err != nil {
			return err
		}
		user, err = q.GetUser(ctx, uid)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "token")
	}
	c.log.InfoContext(ctx, "e-mail address verified", "user_id", user.ID)
	return query.User(user), nil
}

// ResendVerification mails a new link to the signed-in user, at most once a minute. For an address that is verified already it does nothing.
func (c *Commands) ResendVerification(ctx context.Context, userID string) error {
	uid, ok := query.ParseID(userID)
	if !ok {
		return query.ErrNoSession()
	}
	db := sqlc.New(c.pool)
	u, err := db.GetUser(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return query.ErrNoSession()
	}
	if err != nil {
		return convert.Err(err, "user")
	}
	if u.EmailVerifiedAt != nil {
		return nil
	}
	recent, err := c.recentlySent(ctx, db, uid, kindVerify)
	if err != nil {
		return err
	}
	if recent {
		return errkey.New(codes.ResourceExhausted, "identity.verify.throttled", "a link was sent a moment ago: wait a minute before asking again")
	}
	var token string
	if err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		token, err = issueToken(ctx, sqlc.New(tx), uid, kindVerify, credential.VerifyPrefix, VerifyTTL, c.now())
		return err
	}); err != nil {
		return convert.Err(err, "token")
	}
	c.mail.Send(ctx, usermail.Mail{Kind: usermail.KindVerify, To: u.Email, Locale: u.Locale, Link: c.link("verify", token)})
	return nil
}

// RequestPasswordReset mails a reset link when the address has an account. The answer is the same whether it has or not (and when a link went out less
// than a minute ago, which keeps the endpoint from flooding an inbox); only a malformed address is refused, and that depends on the input alone.
func (c *Commands) RequestPasswordReset(ctx context.Context, req *plansv1.RequestPasswordResetRequest) error {
	email := NormalizeEmail(req.GetEmail())
	if !validEmail(email) {
		return domain.Invalid("email", "must be a plain e-mail address")
	}
	db := sqlc.New(c.pool)
	u, err := db.GetUserByEmail(ctx, email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return convert.Err(err, "user")
	}
	recent, err := c.recentlySent(ctx, db, u.ID, kindReset)
	if err != nil {
		return err
	}
	if recent {
		return nil
	}
	var token string
	if err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		token, err = issueToken(ctx, sqlc.New(tx), u.ID, kindReset, credential.ResetPrefix, ResetTTL, c.now())
		return err
	}); err != nil {
		return convert.Err(err, "token")
	}
	locale := u.Locale
	if l := req.GetLocale(); l != "" {
		locale = usermail.Locale(l)
	}
	c.mail.Send(ctx, usermail.Mail{Kind: usermail.KindReset, To: u.Email, Locale: locale, Link: c.link("reset", token)})
	c.log.InfoContext(ctx, "password reset requested", "user_id", u.ID)
	return nil
}

// ResetPassword uses a reset token and sets the new password. Opening the mailed link proves the mailbox, so the address counts as verified; the lock from
// failed sign-ins is cleared; and every session of the user is revoked, so whoever held one (the person after a theft, or a thief) has to sign in again.
func (c *Commands) ResetPassword(ctx context.Context, req *plansv1.ResetPasswordRequest) error {
	token := req.GetToken()
	if n := len(req.GetNewPassword()); n < credential.MinPasswordLen || n > credential.MaxPasswordLen {
		return domain.Invalid("password", "must be between 10 and 256 characters")
	}
	if !credential.WellFormed(credential.ResetPrefix, token) {
		return invalidToken()
	}
	// A bad token is refused before the work of hashing; the atomic use below is what makes it single use.
	if _, err := sqlc.New(c.pool).GetLiveUserToken(ctx, sqlc.GetLiveUserTokenParams{TokenHash: credential.HashToken(token), Kind: kindReset}); errors.Is(err, pgx.ErrNoRows) {
		return invalidToken()
	} else if err != nil {
		return convert.Err(err, "token")
	}
	var hash string
	var herr error
	if err := c.withSlot(ctx, func() { hash, herr = credential.Hash(req.GetNewPassword()) }); err != nil {
		return err
	}
	if herr != nil {
		return status.Error(codes.Internal, "password hashing failed")
	}
	var userID uuid.UUID
	err := pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		uid, err := q.ConsumeUserToken(ctx, sqlc.ConsumeUserTokenParams{TokenHash: credential.HashToken(token), Kind: kindReset})
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidToken()
		}
		if err != nil {
			return err
		}
		userID = uid
		if err := q.SetPassword(ctx, sqlc.SetPasswordParams{ID: uid, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.MarkEmailVerified(ctx, uid); err != nil {
			return err
		}
		if err := q.RevokeUserSessions(ctx, uid); err != nil {
			return err
		}
		return q.InvalidateUserTokens(ctx, sqlc.InvalidateUserTokensParams{UserID: uid, Kind: kindReset})
	})
	if err != nil {
		return convert.Err(err, "token")
	}
	c.log.InfoContext(ctx, "password reset", "user_id", userID)
	return nil
}
