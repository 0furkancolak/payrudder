//go:build integration

package itest

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// The help desk's fourth step (plan 12): the first-answer deadline by the plan of the app and not on its days off, merging two tickets of one person, a rating after
// a ticket is solved, a search over the words of the messages, and links to a customer, an invoice or a subscription. The tables are shared with the other support
// tests, so counts are differences.
func testSupportLeftovers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ada := e.verified(t, "left-ada@example.com", "Leftovers Co")
	stranger := e.verified(t, "left-stranger@example.com", "Stranger Co")
	colleague := e.signUp(t, "left-colleague@example.com", "Colleague Co")
	if _, err := e.system.Exec(ctx, `INSERT INTO account_members (account_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'admin')`, ada.account, colleague.GetUser().GetId()); err != nil {
		t.Fatal(err)
	}
	app, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: ada.id, AccountId: ada.account, Name: "Shop", Currency: "EUR", Timezone: "Europe/Berlin"})
	if err != nil {
		t.Fatal(err)
	}
	appID := app.GetApp().GetId()
	strangerApp, err := e.ident.CreateMyApp(ctx, &plansv1.CreateMyAppRequest{UserId: stranger.id, AccountId: stranger.account, Name: "Elsewhere", Currency: "EUR", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	tenantOf := func(app, env string) string {
		t.Helper()
		var id string
		if err := e.system.QueryRow(ctx, `SELECT id::text FROM tenants WHERE app_id = $1::uuid AND environment = $2`, app, env).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	open := func(who person, subject, body string, links ...*plansv1.SupportLink) *plansv1.SupportTicket {
		t.Helper()
		r, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: who.id, AccountId: who.account, AppId: appID, Subject: subject, Category: "question", Priority: "high", Body: body, Links: links})
		if err != nil {
			t.Fatalf("open %q: %v", subject, err)
		}
		return r.GetTicket()
	}
	operatorGet := func(id string) *plansv1.GetSupportTicketResponse {
		t.Helper()
		r, err := e.support.GetSupportTicket(operator(), &plansv1.GetSupportTicketRequest{TicketId: id})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	solve := func(who person, id string) {
		t.Helper()
		if _, err := e.support.SetMySupportTicketStatus(ctx, &plansv1.SetMySupportTicketStatusRequest{UserId: who.id, TicketId: id, Status: "solved"}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("the first-answer deadline follows the plan of the ticket's app", func(t *testing.T) {
		live := tenantOf(appID, "live")
		dueAt := func(tier plansv1.TenantTier) (*plansv1.SupportTicket, time.Time, time.Time) {
			t.Helper()
			if _, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live, Tier: tier}); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			tk := open(ada, "Tier "+tier.String(), "x")
			return tk, before, time.Now()
		}
		for _, c := range []struct {
			tier plansv1.TenantTier
			name string
		}{{plansv1.TenantTier_TENANT_TIER_FREE, "free"}, {plansv1.TenantTier_TENANT_TIER_STANDARD, "standard"}, {plansv1.TenantTier_TENANT_TIER_ENTERPRISE, "enterprise"}} {
			name := c.name
			tk, before, after := dueAt(c.tier)
			// The same bracket the standard test uses: the deadline of the moment before and the moment after the call, counted for that plan.
			lo, hi := domain.SupportFirstResponseDue(before, name, "high", "Europe/Berlin", nil), domain.SupportFirstResponseDue(after, name, "high", "Europe/Berlin", nil)
			if due := tk.GetFirstResponseDue().AsTime(); due.Before(lo) || due.After(hi) {
				t.Errorf("%s: due %v, want between %v and %v", name, due, lo, hi)
			}
			if got := operatorGet(tk.GetId()).GetTicket().GetSlaTier(); got != name {
				t.Errorf("%s: the operator sees the plan %q", name, got)
			}
			// The person is not told which plan the table was read for.
			if mine, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()}); err != nil || mine.GetTicket().GetSlaTier() != "" {
				t.Errorf("%s: the person's view has the plan %q (%v)", name, mine.GetTicket().GetSlaTier(), err)
			}
		}
		// A free app waits longer than a standard one, and that longer than an enterprise one, for the same priority at the same moment.
		now := time.Now()
		free, std, ent := domain.SupportFirstResponseDue(now, "free", "high", "Europe/Berlin", nil), domain.SupportFirstResponseDue(now, "standard", "high", "Europe/Berlin", nil), domain.SupportFirstResponseDue(now, "enterprise", "high", "Europe/Berlin", nil)
		if !free.After(std) || !std.After(ent) {
			t.Errorf("free %v, standard %v, enterprise %v are not in order", free, std, ent)
		}
		// A ticket about the account as a whole takes the best plan among its apps' live tenants (the last loop left the app on enterprise); an account with no app
		// at all has the plan of nobody: free.
		bare, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, Subject: "No app", Category: "question", Body: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if got := operatorGet(bare.GetTicket().GetId()).GetTicket().GetSlaTier(); got != "enterprise" {
			t.Errorf("an account-wide ticket took the plan %q", got)
		}
		nobody := e.verified(t, "left-nobody@example.com", "Nobody Co")
		none, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: nobody.id, AccountId: nobody.account, Subject: "No app at all", Category: "question", Body: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if got := operatorGet(none.GetTicket().GetId()).GetTicket().GetSlaTier(); got != "free" {
			t.Errorf("an account with no app took the plan %q", got)
		}
		if _, err := e.admin.UpdateTenantSettings(operator(), &plansv1.UpdateTenantSettingsRequest{TenantId: live, Tier: plansv1.TenantTier_TENANT_TIER_STANDARD}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the days off of an app stop the clock; only the operator keeps them", func(t *testing.T) {
		days := func(n int) []*plansv1.SupportHoliday {
			var out []*plansv1.SupportHoliday
			for i := -1; i < n; i++ {
				out = append(out, &plansv1.SupportHoliday{Day: time.Now().AddDate(0, 0, i).UTC().Format("2006-01-02"), Name: "Closed"})
			}
			return out
		}
		set := func(h []*plansv1.SupportHoliday) error {
			_, err := e.support.SetSupportHolidays(operator(), &plansv1.SetSupportHolidaysRequest{AppId: appID, Holidays: h, AuthorName: "Sam"})
			return err
		}
		_, err := e.support.SetSupportHolidays(ctx, &plansv1.SetSupportHolidaysRequest{AppId: appID})
		wantCode(t, "a person setting the days off", err, codes.PermissionDenied, "")
		_, err = e.support.GetSupportHolidays(ctx, &plansv1.GetSupportHolidaysRequest{AppId: appID})
		wantCode(t, "a person reading the days off", err, codes.PermissionDenied, "")
		_, err = e.support.SetSupportHolidays(operator(), &plansv1.SetSupportHolidaysRequest{AppId: "00000000-0000-4000-8000-00000000dead"})
		wantCode(t, "an app that does not exist", err, codes.NotFound, "support.app.not_found")
		wantCode(t, "a day that is no date", set([]*plansv1.SupportHoliday{{Day: "tomorrow"}}), codes.InvalidArgument, "plans.request.invalid")
		wantCode(t, "the same day twice", set([]*plansv1.SupportHoliday{{Day: "2026-12-25"}, {Day: "2026-12-25"}}), codes.InvalidArgument, "plans.request.invalid")

		// Every day of the next three weeks is off: a ticket opened now is due after them (without the days it would be due within a few days).
		if err := set(days(21)); err != nil {
			t.Fatal(err)
		}
		got, err := e.support.GetSupportHolidays(operator(), &plansv1.GetSupportHolidaysRequest{AppId: appID})
		if err != nil || got.GetAppName() != "Shop" || len(got.GetHolidays()) != 22 || got.GetHolidays()[0].GetDay() > got.GetHolidays()[1].GetDay() {
			t.Fatalf("the days off: %v, %v", got, err)
		}
		tk := open(ada, "During the closure", "x")
		berlin, _ := time.LoadLocation("Europe/Berlin")
		last := got.GetHolidays()[len(got.GetHolidays())-1].GetDay()
		if due := tk.GetFirstResponseDue().AsTime().In(berlin).Format("2006-01-02"); due <= last {
			t.Fatalf("a ticket opened in a closure that lasts until %s is due on %s", last, due)
		}
		// Removing the days and changing the priority counts the deadline again, now without them.
		if err := set(nil); err != nil {
			t.Fatal(err)
		}
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: tk.GetId(), Priority: ptr("urgent"), AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		if due := operatorGet(tk.GetId()).GetTicket().GetFirstResponseDue().AsTime(); due.After(time.Now().AddDate(0, 0, 5)) {
			t.Errorf("after the days were removed the urgent deadline is %v", due)
		}
		var audits int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'support.holidays_set' AND target_id = $1`, appID).Scan(&audits); err != nil || audits < 2 {
			t.Errorf("audit entries for the days off: %d, %v", audits, err)
		}
	})

	t.Run("two tickets of one person are merged: the messages move, the first closes with a pointer, and nothing else can be merged", func(t *testing.T) {
		t1 := open(ada, "Webhooks late", "Since this morning.")
		t2 := open(ada, "Webhooks again", "Same problem, more detail.")
		t3 := open(ada, "Third, for the closed-target case", "x")
		theirs := open(person{id: colleague.GetUser().GetId(), account: ada.account}, "A colleague's ticket", "x")
		// t1 was answered (pending); t2 waits for the operator.
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: t1.GetId(), Body: "Looking.", AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		merge := func(from, into string) (*plansv1.MergeSupportTicketsResponse, error) {
			return e.support.MergeSupportTickets(operator(), &plansv1.MergeSupportTicketsRequest{TicketId: from, IntoTicketId: into, AuthorName: "Sam"})
		}
		_, err := e.support.MergeSupportTickets(ctx, &plansv1.MergeSupportTicketsRequest{TicketId: t2.GetId(), IntoTicketId: t1.GetId()})
		wantCode(t, "a person merging", err, codes.PermissionDenied, "")
		_, err = merge(t2.GetId(), t2.GetId())
		wantCode(t, "a ticket into itself", err, codes.FailedPrecondition, "support.ticket.cannot_merge")
		_, err = merge(t2.GetId(), theirs.GetId())
		wantCode(t, "another person's ticket in the same account", err, codes.FailedPrecondition, "support.ticket.cannot_merge")
		_, err = merge(t2.GetId(), "00000000-0000-4000-8000-00000000dead")
		wantCode(t, "a ticket that does not exist", err, codes.NotFound, "support.ticket.not_found")

		done, err := merge(t2.GetId(), t1.GetId())
		if err != nil {
			t.Fatal(err)
		}
		if done.GetTicket().GetStatus() != "closed" || done.GetTicket().GetMergedIntoId() != t1.GetId() || done.GetTicket().GetMergedIntoNumber() != t1.GetNumber() {
			t.Fatalf("the merged ticket: %v", done.GetTicket())
		}
		// The thread is in one place, oldest first, and a person's message that waited for the operator reopened the ticket that was waiting for the person.
		into := operatorGet(t1.GetId())
		var bodies []string
		for _, m := range into.GetMessages() {
			bodies = append(bodies, m.GetBody())
		}
		if strings.Join(bodies, "|") != "Since this morning.|Looking.|Same problem, more detail." && strings.Join(bodies, "|") != "Since this morning.|Same problem, more detail.|Looking." {
			t.Errorf("the thread of the ticket that kept it: %v", bodies)
		}
		if into.GetTicket().GetStatus() != "open" || into.GetTicket().GetLastAuthor() != "customer" {
			t.Errorf("the ticket that kept the thread: %v %v", into.GetTicket().GetStatus(), into.GetTicket().GetLastAuthor())
		}
		if left := operatorGet(t2.GetId()); len(left.GetMessages()) != 0 {
			t.Errorf("the merged ticket still holds %d messages", len(left.GetMessages()))
		}
		// Both histories say it, and the person sees the merge (it is theirs) in the allow-listed history.
		kinds := func(r *plansv1.GetMySupportTicketResponse) string {
			var out []string
			for _, ev := range r.GetEvents() {
				if ev.GetKind() == "merge" {
					out = append(out, ev.GetFromValue()+">"+ev.GetToValue())
				}
			}
			return strings.Join(out, ",")
		}
		mineInto, _ := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: t1.GetId()})
		mineFrom, _ := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: t2.GetId()})
		if kinds(mineInto) != "#"+itoa(t2.GetNumber())+">" || kinds(mineFrom) != ">#"+itoa(t1.GetNumber()) || mineFrom.GetTicket().GetMergedIntoNumber() != t1.GetNumber() {
			t.Errorf("history: %q and %q (t1 #%d, t2 #%d, pointer #%d)", kinds(mineInto), kinds(mineFrom), t1.GetNumber(), t2.GetNumber(), mineFrom.GetTicket().GetMergedIntoNumber())
		}
		// A merged ticket takes no reply, from either side, and no change.
		_, err = e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: t2.GetId(), Body: "x"})
		wantCode(t, "the operator writing to a merged ticket", err, codes.FailedPrecondition, "support.ticket.merged")
		_, err = e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: t2.GetId(), Priority: ptr("low")})
		wantCode(t, "the operator changing a merged ticket", err, codes.FailedPrecondition, "support.ticket.merged")
		_, err = e.support.ReplyMySupportTicket(ctx, &plansv1.ReplyMySupportTicketRequest{UserId: ada.id, TicketId: t2.GetId(), Body: "x"})
		wantCode(t, "the person writing to a merged ticket", err, codes.FailedPrecondition, "")
		_, err = merge(t2.GetId(), t3.GetId())
		wantCode(t, "a ticket merged already", err, codes.FailedPrecondition, "support.ticket.cannot_merge")
		_, err = merge(t3.GetId(), t2.GetId())
		wantCode(t, "into a ticket that was merged", err, codes.FailedPrecondition, "support.ticket.cannot_merge")
		// A closed ticket cannot take messages.
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: t3.GetId(), Status: ptr("closed")}); err != nil {
			t.Fatal(err)
		}
		_, err = merge(t1.GetId(), t3.GetId())
		wantCode(t, "into a closed ticket", err, codes.FailedPrecondition, "support.ticket.cannot_merge")
	})

	t.Run("a solved ticket is rated; an open one takes no rating; the report counts them", func(t *testing.T) {
		tk := open(ada, "Rate me", "x")
		rate := func(who person, id string, n int32, comment string) (*plansv1.RateMySupportTicketResponse, error) {
			return e.support.RateMySupportTicket(ctx, &plansv1.RateMySupportTicketRequest{UserId: who.id, TicketId: id, Rating: n, Comment: comment})
		}
		report := func() *plansv1.GetSupportReportResponse {
			t.Helper()
			r, err := e.support.GetSupportReport(operator(), &plansv1.GetSupportReportRequest{})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		count := func(r *plansv1.GetSupportReportResponse, star string) int32 {
			for _, c := range r.GetRatings() {
				if c.GetValue() == star {
					return c.GetCount()
				}
			}
			return -1
		}
		_, err := rate(ada, tk.GetId(), 4, "")
		wantCode(t, "rating an open ticket", err, codes.FailedPrecondition, "support.ticket.not_solved")
		solve(ada, tk.GetId())
		for what, n := range map[string]int32{"zero": 0, "six": 6, "negative": -1} {
			_, err := rate(ada, tk.GetId(), n, "")
			wantCode(t, what, err, codes.InvalidArgument, "plans.request.invalid")
		}
		_, err = rate(ada, tk.GetId(), 3, strings.Repeat("x", 1001))
		wantCode(t, "a comment of 1001 characters", err, codes.InvalidArgument, "plans.request.invalid")
		_, err = rate(stranger, tk.GetId(), 5, "")
		if err == nil {
			t.Error("a person of another account rated the ticket")
		}
		before := report()
		got, err := rate(ada, tk.GetId(), 4, "  Fast and clear.  ")
		if err != nil || got.GetTicket().GetRating() != 4 || got.GetTicket().GetRatingComment() != "Fast and clear." || got.GetTicket().GetRateTime() == nil {
			t.Fatalf("rated: %v, %v", got, err)
		}
		after := report()
		if after.GetRated() != before.GetRated()+1 || count(after, "4") != count(before, "4")+1 || len(after.GetRatings()) != 5 || after.GetAverageRating() < 1 || after.GetAverageRating() > 5 {
			t.Errorf("report: rated %d -> %d, four stars %d -> %d, average %v", before.GetRated(), after.GetRated(), count(before, "4"), count(after, "4"), after.GetAverageRating())
		}
		// A second rating replaces the first; the operator reads it on the ticket.
		if _, err := rate(ada, tk.GetId(), 2, "Slow after all."); err != nil {
			t.Fatal(err)
		}
		again := report()
		if again.GetRated() != after.GetRated() || count(again, "4") != count(before, "4") || count(again, "2") != count(before, "2")+1 {
			t.Errorf("a second rating counted twice: %d, fours %d, twos %d", again.GetRated(), count(again, "4"), count(again, "2"))
		}
		if o := operatorGet(tk.GetId()).GetTicket(); o.GetRating() != 2 || o.GetRatingComment() != "Slow after all." {
			t.Errorf("the operator reads %d %q", o.GetRating(), o.GetRatingComment())
		}
		// A reopened ticket keeps what was said; a ticket rated stays rated when it is read by the other members of the account too.
		if mine, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: colleague.GetUser().GetId(), TicketId: tk.GetId()}); err != nil || mine.GetTicket().GetRating() != 2 {
			t.Errorf("a colleague reads: %v, %v", mine.GetTicket().GetRating(), err)
		}
	})

	t.Run("the search finds the words of the messages: the person's list only the public ones, the operator's the notes too", func(t *testing.T) {
		seen := open(ada, "Plain subject", "The quokkazanzibar webhook retries stopped.")
		noted := open(ada, "Another subject", "Nothing special here.")
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: noted.GetId(), Body: "Refund marmaladeapproved for this customer.", Internal: true, AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.support.ReplySupportTicket(operator(), &plansv1.ReplySupportTicketRequest{TicketId: noted.GetId(), Body: "We fixed the gooseberrypatch for you.", AuthorName: "Sam"}); err != nil {
			t.Fatal(err)
		}
		other := e.verified(t, "left-searcher@example.com", "Searcher Co")
		if _, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: other.id, AccountId: other.account, Subject: "Someone else's", Category: "question", Body: "Their quokkazanzibar too."}); err != nil {
			t.Fatal(err)
		}
		mine := func(search string) map[string]bool {
			t.Helper()
			r, err := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: ada.id, Search: search})
			if err != nil {
				t.Fatalf("search %q: %v", search, err)
			}
			out := map[string]bool{}
			for _, tk := range r.GetTickets() {
				out[tk.GetId()] = true
			}
			return out
		}
		theirs := func(search string) map[string]bool {
			t.Helper()
			r, err := e.support.ListSupportTickets(operator(), &plansv1.ListSupportTicketsRequest{Search: search})
			if err != nil {
				t.Fatalf("operator search %q: %v", search, err)
			}
			out := map[string]bool{}
			for _, tk := range r.GetTickets() {
				out[tk.GetId()] = true
			}
			return out
		}
		if got := mine("quokkazanzibar"); !got[seen.GetId()] || len(got) != 1 {
			t.Errorf("the person's search for a word in a message: %v (their own ticket only, not the other account's)", got)
		}
		if got := mine("gooseberrypatch"); !got[noted.GetId()] || len(got) != 1 {
			t.Errorf("a word in the operator's public answer: %v", got)
		}
		if got := mine("marmaladeapproved"); len(got) != 0 {
			t.Errorf("the person found a word of an internal note: %v", got)
		}
		if got := theirs("marmaladeapproved"); !got[noted.GetId()] || len(got) != 1 {
			t.Errorf("the operator's search for a word in a note: %v", got)
		}
		if got := theirs("quokkazanzibar"); len(got) != 2 {
			t.Errorf("the operator sees the word in both accounts: %v", got)
		}
		// The subject and the number still work, the way people type searches (a phrase, a minus, a hash, quotes, percent signs) never fails, and an empty search lists.
		if got := mine("plain subject"); !got[seen.GetId()] {
			t.Errorf("the subject: %v", got)
		}
		if got := mine("#" + itoa(seen.GetNumber())); !got[seen.GetId()] || len(got) != 1 {
			t.Errorf("the number: %v", got)
		}
		if got := mine(`"quokkazanzibar webhook" -gooseberrypatch`); !got[seen.GetId()] || got[noted.GetId()] {
			t.Errorf("a phrase and a minus: %v", got)
		}
		for _, odd := range []string{`'; DROP TABLE support_tickets; --`, `%`, `_`, `\`, `"`, `((`, `a & | !`, strings.Repeat("x", 500)} {
			mine(odd)
			theirs(odd)
		}
		if got := mine(""); !got[seen.GetId()] || !got[noted.GetId()] {
			t.Errorf("an empty search lists all: %v", got)
		}
	})

	t.Run("a ticket links to a customer, an invoice and a subscription of an app of its account, and only of its account", func(t *testing.T) {
		inv, sub := "0192f0b4-6f6e-7c3a-9a3e-6a1f2b3c4d5e", "0192F0B4-6F6E-7C3A-9A3E-6A1F2B3C4D5F"
		link := func(kind, ref, app, env string) *plansv1.SupportLink {
			return &plansv1.SupportLink{Kind: kind, Ref: ref, AppId: app, Environment: env}
		}
		tk := open(ada, "About some records", "x", link("customer", "cus_1", appID, "live"), link("invoice", inv, appID, "sandbox"), link("customer", "cus_1", appID, "live"))
		got, err := e.support.GetMySupportTicket(ctx, &plansv1.GetMySupportTicketRequest{UserId: ada.id, TicketId: tk.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		links := got.GetTicket().GetLinks()
		if len(links) != 2 || links[0].GetKind() != "customer" || links[0].GetRef() != "cus_1" || links[0].GetTenantId() != tenantOf(appID, "live") || links[0].GetAppName() != "Shop" ||
			links[1].GetKind() != "invoice" || links[1].GetTenantId() != tenantOf(appID, "sandbox") || links[1].GetEnvironment() != "sandbox" {
			t.Fatalf("links (the same one twice counts once): %v", links)
		}
		// The lists carry no links; the operator reads them on the ticket.
		list, _ := e.support.ListMySupportTickets(ctx, &plansv1.ListMySupportTicketsRequest{UserId: ada.id})
		for _, l := range list.GetTickets() {
			if len(l.GetLinks()) != 0 {
				t.Errorf("a list row carries links: %v", l.GetLinks())
			}
		}
		if len(operatorGet(tk.GetId()).GetTicket().GetLinks()) != 2 {
			t.Error("the operator does not read the links")
		}
		// The operator replaces them (a key that is not sent leaves them), and the id of a subscription is kept in lower case.
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: tk.GetId(), Priority: ptr("low")}); err != nil {
			t.Fatal(err)
		}
		if len(operatorGet(tk.GetId()).GetTicket().GetLinks()) != 2 {
			t.Error("a change that did not name links removed them")
		}
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: tk.GetId(), SetLinks: true, Links: []*plansv1.SupportLink{link("subscription", sub, appID, "live")}}); err != nil {
			t.Fatal(err)
		}
		if l := operatorGet(tk.GetId()).GetTicket().GetLinks(); len(l) != 1 || l[0].GetRef() != strings.ToLower(sub) {
			t.Errorf("replaced: %v", l)
		}
		if _, err := e.support.UpdateSupportTicket(operator(), &plansv1.UpdateSupportTicketRequest{TicketId: tk.GetId(), SetLinks: true}); err != nil || len(operatorGet(tk.GetId()).GetTicket().GetLinks()) != 0 {
			t.Errorf("an empty list did not clear the links: %v", err)
		}
		// What is refused: another account's app, an app that is not an id, too many, a kind that does not exist, an id that is no UUID, an environment that does not exist.
		try := func(what string, c codes.Code, key string, l ...*plansv1.SupportLink) {
			t.Helper()
			_, err := e.support.OpenMySupportTicket(ctx, &plansv1.OpenMySupportTicketRequest{UserId: ada.id, AccountId: ada.account, AppId: appID, Subject: "Refused " + what, Category: "question", Body: "x", Links: l})
			wantCode(t, what, err, c, key)
		}
		try("another account's app", codes.NotFound, "support.app.not_found", link("customer", "cus_1", strangerApp.GetApp().GetId(), "live"))
		try("an app that is no id", codes.NotFound, "support.app.not_found", link("customer", "cus_1", "shop", "live"))
		try("six links", codes.InvalidArgument, "plans.request.invalid", link("customer", "a", appID, "live"), link("customer", "b", appID, "live"), link("customer", "c", appID, "live"), link("customer", "d", appID, "live"),
			link("customer", "e", appID, "live"), link("customer", "f", appID, "live"))
		try("a payment", codes.InvalidArgument, "plans.request.invalid", link("payment", inv, appID, "live"))
		try("an invoice that is no id", codes.InvalidArgument, "plans.request.invalid", link("invoice", "INV-1", appID, "live"))
		try("an environment that does not exist", codes.InvalidArgument, "plans.request.invalid", link("customer", "cus_1", appID, "staging"))
		// Nothing was left behind by the refusals: no half-made ticket.
		var made int
		if err := e.system.QueryRow(ctx, `SELECT count(*) FROM support_tickets WHERE subject LIKE 'Refused %'`).Scan(&made); err != nil || made != 0 {
			t.Errorf("%d tickets were left by refused requests (%v)", made, err)
		}
	})
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
