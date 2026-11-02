//go:build integration

package itest

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
)

// testTwoFactorMailsAndReset (plan 22 G9 leftovers): the person is mailed when two-factor sign-in is turned on or off and when a recovery code signs somebody in; the
// operator can turn it off for a person who lost both device and codes, which ends their sessions, clears the lock, mails them and leaves an audit entry with the reason.
func testTwoFactorMailsAndReset(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ruth := e.verified(t, "ruth@2fa-mail.example", "Two Factor Mail Co")
	sent := func(kind usermail.Kind) int { return len(e.mail.to(kind, ruth.email)) }

	secret, recovery := e.enableTwoFactor(t, ruth)
	if sent(usermail.KindTwoFactorOn) != 1 || sent(usermail.KindTwoFactorOff) != 0 || sent(usermail.KindRecoveryUsed) != 0 {
		t.Fatalf("after turning it on: on %d, off %d, recovery %d mails", sent(usermail.KindTwoFactorOn), sent(usermail.KindTwoFactorOff), sent(usermail.KindRecoveryUsed))
	}
	for _, m := range e.mail.to(usermail.KindTwoFactorOn, ruth.email) {
		if m.Link != "" || m.Code != "" {
			t.Errorf("the mail carries a link or a code: %+v", m)
		}
	}

	// A code from the app signs in without a mail; a recovery code signs in and says so.
	e.fresh(t, ruth.id)
	if _, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: e.firstStep(t, ruth.email).GetChallenge(), Code: codeNow(t, secret)}); err != nil {
		t.Fatal(err)
	}
	if sent(usermail.KindRecoveryUsed) != 0 {
		t.Fatal("a code from the app was mailed as a recovery code")
	}
	if _, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: e.firstStep(t, ruth.email).GetChallenge(), Code: recovery[0]}); err != nil {
		t.Fatal(err)
	}
	if sent(usermail.KindRecoveryUsed) != 1 {
		t.Fatalf("recovery code sign-in: %d mails, want 1", sent(usermail.KindRecoveryUsed))
	}
	// A wrong code and a spent recovery code mail nothing.
	if _, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: e.firstStep(t, ruth.email).GetChallenge(), Code: recovery[0]}); err == nil {
		t.Fatal("a recovery code worked twice")
	}
	if sent(usermail.KindRecoveryUsed) != 1 {
		t.Fatalf("a refused code was mailed: %d", sent(usermail.KindRecoveryUsed))
	}

	// Turning it off with a recovery code is two news: the recovery code and the switch.
	if _, err := e.ident.DisableTwoFactor(ctx, &plansv1.DisableTwoFactorRequest{UserId: ruth.id, Code: recovery[1]}); err != nil {
		t.Fatal(err)
	}
	if sent(usermail.KindTwoFactorOff) != 1 || sent(usermail.KindRecoveryUsed) != 2 {
		t.Fatalf("after turning it off: off %d, recovery %d mails, want 1 and 2", sent(usermail.KindTwoFactorOff), sent(usermail.KindRecoveryUsed))
	}
	// In the person's language.
	if m := e.mail.to(usermail.KindTwoFactorOff, ruth.email)[0]; m.To != ruth.email {
		t.Errorf("mail = %+v", m)
	}

	t.Run("the operator resets a lost second factor", func(t *testing.T) {
		secret, recovery = e.enableTwoFactor(t, ruth)
		e.fresh(t, ruth.id)
		opened, err := e.ident.VerifyTwoFactor(ctx, &plansv1.VerifyTwoFactorRequest{Challenge: e.firstStep(t, ruth.email).GetChallenge(), Code: codeNow(t, secret)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.system.Exec(ctx, `UPDATE users SET failed_logins = 5, locked_until = now() + interval '10 minutes' WHERE id = $1::uuid`, ruth.id); err != nil {
			t.Fatal(err)
		}
		req := &plansv1.ResetUserTwoFactorRequest{Email: strings.ToUpper(ruth.email), Reason: "lost the phone and the codes; checked by video call"}

		// Only the operator may, and a reason is required.
		_, err = e.ident.ResetUserTwoFactor(ctx, req)
		wantCode(t, "a caller that is not the operator", err, codes.PermissionDenied, "identity.operator.forbidden")
		op := actedAs(operator(), audit.Actor{Type: audit.ActorOperator, ID: "ops@payrudder.example", Email: "ops@payrudder.example", IP: "198.51.100.7", RequestID: "req-reset-1"})
		_, err = e.ident.ResetUserTwoFactor(op, &plansv1.ResetUserTwoFactorRequest{Email: ruth.email, Reason: "  "})
		wantCode(t, "no reason", err, codes.InvalidArgument, "")
		_, err = e.ident.ResetUserTwoFactor(op, &plansv1.ResetUserTwoFactorRequest{Email: "nobody-at-all@2fa-mail.example", Reason: "x"})
		wantCode(t, "an unknown address", err, codes.NotFound, "identity.user.not_found")
		if st, _ := e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: ruth.id}); !st.GetEnabled() {
			t.Fatal("a refused reset turned it off")
		}

		before := sent(usermail.KindTwoFactorReset)
		resp, err := e.ident.ResetUserTwoFactor(op, req)
		if err != nil || !resp.GetWasEnabled() {
			t.Fatalf("reset = %v, %v", resp, err)
		}
		if st, err := e.ident.GetTwoFactor(ctx, &plansv1.GetTwoFactorRequest{UserId: ruth.id}); err != nil || st.GetEnabled() || st.GetRecoveryCodesRemaining() != 0 {
			t.Errorf("after the reset: %v, %v", st, err)
		}
		if _, err := e.ident.GetSession(ctx, &plansv1.GetSessionRequest{Token: opened.GetToken()}); err == nil {
			t.Error("a session of the person survived the reset")
		}
		// The lock is gone and the password alone signs in; the recovery codes are dead.
		if r := e.firstStep(t, ruth.email); r.GetToken() == "" || r.GetChallenge() != "" {
			t.Errorf("sign-in after the reset = %v", r)
		}
		if sent(usermail.KindTwoFactorReset) != before+1 {
			t.Errorf("the person was mailed %d times", sent(usermail.KindTwoFactorReset)-before)
		}
		var challenges int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM login_challenges WHERE user_id = $1::uuid`, ruth.id).Scan(&challenges); err != nil || challenges != 0 {
			t.Errorf("open challenges after the reset: %d, %v", challenges, err)
		}

		// The entry: who (the operator), whom, why; nothing secret.
		got := e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actions: []string{"two_factor.reset_by_operator"}, TargetType: "user", TargetId: ruth.id})
		if len(got) != 1 || got[0].GetActorType() != "operator" || got[0].GetActorEmail() != "ops@payrudder.example" || got[0].GetSourceIp() != "198.51.100.7" || got[0].GetRequestId() != "req-reset-1" ||
			!strings.Contains(got[0].GetDetailJson(), "lost the phone and the codes") {
			t.Fatalf("two_factor.reset_by_operator = %v", got)
		}
		for _, secretText := range append(recovery, secret) {
			if strings.Contains(got[0].GetDetailJson(), secretText) || strings.Contains(got[0].GetSummary(), secretText) {
				t.Errorf("the entry holds a secret: %v", got[0])
			}
		}

		// Again: nothing was on, so nothing changes, nothing is recorded, nobody is mailed.
		again, err := e.ident.ResetUserTwoFactor(op, req)
		if err != nil || again.GetWasEnabled() {
			t.Fatalf("second reset = %v, %v", again, err)
		}
		if sent(usermail.KindTwoFactorReset) != before+1 {
			t.Error("a reset that changed nothing was mailed")
		}
		if n := len(e.events(t, operator(), &auditv1.ListAuditEventsRequest{Actions: []string{"two_factor.reset_by_operator"}, TargetType: "user", TargetId: ruth.id})); n != 1 {
			t.Errorf("%d entries after a reset that changed nothing", n)
		}
	})
}
