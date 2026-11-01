package domain

import (
	"testing"
	"time"

	_ "github.com/0furkancolak/payrudder/pkg/tzdata" // zones for the business-hours test, as plans-svc itself carries them
)

var t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func TestSupportFirstResponseFollowsPriority(t *testing.T) {
	want := map[string]time.Duration{"urgent": time.Hour, "high": 4 * time.Hour, "normal": 9 * time.Hour, "low": 27 * time.Hour}
	for p, d := range want {
		if got := SupportFirstResponse(p); got != d {
			t.Errorf("%s: %v, want %v", p, got, d)
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
	open := SupportTicketState{Status: "open", Priority: "normal", CreatedAt: t0, FirstResponseDue: SupportFirstResponseDue(t0, "normal", "UTC"), Zone: "UTC"}

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
	open := SupportTicketState{Status: "open", Priority: "normal", CreatedAt: t0, FirstResponseDue: SupportFirstResponseDue(t0, "normal", "UTC"), Zone: "UTC"}
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
		if got := SupportFirstResponseDue(c.from, c.priority, "Europe/Berlin"); !got.Equal(c.want) {
			t.Errorf("%s: due %v, want %v", name, got.In(berlin), c.want)
		}
	}
	// An unknown zone counts in UTC instead of failing the ticket.
	if got := SupportFirstResponseDue(t0, "urgent", "Mars/Olympus"); !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("unknown zone: %v", got)
	}
}
