package domain

import (
	"strings"
	"time"
	"unicode/utf8"
)

// The help desk's vocabulary (support_tickets in the migration).
var (
	SupportCategories = []string{"billing", "integration", "bug", "question", "other"}
	SupportPriorities = []string{"low", "normal", "high", "urgent"}
	// SupportStatuses: open waits for the operator, pending waits for the person.
	SupportStatuses = []string{"open", "pending", "solved", "closed"}
)

// SupportMaxBody bounds a message.
const SupportMaxBody = 10000

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// SupportFirstResponse is how long the operator has to answer a new ticket first, by priority.
func SupportFirstResponse(priority string) time.Duration {
	switch priority {
	case "urgent":
		return time.Hour
	case "high":
		return 4 * time.Hour
	case "low":
		return 72 * time.Hour
	default:
		return 24 * time.Hour
	}
}

// ValidateSupportTicket checks what a person writes when they open a ticket.
func ValidateSupportTicket(subject, category, priority, body string) error {
	var v Violations
	if n := utf8.RuneCountInString(strings.TrimSpace(subject)); n < 3 || n > 200 {
		v.Add("subject", "between 3 and 200 characters")
	}
	if !inList(SupportCategories, category) {
		v.Add("category", "one of "+strings.Join(SupportCategories, ", "))
	}
	if !inList(SupportPriorities, priority) {
		v.Add("priority", "one of "+strings.Join(SupportPriorities, ", "))
	}
	validateSupportBody(&v, body)
	return v.Err()
}

// ValidateSupportReply checks the text of an answer.
func ValidateSupportReply(body string) error {
	var v Violations
	validateSupportBody(&v, body)
	return v.Err()
}

func validateSupportBody(v *Violations, body string) {
	if n := utf8.RuneCountInString(strings.TrimSpace(body)); n < 1 || n > SupportMaxBody {
		v.Add("body", "between 1 and 10000 characters")
	}
}

// SupportFilter checks the values a list may be narrowed by.
func ValidateSupportFilter(status, priority string) error {
	var v Violations
	if status != "" && status != "active" && !inList(SupportStatuses, status) {
		v.Add("status", "one of active, "+strings.Join(SupportStatuses, ", "))
	}
	if priority != "" && !inList(SupportPriorities, priority) {
		v.Add("priority", "one of "+strings.Join(SupportPriorities, ", "))
	}
	return v.Err()
}

// SupportTicketState is what a ticket's life depends on.
type SupportTicketState struct {
	Status, Priority, Assignee string
	FirstResponseDue           time.Time
	FirstRespondedAt           *time.Time
	SolvedAt                   *time.Time
	CreatedAt                  time.Time
}

// SupportCustomerReply is the state after the person writes: a ticket waiting for them, or solved, goes back to the operator. A closed ticket takes no
// reply (the person opens a new one). It reports false when the reply is not allowed.
func SupportCustomerReply(s SupportTicketState) (SupportTicketState, bool) {
	if s.Status == "closed" {
		return s, false
	}
	s.Status, s.SolvedAt = "open", nil
	return s, true
}

// SupportOperatorReply is the state after the operator answers in public: the ticket waits for the person (unless it is solved or closed, which a reply does
// not undo), and the first such answer stops the clock.
func SupportOperatorReply(s SupportTicketState, now time.Time) SupportTicketState {
	if s.Status == "open" {
		s.Status = "pending"
	}
	if s.FirstRespondedAt == nil {
		s.FirstRespondedAt = &now
	}
	return s
}

// SupportSetStatus is a change of status. The person may only mark a ticket solved or reopen one that is not closed; the operator may set any status.
func SupportSetStatus(s SupportTicketState, to string, operator bool, now time.Time) (SupportTicketState, error) {
	var v Violations
	switch {
	case !inList(SupportStatuses, to):
		v.Add("status", "one of "+strings.Join(SupportStatuses, ", "))
	case !operator && to != "solved" && to != "open":
		v.Add("status", "solved or open")
	case !operator && s.Status == "closed":
		v.Add("status", "a closed ticket cannot be changed; open a new one")
	}
	if err := v.Err(); err != nil {
		return s, err
	}
	s.Status = to
	switch to {
	case "solved", "closed":
		if s.SolvedAt == nil {
			s.SolvedAt = &now
		}
	default:
		s.SolvedAt = nil
	}
	return s, nil
}

// SupportSetPriority changes the priority and moves the first-response deadline with it, counted from when the ticket was opened, unless the operator has
// already answered (the deadline then no longer matters).
func SupportSetPriority(s SupportTicketState, to string) (SupportTicketState, error) {
	if !inList(SupportPriorities, to) {
		var v Violations
		v.Add("priority", "one of "+strings.Join(SupportPriorities, ", "))
		return s, v.Err()
	}
	s.Priority = to
	if s.FirstRespondedAt == nil {
		s.FirstResponseDue = s.CreatedAt.Add(SupportFirstResponse(to))
	}
	return s, nil
}

// ValidateSupportAssignee bounds the assignee's name.
func ValidateSupportAssignee(a string) error {
	if utf8.RuneCountInString(a) > 100 {
		var v Violations
		v.Add("assignee", "at most 100 characters")
		return v.Err()
	}
	return nil
}
