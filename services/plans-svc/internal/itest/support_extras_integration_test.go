//go:build integration

package itest

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/support"
)

// The help desk's third step (plan 12): the operators are mailed when the person answers them, the person's unread count, tags the person never sees, an assignee
// chosen from the operators, the report of the queue, canned replies, and the gauge an alert watches. The tables are shared with the other support tests, so every
// count here is a difference, never a total.
func testSupportDeskExtras(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "extras-ada@example.com", "Extras Co")
	open := func(subject string) *plansv1.SupportTicket {
		t.Helper()
		r, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, Subject: subject, Category: "question", Body: "Hello"})
		if err != nil {
			t.Fatalf("open %q: %v", subject, err)
		}
		return r.GetTicket()
	}
	answer := func(id string) {
		t.Helper()
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: id, Body: "On it.", AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
	}
	reply := func(id string) {
		t.Helper()
		if _, err := e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: ada.id, TicketId: id, Body: "More detail."}); err != nil {
			t.Fatal(err)
		}
	}
	report := func() *plansv1.GetSupportReportResponse {
		t.Helper()
		r, err := e.support.GetSupportReport(operator(), &plansv1.GetSupportReportRequest{})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	unread := func() int32 {
		t.Helper()
		r, err := e.support.CountMySupportUnread(ctx, &plansv1.CountMySupportUnreadRequest{UserId: ada.id})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetUnread()
	}
	tk := open("Webhooks stopped")
	id := tk.GetId()

	t.Run("the operators are mailed when the person answers the operator, once until the operator answers again", func(t *testing.T) {
		replies := func() int { return len(e.mail.to(usermail.KindTicketCustomerReply, testOperatorEmail)) }
		before := replies()
		reply(id) // the ticket waits for the operator already: nothing new to say
		if replies() != before {
			t.Fatalf("the person's second message in a row mailed the operators (%d)", replies()-before)
		}
		answer(id)
		reply(id)
		got := e.mail.to(usermail.KindTicketCustomerReply, testOperatorEmail)
		if len(got) != before+1 {
			t.Fatalf("operator mails after the person answered: %d, want %d", len(got), before+1)
		}
		m := got[len(got)-1]
		if m.Ticket != "#"+strings.TrimPrefix(m.Ticket, "#") || m.Account != "Extras Co" || m.Subject != "Webhooks stopped" || m.Link != testAppURL+"/support?sheet=ticket:"+id {
			t.Errorf("operator mail: %+v", m)
		}
		reply(id)
		if replies() != before+1 {
			t.Error("a second message in a row mailed the operators again")
		}
		answer(id)
		reply(id)
		if replies() != before+2 {
			t.Errorf("after another answer the person's reply was not mailed: %d", replies()-before)
		}
	})

	t.Run("the person's unread count follows the operator's answers and reading the ticket clears it", func(t *testing.T) {
		if unread() != 0 {
			t.Fatalf("unread before any answer = %d", unread())
		}
		answer(id)
		if unread() != 1 {
			t.Fatalf("unread after an answer = %d", unread())
		}
		if _, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: id}); err != nil {
			t.Fatal(err)
		}
		if unread() != 0 {
			t.Errorf("unread after reading = %d", unread())
		}
		// Someone who is not a member of the account is told nothing about it.
		stranger := e.verified(t, "extras-stranger@example.com", "Stranger Co")
		if r, err := e.support.CountMySupportUnread(ctx, &plansv1.CountMySupportUnreadRequest{UserId: stranger.id}); err != nil || r.GetUnread() != 0 {
			t.Errorf("a stranger's count: %v %v", r, err)
		}
		if _, err := e.support.CountMySupportUnread(ctx, &plansv1.CountMySupportUnreadRequest{UserId: "nope"}); err == nil {
			t.Error("a count for a user id that is none")
		}
	})

	t.Run("tags are the operator's: normalised, in the history, filterable, and never in what the person sees", func(t *testing.T) {
		r, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, SetTags: true, Tags: []string{" Refund", "vat", "refund", ""}, AuthorName: "Sam"})
		if err != nil || !reflect.DeepEqual(r.GetTicket().GetTags(), []string{"refund", "vat"}) {
			t.Fatalf("set tags: %v %v", r.GetTicket().GetTags(), err)
		}
		list, err := e.support.ListSupportTickets(operator(), &plansv1.ListSupportTicketsRequest{Tag: "VAT"})
		found := false
		for _, x := range list.GetTickets() {
			found = found || x.GetId() == id
		}
		if err != nil || !found {
			t.Errorf("filter by tag: %v found=%v", err, found)
		}
		if none, _ := e.support.ListSupportTickets(operator(), &plansv1.ListSupportTicketsRequest{Tag: "no-such-tag"}); len(none.GetTickets()) != 0 {
			t.Errorf("a tag nobody has found %d tickets", len(none.GetTickets()))
		}
		full, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: id})
		var change *plansv1.SupportEvent
		for _, ev := range full.GetEvents() {
			if ev.GetKind() == "tags" {
				change = ev
			}
		}
		if err != nil || change == nil || change.GetFromValue() != "" || change.GetToValue() != "refund,vat" || change.GetActorName() != "Sam" {
			t.Errorf("history of the tags: %v %+v", err, change)
		}
		// Another update that leaves the tags alone leaves them; clearing them is its own change.
		if r, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Priority: ptr("high")}); err != nil || len(r.GetTicket().GetTags()) != 2 {
			t.Errorf("an update without set_tags: %v %v", r.GetTicket().GetTags(), err)
		}
		// None of the person's calls shows a tag.
		mineOne, _ := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: id})
		mineList, _ := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: ada.id})
		replied, _ := e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: ada.id, TicketId: id, Body: "Thanks"})
		if len(mineOne.GetTicket().GetTags()) != 0 || len(replied.GetTicket().GetTags()) != 0 {
			t.Errorf("the person saw tags: %v %v", mineOne.GetTicket().GetTags(), replied.GetTicket().GetTags())
		}
		for _, x := range mineList.GetTickets() {
			if len(x.GetTags()) != 0 {
				t.Errorf("the person's list shows tags on %s: %v", x.GetId(), x.GetTags())
			}
		}
		for name, tags := range map[string][]string{"a word that is no tag": {"Bad Tag!"}, "too many": {"a", "b", "c", "d", "e", "f"}, "too long": {strings.Repeat("x", 31)}} {
			_, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, SetTags: true, Tags: tags})
			wantCode(t, name, err, codes.InvalidArgument, "plans.request.invalid")
		}
		if r, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, SetTags: true}); err != nil || len(r.GetTicket().GetTags()) != 0 {
			t.Errorf("clearing the tags: %v %v", r.GetTicket().GetTags(), err)
		}
	})

	t.Run("the assignee is one of the operators, or empty, or what it was", func(t *testing.T) {
		ops, err := e.support.ListSupportOperators(operator(), &plansv1.ListSupportOperatorsRequest{})
		if err != nil || len(ops.GetOperators()) != 1 || ops.GetOperators()[0].GetEmail() != testOperatorEmail {
			t.Fatalf("operators: %v %v", ops, err)
		}
		r, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Assignee: ptr("OPS@example.com"), AuthorName: "Sam"})
		if err != nil || r.GetTicket().GetAssignee() != testOperatorEmail {
			t.Fatalf("assign to an operator: %v %v", r.GetTicket().GetAssignee(), err)
		}
		_, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Assignee: ptr("stranger@example.com")})
		wantCode(t, "assign to somebody who is no operator", err, codes.InvalidArgument, "plans.request.invalid")
		// An assignee from before the list was an address (a name typed by hand) stays until somebody changes it.
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET assignee = 'Sam the intern' WHERE id = $1::uuid`, id); err != nil {
			t.Fatal(err)
		}
		if r, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Assignee: ptr("Sam the intern")}); err != nil || r.GetTicket().GetAssignee() != "Sam the intern" {
			t.Errorf("keeping an old assignee: %v %v", r.GetTicket().GetAssignee(), err)
		}
		if r, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: id, Assignee: ptr("")}); err != nil || r.GetTicket().GetAssignee() != "" {
			t.Errorf("clearing the assignee: %v %v", r.GetTicket().GetAssignee(), err)
		}
		if _, err := e.support.ListSupportOperators(ctx, &plansv1.ListSupportOperatorsRequest{}); err == nil {
			t.Error("the operators were listed without the operator context")
		}
	})

	t.Run("the report counts the queue and the first answers", func(t *testing.T) {
		before := report()
		fresh := open("Needs an answer")
		late := open("Is late")
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET first_response_due = now() - interval '2 hours' WHERE id = $1::uuid`, late.GetId()); err != nil {
			t.Fatal(err)
		}
		mid := report()
		if mid.GetWaiting() != before.GetWaiting()+2 || mid.GetBreached() != before.GetBreached()+1 || mid.GetOpened() != before.GetOpened()+2 || mid.GetWindowDays() != 30 {
			t.Fatalf("after two tickets: waiting %d→%d, breached %d→%d, opened %d→%d", before.GetWaiting(), mid.GetWaiting(), before.GetBreached(), mid.GetBreached(), before.GetOpened(), mid.GetOpened())
		}
		count := func(r *plansv1.GetSupportReportResponse, status string) int32 {
			for _, c := range r.GetByStatus() {
				if c.GetValue() == status {
					return c.GetCount()
				}
			}
			return 0
		}
		answer(fresh.GetId()) // in time
		answer(late.GetId())  // late: answered, not on time
		after := report()
		if after.GetWaiting() != mid.GetWaiting()-2 || after.GetBreached() != mid.GetBreached()-1 || count(after, "pending") != count(mid, "pending")+2 ||
			after.GetAnswered() != mid.GetAnswered()+2 || after.GetAnsweredOnTime() != mid.GetAnsweredOnTime()+1 {
			t.Errorf("after two answers: waiting %d→%d, breached %d→%d, answered %d→%d, on time %d→%d", mid.GetWaiting(), after.GetWaiting(), mid.GetBreached(), after.GetBreached(),
				mid.GetAnswered(), after.GetAnswered(), mid.GetAnsweredOnTime(), after.GetAnsweredOnTime())
		}
		if _, err := e.support.GetSupportReport(ctx, &plansv1.GetSupportReportRequest{}); err == nil {
			t.Error("the report was served without the operator context")
		}
	})

	t.Run("the gauge counts the late tickets by priority, and a priority with none says 0", func(t *testing.T) {
		reader := metric.NewManualReader()
		prev := otel.GetMeterProvider()
		otel.SetMeterProvider(metric.NewMeterProvider(metric.WithReader(reader)))
		t.Cleanup(func() { otel.SetMeterProvider(prev) })
		desk := support.New(e.system, slog.New(slog.NewTextHandler(io.Discard, nil)), support.Config{})
		if err := desk.RegisterMetrics(); err != nil {
			t.Fatal(err)
		}
		late := open("Late and urgent")
		if _, err := e.system.Exec(ctx, `UPDATE support_tickets SET priority = 'urgent', first_response_due = now() - interval '3 hours' WHERE id = $1::uuid`, late.GetId()); err != nil {
			t.Fatal(err)
		}
		gauge := func(name string) map[string]int64 {
			t.Helper()
			var rm metricdata.ResourceMetrics
			if err := reader.Collect(ctx, &rm); err != nil {
				t.Fatal(err)
			}
			out := map[string]int64{}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if g, ok := m.Data.(metricdata.Gauge[int64]); ok && m.Name == name {
						for _, dp := range g.DataPoints {
							p, _ := dp.Attributes.Value("priority")
							out[p.AsString()] = dp.Value
						}
					}
				}
			}
			return out
		}
		got := gauge("support_sla_breached_tickets")
		if got["urgent"] < 1 {
			t.Errorf("late tickets by priority = %v, want at least one urgent", got)
		}
		for _, p := range []string{"low", "normal", "high", "urgent"} {
			if _, ok := got[p]; !ok {
				t.Errorf("priority %s has no reading: an alert on it would never resolve (%v)", p, got)
			}
		}
		if w := gauge("support_waiting_tickets"); w[""] < 1 {
			t.Errorf("waiting tickets = %v", w)
		}
	})

	t.Run("canned replies are the operators' to make, change and delete", func(t *testing.T) {
		list := func() []*plansv1.SupportCannedReply {
			t.Helper()
			r, err := e.support.ListSupportCannedReplies(operator(), &plansv1.ListSupportCannedRepliesRequest{})
			if err != nil {
				t.Fatal(err)
			}
			return r.GetReplies()
		}
		before := len(list())
		made, err := e.support.SaveSupportCannedReply(operator(), &plansv1.SaveSupportCannedReplyRequest{Title: "Asking for logs", Body: "Could you send the webhook delivery ids?", AuthorName: "Sam"})
		if err != nil || made.GetReply().GetId() == "" || made.GetReply().GetUpdatedBy() != "Sam" {
			t.Fatalf("make: %v %v", made, err)
		}
		if len(list()) != before+1 {
			t.Fatalf("after making one: %d, want %d", len(list()), before+1)
		}
		changed, err := e.support.SaveSupportCannedReply(operator(), &plansv1.SaveSupportCannedReplyRequest{Id: made.GetReply().GetId(), Title: "Asking for logs (webhooks)", Body: "Please send the delivery ids.", AuthorName: "Kim"})
		if err != nil || changed.GetReply().GetTitle() != "Asking for logs (webhooks)" || changed.GetReply().GetUpdatedBy() != "Kim" || len(list()) != before+1 {
			t.Fatalf("change: %v %v", changed, err)
		}
		_, err = e.support.SaveSupportCannedReply(operator(), &plansv1.SaveSupportCannedReplyRequest{Title: " ", Body: "x"})
		wantCode(t, "no title", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.support.SaveSupportCannedReply(operator(), &plansv1.SaveSupportCannedReplyRequest{Title: "x", Body: ""})
		wantCode(t, "no text", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = e.support.SaveSupportCannedReply(operator(), &plansv1.SaveSupportCannedReplyRequest{Id: "0192b7a0-0000-7000-8000-000000000000", Title: "x", Body: "y"})
		wantCode(t, "a reply that is not there", err, codes.NotFound, "support.canned_reply.not_found")
		if _, err := e.support.ListSupportCannedReplies(ctx, &plansv1.ListSupportCannedRepliesRequest{}); err == nil {
			t.Error("canned replies were listed without the operator context")
		}
		if _, err := e.support.DeleteSupportCannedReply(operator(), &plansv1.DeleteSupportCannedReplyRequest{Id: made.GetReply().GetId()}); err != nil || len(list()) != before {
			t.Errorf("delete: %v, %d left, want %d", err, len(list()), before)
		}
		_, err = e.support.DeleteSupportCannedReply(operator(), &plansv1.DeleteSupportCannedReplyRequest{Id: made.GetReply().GetId()})
		wantCode(t, "deleting twice", err, codes.NotFound, "support.canned_reply.not_found")
	})
}

func ptr[T any](v T) *T { return &v }
