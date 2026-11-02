package domain

import (
	"regexp"
	"sort"
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

// SupportFirstResponse is how much business time the operator has to answer a new ticket first, by the plan of the ticket's app and the ticket's priority (plan 12,
// a default table, not agreed; the owner may overturn it). The free row is the one table the help desk had before plans counted, and the plan every new app starts on:
// urgent one hour, high four, normal one business day (nine hours), low three business days. A standard app has less time to wait and an enterprise one less still. An
// unknown plan counts as free.
func SupportFirstResponse(tier, priority string) time.Duration {
	table, ok := supportFirstResponse[tier]
	if !ok {
		table = supportFirstResponse["free"]
	}
	if d, ok := table[priority]; ok {
		return d
	}
	return table["normal"]
}

var supportFirstResponse = map[string]map[string]time.Duration{
	"free":       {"urgent": time.Hour, "high": 4 * time.Hour, "normal": 9 * time.Hour, "low": 27 * time.Hour},
	"standard":   {"urgent": time.Hour, "high": 2 * time.Hour, "normal": 6 * time.Hour, "low": 18 * time.Hour},
	"enterprise": {"urgent": 30 * time.Minute, "high": time.Hour, "normal": 4 * time.Hour, "low": 9 * time.Hour},
}

// SupportFirstResponseDue is the deadline of a ticket opened at from: the plan's and priority's business time after it, counted in the ticket's zone (UTC when the zone
// is empty or not known). A ticket opened out of hours starts the clock when the next business day opens. The clock does not run on a weekend or on a day in off (the
// app's days off, "2006-01-02" in the ticket's zone).
func SupportFirstResponseDue(from time.Time, tier, priority, zone string, off map[string]bool) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	left, t := SupportFirstResponse(tier, priority), from.In(loc)
	for {
		y, m, d := t.Date()
		open, end := time.Date(y, m, d, supportOpenHour, 0, 0, 0, loc), time.Date(y, m, d, supportCloseHour, 0, 0, 0, loc)
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday && !off[t.Format("2006-01-02")] && t.Before(end) {
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
	// Zone is the time zone the first-answer deadline counts business hours in, Tier the plan of the ticket's app it was counted for, and OffDays the app's days off
	// (loaded by the caller that changes the priority).
	Zone, Tier string
	OffDays    map[string]bool
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
		s.FirstResponseDue = SupportFirstResponseDue(s.CreatedAt, s.Tier, to, s.Zone, s.OffDays)
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

// ValidateSupportRating checks what a person says of a solved ticket: 1 to 5 and a comment of at most 1000 characters (optional).
func ValidateSupportRating(rating int, comment string) error {
	var v Violations
	if rating < 1 || rating > 5 {
		v.Add("rating", "between 1 and 5")
	}
	if utf8.RuneCountInString(comment) > 1000 {
		v.Add("comment", "at most 1000 characters")
	}
	return v.Err()
}

// SupportMaxLinks is the most objects a ticket links to; SupportMaxHolidays the most days off an app lists (plan 12, defaults not agreed).
const (
	SupportMaxLinks    = 5
	SupportMaxHolidays = 200
)

// SupportLinkKinds are the objects a ticket may link to.
var SupportLinkKinds = []string{"customer", "invoice", "subscription"}

var uuidText = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NormalizeSupportLink checks one link and returns it trimmed: a kind from the list, a reference of 1 to 200 characters without control characters (a customer's
// external id, or the id of an invoice or a subscription, which is a UUID), and an environment of sandbox or live.
func NormalizeSupportLink(kind, ref, environment string) (string, string, string, error) {
	kind, ref, environment = strings.TrimSpace(kind), strings.TrimSpace(ref), strings.TrimSpace(environment)
	var v Violations
	if !inList(SupportLinkKinds, kind) {
		v.Add("links", "kind is one of "+strings.Join(SupportLinkKinds, ", "))
	}
	if n := utf8.RuneCountInString(ref); n < 1 || n > 200 || strings.ContainsFunc(ref, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		v.Add("links", "ref is 1 to 200 characters without control characters")
	} else if kind != "customer" && !uuidText.MatchString(ref) {
		v.Add("links", "an invoice or a subscription is named by its id")
	}
	if environment != "sandbox" && environment != "live" {
		v.Add("links", "environment is sandbox or live")
	}
	return kind, refCase(kind, ref), environment, v.Err()
}

// refCase lower-cases the id of an invoice or a subscription (the same UUID is written either way) and leaves a customer's external id as it is.
func refCase(kind, ref string) string {
	if kind == "customer" {
		return ref
	}
	return strings.ToLower(ref)
}

// NormalizeSupportHolidays checks an app's days off: at most 200, each day once, written yyyy-mm-dd, within three years either side of today, a name of at most
// 100 characters. It returns them in date order.
func NormalizeSupportHolidays(days []SupportHoliday, today time.Time) ([]SupportHoliday, error) {
	var v Violations
	seen := map[string]bool{}
	out := make([]SupportHoliday, 0, len(days))
	if len(days) > SupportMaxHolidays {
		v.Add("holidays", "at most 200 days")
	}
	for _, h := range days {
		d, err := time.Parse("2006-01-02", strings.TrimSpace(h.Day))
		switch {
		case err != nil:
			v.Add("holidays", "a day is written yyyy-mm-dd")
			continue
		case d.Before(today.AddDate(-3, 0, 0)) || d.After(today.AddDate(3, 0, 0)):
			v.Add("holidays", "a day is within three years of today")
		case seen[d.Format("2006-01-02")]:
			v.Add("holidays", "each day once")
		case utf8.RuneCountInString(strings.TrimSpace(h.Name)) > 100:
			v.Add("holidays", "a name is at most 100 characters")
		}
		seen[d.Format("2006-01-02")] = true
		out = append(out, SupportHoliday{Day: d.Format("2006-01-02"), Name: strings.TrimSpace(h.Name)})
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}

// SupportHoliday is a day off of an app.
type SupportHoliday struct{ Day, Name string }

// SupportMergeCheck says whether ticket a may be merged into b (plan 12): two different tickets of one person in one account, neither merged already, and the target
// not closed. It returns a reason for a refusal.
func SupportMergeCheck(a, b SupportMergeSide) string {
	switch {
	case a.ID == b.ID:
		return "a ticket cannot be merged into itself"
	case a.AccountID != b.AccountID || a.OpenedBy != b.OpenedBy:
		return "only two tickets of one person in one account can be merged"
	case a.Merged || b.Merged:
		return "a ticket that was merged already cannot be merged again"
	case b.Status == "closed":
		return "the ticket to merge into is closed"
	}
	return ""
}

// SupportMergeSide is what a merge needs to know of a ticket.
type SupportMergeSide struct {
	ID, AccountID, OpenedBy, Status string
	Merged                          bool
}
