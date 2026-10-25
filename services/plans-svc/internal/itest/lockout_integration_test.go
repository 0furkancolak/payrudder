//go:build integration

package itest

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/command"
)

// testPasswordLockout: the attempt is counted in the statement that checks the limit, so a burst of parallel guesses cannot get more than
// MaxFailures tries before the lock holds.
func testPasswordLockout(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const addr = "lockout@example.com"
	e.signUp(t, addr, "Lockout Co")

	const burst = 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	opened := 0
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pw := "a wrong password " + string(rune('a'+i%26))
			if i == burst-1 {
				pw = goodPassword // the right one arrives last in the burst, when the lock should already hold for most of the others
			}
			if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: pw}); err == nil {
				mu.Lock()
				opened++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	var failed int
	if err := e.system.QueryRow(ctx, `SELECT failed_logins FROM users WHERE email = $1`, addr).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed > command.MaxFailures {
		t.Errorf("%d parallel guesses were verified, at most %d may be", failed, command.MaxFailures)
	}
	if opened > 1 {
		t.Errorf("%d sessions opened", opened)
	}

	if opened == 0 {
		_, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword})
		wantCode(t, "locked account, right password", err, codes.Unauthenticated, "")
	}
	if _, err := e.system.Exec(ctx, `UPDATE users SET locked_until = now() - interval '1 minute' WHERE email = $1`, addr); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ident.SignIn(ctx, &plansv1.SignInRequest{Email: addr, Password: goodPassword}); err != nil {
		t.Errorf("sign-in after the lock ran out: %v", err)
	}
}
