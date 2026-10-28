//go:build integration

package itest

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
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

	open, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, AppId: created.GetApp().GetId(),
		Subject: "Paddle webhooks are late", Category: "integration", Priority: "high", Body: "Since this morning."})
	if err != nil {
		t.Fatal(err)
	}
	tk := open.GetTicket()

	t.Run("a new ticket is open, numbered and has a first-response deadline by priority", func(t *testing.T) {
		if tk.GetStatus() != "open" || tk.GetNumber() == 0 || tk.GetAppName() != "Shop" || tk.GetMessageCount() != 1 {
			t.Fatalf("ticket: %+v", tk)
		}
		due := tk.GetFirstResponseDue().AsTime().Sub(tk.GetCreateTime().AsTime())
		if due < 3*time.Hour+59*time.Minute || due > 4*time.Hour+time.Minute {
			t.Errorf("high priority is due in %v, want 4h", due)
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
		urgent, who := "urgent", "Sam"
		u, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Priority: &urgent, Assignee: &who})
		if err != nil || u.GetTicket().GetPriority() != "urgent" || u.GetTicket().GetAssignee() != "Sam" {
			t.Fatalf("update: %v %+v", err, u.GetTicket())
		}
		due := u.GetTicket().GetFirstResponseDue().AsTime().Sub(u.GetTicket().GetCreateTime().AsTime())
		if due < 59*time.Minute || due > 61*time.Minute {
			t.Errorf("urgent is due in %v, want 1h", due)
		}
		// Push the deadline into the past: the ticket is now late and the filter finds it.
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET first_response_due = now() - interval '1 minute' WHERE id = $1::uuid`, id); err != nil {
			t.Fatal(err)
		}
		late, err := e.support.ListSupportTickets(operator(), &plansv1.ListSupportTicketsRequest{SlaBreached: true, Assignee: "Sam"})
		if err != nil || len(late.GetTickets()) != 1 || late.GetTickets()[0].GetId() != id || !late.GetTickets()[0].GetSlaBreached() {
			t.Fatalf("late tickets: %v %v", err, late.GetTickets())
		}
		bad := "asap"
		_, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Priority: &bad})
		wantCode(t, "unknown priority", err, codes.InvalidArgument, "plans.request.invalid")
	})
}
