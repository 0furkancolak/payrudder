package domain

import (
	"regexp"
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

// SupportMaxOpen is the most tickets an account may have waiting (open or pending) at once; the person who opens one more is told to wait for an answer or close
// one. A default (plan 12, not agreed).
const SupportMaxOpen = 20

// What may be attached to a message (plan 12, defaults not agreed). The types and the size are assets-svc's rule for the purpose support_attachment; the count and the
// account's total are this module's.
const (
	SupportMaxAttachments            = 10
	SupportMaxAttachmentBytes        = 5 << 20
	SupportMaxAccountAttachmentBytes = 50 << 20
)

// SupportAttachmentTypes are the content types an attachment may have.
var SupportAttachmentTypes = []string{"image/png", "image/jpeg", "application/pdf", "text/plain"}

// The first-answer clock counts business hours only: Monday to Friday, 09:00 to 18:00 in the ticket's time zone (a default, plan 12, not agreed).
const (
	supportOpenHour  = 9
	supportCloseHour = 18
)

func inList(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// SupportFirstResponse is how much business time the operator has to answer a new ticket first, by priority: urgent one hour, high four, normal one business day
// (nine hours), low three business days.
func SupportFirstResponse(priority string) time.Duration {
	switch priority {
	case "urgent":
		return time.Hour
	case "high":
		return 4 * time.Hour
	case "low":
		return 27 * time.Hour
	default:
		return 9 * time.Hour
	}
}

// SupportFirstResponseDue is the deadline of a ticket opened at from: the priority's business time after it, counted in the ticket's zone (UTC when the zone is
// empty or not known). A ticket opened out of hours starts the clock when the next business day opens. Holidays are not known: shortcut, add a calendar when the
// operator publishes its days off.
func SupportFirstResponseDue(from time.Time, priority, zone string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	left, t := SupportFirstResponse(priority), from.In(loc)
	for {
		y, m, d := t.Date()
		open, end := time.Date(y, m, d, supportOpenHour, 0, 0, 0, loc), time.Date(y, m, d, supportCloseHour, 0, 0, 0, loc)
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday && t.Before(end) {
			if t.Before(open) {
				t = open
			}
			if end.Sub(t) >= left {
				return t.Add(left)
			}
			left -= end.Sub(t)
		}
		t = time.Date(y, m, d+1, supportOpenHour, 0, 0, 0, loc)
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
	// Tags are the operator's labels on the ticket.
	Tags             []string
	FirstResponseDue time.Time
	FirstRespondedAt *time.Time
	SolvedAt         *time.Time
	CreatedAt        time.Time
	// Zone is the time zone the first-answer deadline counts business hours in.
	Zone string
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
		s.FirstResponseDue = SupportFirstResponseDue(s.CreatedAt, to, s.Zone)
	}
	return s, nil
}

// ValidateSupportAssignee checks an assignee: nobody (empty), the ticket's current one (it keeps one that has left the list until it is changed), or one of the
// operators' addresses (the list is plans-svc's PAYRUDDER_OPERATOR_EMAILS, lower case).
func ValidateSupportAssignee(a, current string, operators []string) error {
	a = strings.TrimSpace(a)
	if a == "" || a == current || inList(operators, strings.ToLower(a)) {
		return nil
	}
	var v Violations
	v.Add("assignee", "one of the operators")
	return v.Err()
}

// SupportMaxTags is the most tags a ticket carries; a tag is lower case letters, digits and hyphens, at most 30 characters (plan 12, a default, not agreed).
const SupportMaxTags = 5

var supportTag = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,29}$`)

// NormalizeSupportTags trims, lower-cases and de-duplicates the tags in the order given, and refuses a list that is too long or holds a word that is not a tag.
func NormalizeSupportTags(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	var v Violations
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(t))
		switch {
		case t == "" || inList(out, t):
		case !supportTag.MatchString(t):
			v.Add("tags", "lower case letters, digits and hyphens, at most 30 characters")
		default:
			out = append(out, t)
		}
	}
	if len(out) > SupportMaxTags {
		v.Add("tags", "at most 5 tags")
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ValidateSupportCannedReply bounds a canned reply's title and text.
func ValidateSupportCannedReply(title, body string) error {
	var v Violations
	if n := utf8.RuneCountInString(strings.TrimSpace(title)); n < 1 || n > 100 {
		v.Add("title", "1 to 100 characters")
	}
	validateSupportBody(&v, body)
	return v.Err()
}
