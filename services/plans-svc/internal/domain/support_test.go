package domain

import (
	"reflect"
	"strings"
	"testing"
	"time"

	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // zones for the business-hours test, as plans-svc itself carries them
)

var t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func TestSupportFirstResponseFollowsThePlanAndThePriority(t *testing.T) {
	want := map[string]map[string]time.Duration{
		"free":       {"urgent": time.Hour, "high": 4 * time.Hour, "normal": 9 * time.Hour, "low": 27 * time.Hour}, // the one table the desk had before plans counted
		"standard":   {"urgent": time.Hour, "high": 2 * time.Hour, "normal": 6 * time.Hour, "low": 18 * time.Hour},
		"enterprise": {"urgent": 30 * time.Minute, "high": time.Hour, "normal": 4 * time.Hour, "low": 9 * time.Hour},
		"unheard-of": {"urgent": time.Hour, "high": 4 * time.Hour, "normal": 9 * time.Hour, "low": 27 * time.Hour}, // an unknown plan counts as free
	}
	for tier, byPriority := range want {
		for p, d := range byPriority {
			if got := SupportFirstResponse(tier, p); got != d {
				t.Errorf("%s %s: %v, want %v", tier, p, got, d)
			}
		}
	}
	// A better plan never waits longer than a worse one for the same priority.
	for _, p := range SupportPriorities {
		if SupportFirstResponse("enterprise", p) > SupportFirstResponse("standard", p) || SupportFirstResponse("standard", p) > SupportFirstResponse("free", p) {
			t.Errorf("%s: the plans are not in order", p)
		}
	}
}

func TestValidateSupportTicket(t *testing.T) {
	if err := ValidateSupportTicket("Invoice is wrong", "billing", "normal", "Please look"); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][4]string{
		"short subject": {"hi", "billing", "normal", "x"},
		"bad category":  {"Invoice is wrong", "sales", "normal", "x"},
		"bad priority":  {"Invoice is wrong", "billing", "asap", "x"},
		"blank body":    {"Invoice is wrong", "billing", "normal", "   "},
	} {
		if ValidateSupportTicket(c[0], c[1], c[2], c[3]) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSupportLifecycle(t *testing.T) {
	open := SupportTicketState{Status: "open", Priority: "normal", Tier: "free", CreatedAt: t0, FirstResponseDue: SupportFirstResponseDue(t0, "free", "normal", "UTC", nil), Zone: "UTC"}

	// The operator's first public answer stops the clock and hands the ticket to the person.
	answered := SupportOperatorReply(open, t0.Add(time.Hour))
	if answered.Status != "pending" || answered.FirstRespondedAt == nil {
		t.Fatalf("after the answer: %+v", answered)
	}
	// A second answer does not move the first-response time.
	again := SupportOperatorReply(answered, t0.Add(5*time.Hour))
	if !again.FirstRespondedAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("first response moved to %v", again.FirstRespondedAt)
	}

	// The person's reply reopens a pending or solved ticket.
	solved, err := SupportSetStatus(answered, "solved", false, t0.Add(2*time.Hour))
	if err != nil || solved.SolvedAt == nil {
		t.Fatalf("solve: %v %+v", err, solved)
	}
	reopened, ok := SupportCustomerReply(solved)
	if !ok || reopened.Status != "open" || reopened.SolvedAt != nil {
		t.Errorf("reply on solved: %v %+v", ok, reopened)
	}

	// A closed ticket takes no reply and the person cannot change it; the operator can.
	closed, _ := SupportSetStatus(open, "closed", true, t0)
	if _, ok := SupportCustomerReply(closed); ok {
		t.Error("reply on a closed ticket accepted")
	}
	if _, err := SupportSetStatus(closed, "open", false, t0); err == nil {
		t.Error("the person reopened a closed ticket")
	}
	if _, err := SupportSetStatus(open, "pending", false, t0); err == nil {
		t.Error("the person set pending")
	}
	if back, err := SupportSetStatus(closed, "open", true, t0); err != nil || back.SolvedAt != nil {
		t.Errorf("operator reopen: %v %+v", err, back)
	}
}

func TestSupportPriorityMovesTheDeadlineUntilAnswered(t *testing.T) {
	open := SupportTicketState{Status: "open", Priority: "normal", Tier: "free", CreatedAt: t0, FirstResponseDue: SupportFirstResponseDue(t0, "free", "normal", "UTC", nil), Zone: "UTC"}
	urgent, err := SupportSetPriority(open, "urgent")
	if err != nil || !urgent.FirstResponseDue.Equal(t0.Add(time.Hour)) {
		t.Fatalf("urgent: %v %v", err, urgent.FirstResponseDue)
	}
	answered := SupportOperatorReply(open, t0.Add(time.Hour))
	low, _ := SupportSetPriority(answered, "low")
	if !low.FirstResponseDue.Equal(open.FirstResponseDue) {
		t.Errorf("an answered ticket's deadline moved to %v", low.FirstResponseDue)
	}
	if _, err := SupportSetPriority(open, "asap"); err == nil {
		t.Error("unknown priority accepted")
	}
}

// The clock counts Monday to Friday, 09:00 to 18:00 in the ticket's zone; t0 is a Wednesday.
func TestSupportDeadlineCountsBusinessHoursInTheZone(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, berlin) }
	for name, c := range map[string]struct {
		from     time.Time
		priority string
		want     time.Time
	}{
		"inside the day":                         {at(2026, 10, 28, 10, 30), "high", at(2026, 10, 28, 14, 30)},
		"the day ends and the next opens":        {at(2026, 10, 28, 16, 0), "high", at(2026, 10, 29, 11, 0)},
		"before opening starts at nine":          {at(2026, 10, 28, 6, 15), "urgent", at(2026, 10, 28, 10, 0)},
		"after closing waits for morning":        {at(2026, 10, 28, 19, 0), "urgent", at(2026, 10, 29, 10, 0)},
		"friday evening waits for monday":        {at(2026, 10, 30, 17, 30), "high", at(2026, 11, 2, 12, 30)},
		"a saturday ticket":                      {at(2026, 10, 31, 12, 0), "urgent", at(2026, 11, 2, 10, 0)},
		"a business day is nine hours":           {at(2026, 10, 28, 9, 0), "normal", at(2026, 10, 28, 18, 0)},
		"three business days run over a weekend": {at(2026, 10, 29, 9, 0), "low", at(2026, 11, 2, 18, 0)},
		"the clocks change at night":             {at(2026, 10, 23, 17, 0), "high", at(2026, 10, 26, 12, 0)},
	} {
		if got := SupportFirstResponseDue(c.from, "free", c.priority, "Europe/Berlin", nil); !got.Equal(c.want) {
			t.Errorf("%s: due %v, want %v", name, got.In(berlin), c.want)
		}
	}
	// An unknown zone counts in UTC instead of failing the ticket.
	if got := SupportFirstResponseDue(t0, "free", "urgent", "Mars/Olympus", nil); !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("unknown zone: %v", got)
	}
}

func TestNormalizeSupportTags(t *testing.T) {
	got, err := NormalizeSupportTags([]string{" Refund", "vat", "REFUND", "", "tax-2026"})
	if err != nil || !reflect.DeepEqual(got, []string{"refund", "vat", "tax-2026"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if got, err := NormalizeSupportTags(nil); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no tags = %v, %v (an empty list, not nil: the column is not null)", got, err)
	}
	for name, in := range map[string][]string{"a space": {"two words"}, "an upper-case symbol": {"a_b"}, "starts with a hyphen": {"-a"}, "too long": {strings.Repeat("x", 31)}, "six": {"a", "b", "c", "d", "e", "f"}} {
		if _, err := NormalizeSupportTags(in); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// The same word twice counts once.
	if _, err := NormalizeSupportTags([]string{"a", "b", "c", "d", "e", "E"}); err != nil {
		t.Errorf("five distinct tags: %v", err)
	}
}

func TestValidateSupportAssignee(t *testing.T) {
	ops := []string{"ops@example.com", "kim@example.com"}
	for name, c := range map[string]struct {
		to, current string
		ok          bool
	}{"nobody": {"", "ops@example.com", true}, "an operator": {"KIM@example.com", "", true}, "what it was": {"Sam the intern", "Sam the intern", true},
		"a stranger": {"stranger@example.com", "", false}, "a name that is not what it was": {"Sam", "Sam the intern", false}} {
		if err := ValidateSupportAssignee(c.to, c.current, ops); (err == nil) != c.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A day off stops the clock like a weekend; the plan changes how long the clock runs.
func TestSupportDeadlineSkipsDaysOffAndFollowsThePlan(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	at := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, berlin) }
	off := map[string]bool{"2026-10-29": true, "2026-10-30": true} // Thursday and Friday
	for name, c := range map[string]struct {
		from           time.Time
		tier, priority string
		off            map[string]bool
		want           time.Time
	}{
		"a high ticket on Wednesday afternoon waits out two days off and the weekend": {at(28, 16), "free", "high", off, time.Date(2026, 11, 2, 11, 0, 0, 0, berlin)},
		"the same ticket with no days off is answered on Thursday":                    {at(28, 16), "free", "high", nil, at(29, 11)},
		"a ticket opened on a day off starts at the next opening":                     {at(29, 12), "free", "urgent", off, time.Date(2026, 11, 2, 10, 0, 0, 0, berlin)},
		"a free app has four hours for a high ticket":                                 {at(28, 10), "free", "high", nil, at(28, 14)},
		"a standard app has half of that":                                             {at(28, 10), "standard", "high", nil, at(28, 12)},
		"an enterprise app has a quarter":                                             {at(28, 10), "enterprise", "high", nil, at(28, 11)},
	} {
		got := SupportFirstResponseDue(c.from, c.tier, c.priority, "Europe/Berlin", c.off)
		if !got.Equal(c.want) {
			t.Errorf("%s: due %v, want %v", name, got.In(berlin), c.want)
		}
	}
	// A change of priority counts again with the days off it is given.
	open := SupportTicketState{Status: "open", Priority: "normal", Tier: "free", CreatedAt: at(28, 10), Zone: "Europe/Berlin", OffDays: off}
	if high, err := SupportSetPriority(open, "high"); err != nil || !high.FirstResponseDue.Equal(at(28, 14)) {
		t.Errorf("a priority change: %v, %v", high.FirstResponseDue, err)
	}
}

func TestValidateSupportRating(t *testing.T) {
	for _, r := range []int{1, 3, 5} {
		if err := ValidateSupportRating(r, ""); err != nil {
			t.Errorf("%d refused: %v", r, err)
		}
	}
	for name, c := range map[string]struct {
		r       int
		comment string
	}{"zero": {0, ""}, "six": {6, ""}, "negative": {-1, ""}, "a comment of 1001 characters": {4, strings.Repeat("x", 1001)}} {
		if ValidateSupportRating(c.r, c.comment) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNormalizeSupportLink(t *testing.T) {
	id := "0192F0B4-6F6E-7C3A-9A3E-6A1F2B3C4D5E"
	if k, ref, env, err := NormalizeSupportLink(" invoice ", " "+id+" ", "live"); err != nil || k != "invoice" || ref != strings.ToLower(id) || env != "live" {
		t.Errorf("an invoice: %q %q %q %v", k, ref, env, err)
	}
	if _, ref, _, err := NormalizeSupportLink("customer", "Cus_ABC 1", "sandbox"); err != nil || ref != "Cus_ABC 1" {
		t.Errorf("a customer's external id is kept as it is: %q %v", ref, err)
	}
	for name, c := range map[string][3]string{
		"an unknown kind":                    {"payment", id, "live"},
		"an invoice that is not an id":       {"invoice", "INV-1", "live"},
		"a subscription that is not an id":   {"subscription", "sub_1", "live"},
		"an empty customer":                  {"customer", " ", "live"},
		"a control character":                {"customer", "a\nb", "live"},
		"a reference of 201 characters":      {"customer", strings.Repeat("x", 201), "live"},
		"an environment that does not exist": {"customer", "cus_1", "staging"},
	} {
		if _, _, _, err := NormalizeSupportLink(c[0], c[1], c[2]); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNormalizeSupportHolidays(t *testing.T) {
	today := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	got, err := NormalizeSupportHolidays([]SupportHoliday{{"2026-12-25", " Christmas "}, {"2026-11-11", ""}}, today)
	if err != nil || len(got) != 2 || got[0].Day != "2026-11-11" || got[1].Name != "Christmas" {
		t.Fatalf("sorted and trimmed: %v, %v", got, err)
	}
	many := make([]SupportHoliday, 0, 201)
	for i := 0; i < 201; i++ {
		many = append(many, SupportHoliday{Day: today.AddDate(0, 0, i).Format("2006-01-02")})
	}
	for name, in := range map[string][]SupportHoliday{
		"not a date": {{"25/12/2026", ""}}, "twice": {{"2026-12-25", ""}, {"2026-12-25", "again"}}, "far in the future": {{"2031-01-01", ""}}, "far in the past": {{"2020-01-01", ""}},
		"201 days": many, "a long name": {{"2026-12-25", strings.Repeat("x", 101)}},
	} {
		if _, err := NormalizeSupportHolidays(in, today); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if got, err := NormalizeSupportHolidays(nil, today); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no days = %v, %v", got, err)
	}
}

func TestSupportMergeCheck(t *testing.T) {
	a := SupportMergeSide{ID: "a", AccountID: "acc", OpenedBy: "ada", Status: "open"}
	b := SupportMergeSide{ID: "b", AccountID: "acc", OpenedBy: "ada", Status: "pending"}
	if why := SupportMergeCheck(a, b); why != "" {
		t.Fatalf("two tickets of one person: %q", why)
	}
	for name, c := range map[string][2]SupportMergeSide{
		"itself":          {a, a},
		"another person":  {a, SupportMergeSide{ID: "b", AccountID: "acc", OpenedBy: "bob", Status: "open"}},
		"another account": {a, SupportMergeSide{ID: "b", AccountID: "other", OpenedBy: "ada", Status: "open"}},
		"a merged source": {SupportMergeSide{ID: "a", AccountID: "acc", OpenedBy: "ada", Status: "closed", Merged: true}, b},
		"a merged target": {a, SupportMergeSide{ID: "b", AccountID: "acc", OpenedBy: "ada", Status: "closed", Merged: true}},
		"a closed target": {a, SupportMergeSide{ID: "b", AccountID: "acc", OpenedBy: "ada", Status: "closed"}},
	} {
		if SupportMergeCheck(c[0], c[1]) == "" {
			t.Errorf("%s was allowed", name)
		}
	}
	// A closed source is fine: a closed duplicate can still join the live ticket.
	if why := SupportMergeCheck(SupportMergeSide{ID: "a", AccountID: "acc", OpenedBy: "ada", Status: "closed"}, b); why != "" {
		t.Errorf("a closed source: %q", why)
	}
}
