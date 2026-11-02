//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/support"
)

// testSupport covers the help desk: a person opens a ticket for their account, only members of the account see it, the operator answers (publicly or in a note
// the person never sees), and the status, the first-response clock and the priority follow the thread.
func testSupport(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "support-ada@example.com", "Support Co")
	other := e.verified(t, "support-other@example.com", "Other Co")
	viewer := e.signUp(t, "support-viewer@example.com", "Viewer Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'viewer')`, ada.account, viewer.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	created, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: "Shop", Currency: "EUR", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: other.id, AccountId: other.account, Name: "Elsewhere", Currency: "EUR", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	open, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, AppId: created.GetApp().GetId(),
		Subject: "Paddle webhooks are late", Category: "integration", Priority: "high", Body: "Since this morning."})
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	tk := open.GetTicket()

	t.Run("a new ticket is open, numbered and has a first-response deadline in business hours of its app's zone", func(t *testing.T) {
		if tk.GetStatus() != "open" || tk.GetNumber() == 0 || tk.GetAppName() != "Shop" || tk.GetMessageCount() != 1 {
			t.Fatalf("ticket: %+v", tk)
		}
		// The clock counts Monday to Friday 09:00-18:00 in the app's zone (Europe/Berlin), so the deadline depends on when the test runs: it is between the deadline
		// of the moment before the call and the one of the moment after it, and never inside a weekend or outside the working day.
		due, lo, hi := tk.GetFirstResponseDue().AsTime(), domain.SupportFirstResponseDue(before, "high", "Europe/Berlin"), domain.SupportFirstResponseDue(after, "high", "Europe/Berlin")
		if due.Before(lo) || due.After(hi) {
			t.Errorf("high priority is due %v, want between %v and %v", due, lo, hi)
		}
		berlin, _ := time.LoadLocation("Europe/Berlin")
		if local := due.In(berlin); local.Weekday() == time.Saturday || local.Weekday() == time.Sunday || local.Hour() < 9 || local.Hour() > 18 {
			t.Errorf("the deadline %v is outside business hours", local)
		}
		if tk.GetSlaBreached() {
			t.Error("a new ticket is already breached")
		}
	})

	t.Run("only members of the account see and use it", func(t *testing.T) {
		_, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: other.id, TicketId: tk.GetId()})
		wantCode(t, "stranger reads", err, codes.NotFound, "support.ticket.not_found")
		_, err = e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: other.id, TicketId: tk.GetId(), Body: "hi"})
		wantCode(t, "stranger replies", err, codes.NotFound, "support.ticket.not_found")
		_, err = e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: other.id, AccountId: ada.account, Subject: "Not mine", Category: "other", Body: "x"})
		wantCode(t, "stranger opens for another account", err, codes.PermissionDenied, "support.account.not_member")
		_, err = e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, AppId: otherApp.GetApp().GetId(), Subject: "Wrong app", Category: "other", Body: "x"})
		wantCode(t, "another account's app", err, codes.NotFound, "support.app.not_found")
		mine, err := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: other.id})
		if err != nil || len(mine.GetTickets()) != 0 {
			t.Errorf("stranger's list: %v %v", err, mine.GetTickets())
		}
		seen, err := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: viewer.GetUser().GetId(), Status: "active"})
		if err != nil || len(seen.GetTickets()) != 1 {
			t.Errorf("a member's list: %v %v", err, seen.GetTickets())
		}
	})

	t.Run("the operator calls need the operator context", func(t *testing.T) {
		_, err := e.support.ListSupportTickets(ctx, &plansv1.ListSupportTicketsRequest{})
		if err == nil {
			t.Fatal("a call without the operator context was served")
		}
		list, err := e.support.ListSupportTickets(operator(), &plansv1.ListSupportTicketsRequest{Search: tk.GetSubject()[:6]})
		if err != nil || len(list.GetTickets()) != 1 {
			t.Fatalf("search by subject: %v %v", err, list.GetTickets())
		}
	})

	t.Run("an internal note is invisible to the person and changes nothing else", func(t *testing.T) {
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: tk.GetId(), Body: "Looks like the Paddle outage.", Internal: true, AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		mine, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()})
		if err != nil || len(mine.GetMessages()) != 1 || mine.GetTicket().GetStatus() != "open" || mine.GetTicket().GetFirstResponseTime() != nil {
			t.Fatalf("the person's view: %v %+v", err, mine)
		}
		all, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: tk.GetId()})
		if err != nil || len(all.GetMessages()) != 2 || !all.GetMessages()[1].GetInternal() {
			t.Fatalf("the operator's view: %v %+v", err, all.GetMessages())
		}
	})

	t.Run("a public answer stops the clock and waits for the person, whose reply reopens it", func(t *testing.T) {
		r, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: tk.GetId(), Body: "We are on it.", AuthorName: "Sam"})
		if err != nil {
			t.Fatal(err)
		}
		if r.GetTicket().GetStatus() != "pending" || r.GetTicket().GetFirstResponseTime() == nil || r.GetTicket().GetLastAuthor() != "operator" {
			t.Fatalf("after the answer: %+v", r.GetTicket())
		}
		back, err := e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId(), Body: "Thanks, still failing."})
		if err != nil || back.GetTicket().GetStatus() != "open" || back.GetTicket().GetLastAuthor() != "customer" {
			t.Fatalf("after the person's reply: %v %+v", err, back.GetTicket())
		}
	})

	t.Run("the person marks it solved; the operator closes it; a closed ticket takes no reply", func(t *testing.T) {
		s, err := e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: ada.id, TicketId: tk.GetId(), Status: "solved"})
		if err != nil || s.GetTicket().GetStatus() != "solved" || s.GetTicket().GetSolveTime() == nil {
			t.Fatalf("solve: %v %+v", err, s.GetTicket())
		}
		_, err = e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: ada.id, TicketId: tk.GetId(), Status: "pending"})
		wantCode(t, "person sets pending", err, codes.InvalidArgument, "plans.request.invalid")
		closed := "closed"
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: tk.GetId(), Status: &closed}); err != nil {
			t.Fatal(err)
		}
		_, err = e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId(), Body: "One more thing"})
		wantCode(t, "reply on closed", err, codes.FailedPrecondition, "support.ticket.closed")
	})

	t.Run("the operator sets the priority and the assignee, and finds late tickets", func(t *testing.T) {
		fresh, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, Subject: "Question about tax", Category: "question", Body: "Hello"})
		if err != nil {
			t.Fatal(err)
		}
		id := fresh.GetTicket().GetId()
		urgent, who := "urgent", testOperatorEmail
		u, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Priority: &urgent, Assignee: &who})
		if err != nil || u.GetTicket().GetPriority() != "urgent" || u.GetTicket().GetAssignee() != testOperatorEmail {
			t.Fatalf("update: %v %+v", err, u.GetTicket())
		}
		// The deadline follows the priority, counted from when the ticket was opened (the account has an app in Berlin, hence its zone).
		if want := domain.SupportFirstResponseDue(u.GetTicket().GetCreateTime().AsTime(), "urgent", "Europe/Berlin"); !u.GetTicket().GetFirstResponseDue().AsTime().Equal(want) {
			t.Errorf("urgent is due %v, want %v", u.GetTicket().GetFirstResponseDue().AsTime(), want)
		}
		// Push the deadline into the past: the ticket is now late and the filter finds it.
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET first_response_due = now() - interval '1 minute' WHERE id = $1::uuid`, id); err != nil {
			t.Fatal(err)
		}
		late, err := e.support.ListSupportTickets(operator(), &plansv1.ListSupportTicketsRequest{SlaBreached: true, Assignee: testOperatorEmail})
		if err != nil || len(late.GetTickets()) != 1 || late.GetTickets()[0].GetId() != id || !late.GetTickets()[0].GetSlaBreached() {
			t.Fatalf("late tickets: %v %v", err, late.GetTickets())
		}
		bad := "asap"
		_, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Priority: &bad})
		wantCode(t, "unknown priority", err, codes.InvalidArgument, "plans.request.invalid")
	})
}

// testSupportDesk covers the help desk's second step (plan 12): the mails to the operators and to the person (in their language, only for what they have not done
// themselves), the history of changes, the "unread" mark of the person who opened a ticket, the limit of tickets waiting per account (also when they are opened at
// the same moment) and the job that closes a ticket nobody answered.
func testSupportDesk(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "desk-ada@example.com", "Desk Co")
	teammate := e.signUp(t, "desk-mate@example.com", "Mate Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`, ada.account, teammate.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	// Ada reads Turkish; the operator's address belongs to a user who reads German.
	ops := e.signUp(t, testOperatorEmail, "Ops")
	for id, locale := range map[string]string{ada.id: "tr", ops.GetUser().GetId(): "de"} {
		if _, err := e.system.Exec(ctx, `UPDATE users SET locale = $2 WHERE id = $1::uuid`, id, locale); err != nil {
			t.Fatal(err)
		}
	}
	open := func(user, account, subject string) *plansv1.SupportTicket {
		t.Helper()
		r, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: user, AccountId: account, Subject: subject, Category: "question", Body: "Hello"})
		if err != nil {
			t.Fatalf("open %q: %v", subject, err)
		}
		return r.GetTicket()
	}
	mine := func(user string) map[string]*plansv1.SupportTicket {
		t.Helper()
		r, err := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: user})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]*plansv1.SupportTicket{}
		for _, tk := range r.GetTickets() {
			out[tk.GetId()] = tk
		}
		return out
	}
	tk := open(ada.id, ada.account, "Invoices show the wrong tax")
	id := tk.GetId()
	ticketNo := "#" + strconv.FormatInt(tk.GetNumber(), 10)

	t.Run("a new ticket is mailed to the operator in their language, and the opener is not mailed", func(t *testing.T) {
		got := e.mail.to(usermail.KindTicketNew, testOperatorEmail)
		if len(got) != 1 {
			t.Fatalf("operator mails: %+v", got)
		}
		m := got[0]
		if m.Locale != "de" || m.Ticket != ticketNo || m.Subject != "Invoices show the wrong tax" || m.Priority != "normal" || m.Account != "Desk Co" || m.Link != testAppURL+"/support?sheet=ticket:"+id {
			t.Errorf("operator mail: %+v", m)
		}
		if n := len(e.mail.to(usermail.KindTicketReply, ada.email)) + len(e.mail.to(usermail.KindTicketStatus, ada.email)); n != 0 {
			t.Errorf("the opener was mailed %d times about their own ticket", n)
		}
	})

	t.Run("an answer is mailed to the opener in their language, an internal note is not, and it is unread for the opener only", func(t *testing.T) {
		if mine(ada.id)[id].GetUnread() {
			t.Fatal("a ticket the opener just wrote is unread")
		}
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: id, Body: "Checking with tax.", Internal: true, AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		if n := len(e.mail.to(usermail.KindTicketReply, ada.email)); n != 0 || mine(ada.id)[id].GetUnread() {
			t.Fatalf("an internal note mailed %d times or made the ticket unread", n)
		}
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: id, Body: "Fixed, reload the invoice.", AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		got := e.mail.to(usermail.KindTicketReply, ada.email)
		if len(got) != 1 || got[0].Locale != "tr" || got[0].Ticket != ticketNo || got[0].Link != testAppURL+"/app/support?sheet=ticket:"+id {
			t.Fatalf("reply mails: %+v", got)
		}
		if !mine(ada.id)[id].GetUnread() || mine(teammate.GetUser().GetId())[id].GetUnread() {
			t.Error("the answer is unread for the opener and not for the teammate")
		}
		// The teammate reading it leaves the mark; the operator never has one.
		if r, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: teammate.GetUser().GetId(), TicketId: id}); err != nil || r.GetTicket().GetUnread() {
			t.Fatalf("teammate reads: %v %+v", err, r.GetTicket())
		}
		if !mine(ada.id)[id].GetUnread() {
			t.Error("the teammate's reading cleared the opener's mark")
		}
		if r, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: id}); err != nil || r.GetTicket().GetUnread() {
			t.Fatalf("operator reads: %v %+v", err, r.GetTicket())
		}
		if r, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: id}); err != nil || r.GetTicket().GetUnread() {
			t.Fatalf("opener reads: %v %+v", err, r.GetTicket())
		}
		if mine(ada.id)[id].GetUnread() {
			t.Error("the opener's reading did not clear the mark")
		}
	})

	t.Run("a status the operator sets is mailed and unread; what the person does themselves is not", func(t *testing.T) {
		if _, err := e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: ada.id, TicketId: id, Body: "Thanks, works."}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: ada.id, TicketId: id, Status: "solved"}); err != nil {
			t.Fatal(err)
		}
		if n := len(e.mail.to(usermail.KindTicketStatus, ada.email)); n != 0 {
			t.Fatalf("the person's own change was mailed %d times", n)
		}
		closed, high, sam := "closed", "high", testOperatorEmail
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Priority: &high, Assignee: &sam, AuthorName: "sam@example.com"}); err != nil {
			t.Fatal(err)
		}
		if n := len(e.mail.to(usermail.KindTicketStatus, ada.email)); n != 0 || mine(ada.id)[id].GetUnread() {
			t.Fatalf("a priority and assignee change mailed %d times or made the ticket unread", n)
		}
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Status: &closed, AuthorName: "sam@example.com"}); err != nil {
			t.Fatal(err)
		}
		got := e.mail.to(usermail.KindTicketStatus, ada.email)
		if len(got) != 1 || got[0].Status != "closed" || got[0].Locale != "tr" || got[0].Subject != "Invoices show the wrong tax" {
			t.Fatalf("status mails: %+v", got)
		}
		if !mine(ada.id)[id].GetUnread() {
			t.Error("a status change by the operator is unread for the opener")
		}
	})

	t.Run("every change is in the history, in order, with who made it; both sides read it", func(t *testing.T) {
		type line struct{ kind, actorKind, actor, from, to string }
		want := []line{
			{"status", "operator", "Sam", "open", "pending"},
			{"status", "customer", "Test", "pending", "open"},
			{"status", "customer", "Test", "open", "solved"},
			{"priority", "operator", "sam@example.com", "normal", "high"},
			{"assignee", "operator", "sam@example.com", "", testOperatorEmail},
			{"status", "operator", "sam@example.com", "solved", "closed"},
		}
		check := func(who string, events []*plansv1.SupportEvent) {
			t.Helper()
			var got []line
			for _, ev := range events {
				got = append(got, line{ev.GetKind(), ev.GetActorKind(), ev.GetActorName(), ev.GetFromValue(), ev.GetToValue()})
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s sees\n%v\nwant\n%v", who, got, want)
			}
		}
		person, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: id})
		if err != nil {
			t.Fatal(err)
		}
		check("the person", person.GetEvents())
		all, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: id})
		if err != nil {
			t.Fatal(err)
		}
		check("the operator", all.GetEvents())
		// Nobody can edit it: the help desk's role has no UPDATE or DELETE on the table.
		if _, err := e.system.Exec(ctx, `UPDATE support_events SET to_value = 'x'`); err == nil {
			t.Error("the history can be edited")
		}
		if _, err := e.system.Exec(ctx, `DELETE FROM support_events`); err == nil {
			t.Error("the history can be deleted")
		}
	})

	t.Run("an account has at most 20 tickets waiting, also when they are opened at once, and solving one frees a place", func(t *testing.T) {
		full := e.verified(t, "desk-full@example.com", "Full Co")
		var first *plansv1.SupportTicket
		for i := 0; i < domain.SupportMaxOpen; i++ {
			tk := open(full.id, full.account, "Waiting ticket "+strconv.Itoa(i))
			if first == nil {
				first = tk
			}
		}
		_, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: full.id, AccountId: full.account, Subject: "One too many", Category: "other", Body: "x"})
		wantCode(t, "the 21st ticket", err, codes.ResourceExhausted, "support.tickets.too_many")
		// Another account is not affected; a solved ticket does not count.
		open(ada.id, ada.account, "Another account is fine")
		if _, err := e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: full.id, TicketId: first.GetId(), Status: "solved"}); err != nil {
			t.Fatal(err)
		}
		open(full.id, full.account, "A place was freed")

		race := e.verified(t, "desk-race@example.com", "Race Co")
		var wg sync.WaitGroup
		var opened, refused atomic.Int32
		for i := 0; i < domain.SupportMaxOpen+8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: race.id, AccountId: race.account, Subject: "At the same moment", Category: "other", Body: "x"})
				switch {
				case err == nil:
					opened.Add(1)
				case errkey.Of(err) == "support.tickets.too_many":
					refused.Add(1)
				default:
					t.Errorf("open: %v", err)
				}
			}()
		}
		wg.Wait()
		if opened.Load() != domain.SupportMaxOpen || refused.Load() != 8 {
			t.Errorf("opened %d and refused %d at once, want %d and 8", opened.Load(), refused.Load(), domain.SupportMaxOpen)
		}
	})

	t.Run("a ticket that waited for the person for 7 days closes by itself, is mailed and is in the history; the others stay", func(t *testing.T) {
		closer := e.verified(t, "desk-close@example.com", "Close Co")
		pendingSince := func(subject, lastMessage, becamePending string) string {
			t.Helper()
			tk := open(closer.id, closer.account, subject)
			if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET status = 'pending', last_author = 'operator', last_message_at = now() - $2::interval WHERE id = $1::uuid`, tk.GetId(), lastMessage); err != nil {
				t.Fatal(err)
			}
			if becamePending != "" {
				if _, err := e.system.Exec(ctx, `INSERT INTO support_events (id, ticket_id, kind, actor_kind, actor_name, from_value, to_value, created_at)
					VALUES (gen_random_uuid(), $1::uuid, 'status', 'operator', 'Sam', 'open', 'pending', now() - $2::interval)`, tk.GetId(), becamePending); err != nil {
					t.Fatal(err)
				}
			}
			return tk.GetId()
		}
		stale := pendingSince("Waiting 8 days", "8 days", "8 days")
		noHistory := pendingSince("Waiting 8 days since before the history", "8 days", "")
		recent := pendingSince("Waiting 6 days", "6 days", "6 days")
		setPending := pendingSince("Set to waiting again yesterday", "30 days", "1 day")
		waitingForUs := open(closer.id, closer.account, "Waiting for the operator for 30 days").GetId()
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET last_message_at = now() - interval '30 days' WHERE id = $1::uuid`, waitingForUs); err != nil {
			t.Fatal(err)
		}

		job := support.New(e.system, slog.New(slog.NewTextHandler(io.Discard, nil)), support.Config{Mail: e.mail, AppURL: testAppURL})
		n, err := job.CloseStale(ctx, 7*24*time.Hour)
		if err != nil || n != 2 {
			t.Fatalf("closed %d: %v", n, err)
		}
		status := func(tid string) *plansv1.GetSupportTicketResponse {
			r, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: tid})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		for tid, want := range map[string]string{stale: "closed", noHistory: "closed", recent: "pending", setPending: "pending", waitingForUs: "open"} {
			if got := status(tid).GetTicket().GetStatus(); got != want {
				t.Errorf("ticket %s is %s, want %s", tid, got, want)
			}
		}
		evs := status(stale).GetEvents()
		if last := evs[len(evs)-1]; last.GetActorKind() != "system" || last.GetFromValue() != "pending" || last.GetToValue() != "closed" {
			t.Errorf("history of the closed ticket: %v", evs)
		}
		if status(stale).GetTicket().GetSolveTime() == nil {
			t.Error("a closed ticket has no solve time")
		}
		if mails := e.mail.to(usermail.KindTicketStatus, closer.email); len(mails) != 2 || mails[0].Status != "closed" {
			t.Errorf("mails to the opener: %+v", mails)
		}
		if again, err := job.CloseStale(ctx, 7*24*time.Hour); err != nil || again != 0 {
			t.Errorf("a second pass closed %d: %v", again, err)
		}
		if !mine(closer.id)[stale].GetUnread() {
			t.Error("the closing is news to the opener")
		}
	})
}
