package domain

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

func TestSupportFirstResponseFollowsPriority(t *testing.T) {
	want := map[string]time.Duration{"urgent": time.Hour, "high": 4 * time.Hour, "normal": 24 * time.Hour, "low": 72 * time.Hour}
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
	open := SupportTicketState{Status: "open", Priority: "normal", CreatedAt: t0, FirstResponseDue: t0.Add(24 * time.Hour)}

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
	open := SupportTicketState{Status: "open", Priority: "normal", CreatedAt: t0, FirstResponseDue: t0.Add(24 * time.Hour)}
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
