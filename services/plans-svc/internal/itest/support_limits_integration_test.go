//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/support"
)

// testSupportLimits covers what bounds a person who opens tickets in a loop (security review 4): solving a ticket frees its place among the ones waiting, so the
// number opened in an hour is capped too, solved ones included; and a burst of new tickets mails the operators a digest, not one mail each.
func testSupportLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	const perHour, burst = 3, 2
	desk := support.New(e.system, slog.New(slog.NewTextHandler(io.Discard, nil)), support.Config{Mail: e.mail, Operators: []string{testOperatorEmail}, AppURL: testAppURL,
		OpensPerHour: perHour, MailBurst: burst})
	open := func(who person, subject string) (*plansv1.SupportTicket, error) {
		r, err := desk.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: who.id, AccountId: who.account, Subject: subject, Category: "other", Body: "x"})
		return r.GetTicket(), err
	}

	t.Run("an account opens at most so many tickets an hour, solved ones included, and the others are not affected", func(t *testing.T) {
		looper := e.verified(t, "limits-loop@example.com", "Loop Co")
		for i := 0; i < perHour; i++ {
			tk, err := open(looper, "Open and solve "+strconv.Itoa(i))
			if err != nil {
				t.Fatalf("ticket %d: %v", i, err)
			}
			if _, err := desk.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: looper.id, TicketId: tk.GetId(), Status: "solved"}); err != nil {
				t.Fatal(err)
			}
		}
		_, err := open(looper, "One more")
		wantCode(t, "the fourth in an hour, with none waiting", err, codes.ResourceExhausted, "support.tickets.too_many_recent")
		neighbour := e.verified(t, "limits-neighbour@example.com", "Neighbour Co")
		if _, err := open(neighbour, "Another account is fine"); err != nil {
			t.Errorf("another account: %v", err)
		}
		// An hour later the tickets no longer count.
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET created_at = now() - interval '61 minutes' WHERE account_id = $1::uuid`, looper.account); err != nil {
			t.Fatal(err)
		}
		if _, err := open(looper, "An hour later"); err != nil {
			t.Errorf("after the hour: %v", err)
		}
	})

	t.Run("the limit holds when the tickets are opened at once", func(t *testing.T) {
		race := e.verified(t, "limits-race@example.com", "Race Co")
		var wg sync.WaitGroup
		var opened, refused atomic.Int32
		for i := 0; i < perHour+5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := open(race, "At the same moment")
				switch {
				case err == nil:
					opened.Add(1)
				case errkey.Of(err) == "support.tickets.too_many_recent":
					refused.Add(1)
				default:
					t.Errorf("open: %v", err)
				}
			}()
		}
		wg.Wait()
		if opened.Load() != perHour || refused.Load() != 5 {
			t.Errorf("opened %d and refused %d at once, want %d and 5", opened.Load(), refused.Load(), perHour)
		}
	})

	t.Run("a burst of new tickets mails the operators the first ones, then one digest, then nothing", func(t *testing.T) {
		// The database is shared with the other tests: the burst counts what was opened in the last ten minutes by anybody (the other tests move last_message_at of some
		// tickets back, which the count leaves out like the code does), so start from what is there.
		var already int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM support_tickets WHERE created_at > now() - interval '10 minutes' AND last_message_at > now() - interval '10 minutes'`).Scan(&already); err != nil {
			t.Fatal(err)
		}
		bursty := support.New(e.system, slog.New(slog.NewTextHandler(io.Discard, nil)), support.Config{Mail: e.mail, Operators: []string{testOperatorEmail}, AppURL: testAppURL, MailBurst: already + burst})
		who := e.verified(t, "limits-burst@example.com", "Burst Co")
		beforeNew, beforeDigest := len(e.mail.to(usermail.KindTicketNew, testOperatorEmail)), len(e.mail.to(usermail.KindTicketBurst, testOperatorEmail))
		for i := 0; i < burst+4; i++ {
			if _, err := bursty.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: who.id, AccountId: who.account, Subject: "Burst " + strconv.Itoa(i), Category: "other", Body: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		if n := len(e.mail.to(usermail.KindTicketNew, testOperatorEmail)) - beforeNew; n != burst {
			t.Errorf("%d single mails, want %d", n, burst)
		}
		digest := e.mail.to(usermail.KindTicketBurst, testOperatorEmail)
		if len(digest) != beforeDigest+1 {
			t.Fatalf("%d digests, want 1", len(digest)-beforeDigest)
		}
		if got := digest[len(digest)-1]; got.Link != testAppURL+"/support" {
			t.Errorf("the digest links to %q, want the inbox", got.Link)
		}
	})

	t.Run("the burst is off by default: every new ticket is mailed", func(t *testing.T) {
		who := e.verified(t, "limits-off@example.com", "Off Co")
		off := support.New(e.system, slog.New(slog.NewTextHandler(io.Discard, nil)), support.Config{Mail: e.mail, Operators: []string{testOperatorEmail}, AppURL: testAppURL})
		before := len(e.mail.to(usermail.KindTicketNew, testOperatorEmail))
		for i := 0; i < 8; i++ {
			if _, err := off.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: who.id, AccountId: who.account, Subject: "No limit " + strconv.Itoa(i), Category: "other", Body: "x"}); err != nil {
				t.Fatal(err)
			}
		}
		if n := len(e.mail.to(usermail.KindTicketNew, testOperatorEmail)) - before; n != 8 {
			t.Errorf("%d mails for 8 tickets", n)
		}
	})
}
