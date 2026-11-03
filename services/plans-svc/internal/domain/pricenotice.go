package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// NoticeSendAt is when the notice mail of a subscription whose new price starts at start goes out (plan 15). sendDays is the price change's notice_send_days_before
// (0: at once), applicable the notice that holds for the customer (the run's notice or the customer's country floor, whichever is longer). The mail goes out
// max(sendDays, applicable) days before start, so a customer is never told later than their notice; when that moment is now or past it goes now (held false).
func NoticeSendAt(now, start time.Time, sendDays, applicable int) (at time.Time, held bool) {
	if sendDays <= 0 {
		return now, false
	}
	at = start.Add(-time.Duration(max(sendDays, applicable)) * 24 * time.Hour)
	if !at.After(now) {
		return now, false
	}
	return at, true
}

// ValidateNoticeSendDays checks notice_send_days_before of a request (0: unset): at least the notice of the run and at most a year.
func ValidateNoticeSendDays(sendDays, notice int) error {
	if sendDays != 0 && (sendDays < notice || sendDays > MaxNoticeDays) {
		return Invalid("notice_send_days_before", "0, or between notice_days and 365 days")
	}
	return nil
}

// The notice a customer is given of a new price (plan 15). All three numbers are defaults (not agreed): the floor of 14 days is a guess, 30 days is the notice a run uses
// when it names none, and the operator may raise both. The per-country rows are UNVERIFIED and NOT legal advice: no one with the right to say what a country requires
// has read them.
const (
	MinNoticeDays     = 14
	DefaultNoticeDays = 30
	MaxNoticeDays     = 365
)

// NoticeAnyCountry is the row that holds for a country with no row of its own.
const NoticeAnyCountry = "*"

// NoticeRules are the operator's notice rules of price changes: the notice a run uses when its request names none, and the least notice per country.
type NoticeRules struct {
	Default int
	// Floors maps an upper-case ISO 3166-1 alpha-2 code, or NoticeAnyCountry, to the least number of days.
	Floors map[string]int
}

// DefaultNoticeRules is what holds while nothing is configured.
func DefaultNoticeRules() NoticeRules {
	return NoticeRules{Default: DefaultNoticeDays, Floors: map[string]int{NoticeAnyCountry: MinNoticeDays}}
}

// Floor is the least notice for a customer in a country (empty: unknown): the country's row, else the row for any country, else the code's floor.
func (r NoticeRules) Floor(country string) int {
	if d, ok := r.Floors[strings.ToUpper(strings.TrimSpace(country))]; ok && country != "" {
		return max(d, MinNoticeDays)
	}
	return max(r.Floors[NoticeAnyCountry], MinNoticeDays)
}

var noticeCountry = regexp.MustCompile(`^[A-Z]{2}$`)

// ValidateNoticeRules checks what the operator saved: the default and every row between the code's floor and a year, each country once and written as two capital
// letters (or '*'), a note of at most 200 characters.
func ValidateNoticeRules(def int, rules map[string]int, notes map[string]string) error {
	var v Violations
	if def < MinNoticeDays || def > MaxNoticeDays {
		v.Add("default_notice_days", "between 14 and 365 days")
	}
	for c, d := range rules {
		if c != NoticeAnyCountry && !noticeCountry.MatchString(c) {
			v.Add("rules", "a country is two capital letters (ISO 3166-1 alpha-2) or *")
		}
		if d < MinNoticeDays || d > MaxNoticeDays {
			v.Add("rules", "the least notice is between 14 and 365 days")
		}
		if utf8.RuneCountInString(notes[c]) > 200 {
			v.Add("rules", "a note is at most 200 characters")
		}
	}
	return v.Err()
}
