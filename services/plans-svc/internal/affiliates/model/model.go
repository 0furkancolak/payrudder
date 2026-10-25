// Package model holds the rules of the affiliate program as pure functions: what a commission is, how long a customer earns one, when a customer is the
// affiliate in disguise, and how much of a commission a refund takes back. No database here, so the rules are tested by table.
package model

import (
	"math/big"
	"regexp"
	"strings"
	"time"
)

// Program statuses.
const (
	ProgramDraft  = "draft"
	ProgramActive = "active"
	ProgramPaused = "paused"
)

// Affiliate statuses.
const (
	AffiliatePending   = "pending"
	AffiliateActive    = "active"
	AffiliateSuspended = "suspended"
)

// Commission kinds and terms of a program.
const (
	KindPercent = "percent_bps"
	KindFixed   = "fixed"

	TermFirstInvoices = "first_invoices"
	TermMonths        = "months"
	TermLifetime      = "lifetime"
)

// Sources of an attribution.
const (
	SourceLink = "link"
	SourceCode = "code"
	SourceAPI  = "api"
)

// Reasons an attribution is refused (the answer to AttributeCustomer, and the rows of affiliate_attribution_rejections for the last three).
const (
	ReasonAlreadyAttributed = "already_attributed"
	ReasonUnknownCode       = "unknown_code"
	ReasonProgramInactive   = "program_inactive"
	ReasonAffiliateInactive = "affiliate_inactive"
	ReasonExpired           = "expired"
	ReasonSameEmail         = "same_email"
	ReasonSameDomain        = "same_domain"
	ReasonSamePaymentMethod = "same_payment_method"
)

const (
	defaultCodeLength       = 8
	codeAlphabet            = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	basisPoints       int64 = 10000
)

// CodeRe is what an affiliate code looks like: it travels in a URL and a cookie.
var CodeRe = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

// ValidCode reports whether code can be an affiliate's code.
func ValidCode(code string) bool { return CodeRe.MatchString(code) }

// CodeAlphabet and DefaultCodeLength are for the generator of random codes: no 0 O 1 I L, which are mistaken for one another when read aloud.
func CodeAlphabet() string   { return codeAlphabet }
func DefaultCodeLength() int { return defaultCodeLength }

// Program is the part of a program the rules need.
type Program struct {
	Active                bool
	Kind                  string
	Value                 int64
	Currency              string
	Term                  string
	TermValue             int32
	AttributionWindowDays int32
	HoldDays              int32
}

// CommissionAmount is what an affiliate earns on an invoice that was paid net (pre-tax) in currency, and whether the program pays on it at all. A percentage
// follows the invoice's currency; a fixed amount is in the program's currency only, so an invoice in another currency earns nothing (no conversion exists).
// A percentage rounds half up.
func CommissionAmount(p Program, currency string, netMinor int64) (int64, bool) {
	if netMinor <= 0 {
		return 0, false
	}
	switch p.Kind {
	case KindPercent:
		q, r := netMinor/basisPoints, netMinor%basisPoints
		amount := q*p.Value + (r*p.Value+basisPoints/2)/basisPoints
		return amount, amount > 0
	case KindFixed:
		if !strings.EqualFold(p.Currency, currency) {
			return 0, false
		}
		return p.Value, p.Value > 0
	}
	return 0, false
}

// TermAllows reports whether a customer still earns commission: prior is how many commissions the customer has earned before this invoice (reversed ones count),
// firstEarned when the first of them was earned (nil when there is none), now when this invoice was paid.
func TermAllows(p Program, prior int64, firstEarned *time.Time, now time.Time) bool {
	switch p.Term {
	case TermFirstInvoices:
		return prior < int64(p.TermValue)
	case TermMonths:
		return firstEarned == nil || now.Before(firstEarned.AddDate(0, int(p.TermValue), 0))
	}
	return true
}

// ApproveAfter is when a commission earned at earned can be approved: the program's hold, the refund window.
func ApproveAfter(earned time.Time, holdDays int32) time.Time {
	return earned.AddDate(0, 0, int(holdDays))
}

// WithinWindow reports whether a touch at touched still counts at now for a program with this attribution window. A touch in the future is not one.
func WithinWindow(touched, now time.Time, windowDays int32) bool {
	if touched.After(now.Add(time.Minute)) {
		return false
	}
	return !now.After(touched.AddDate(0, 0, int(windowDays)))
}

// ReversalTarget is how much of a commission of amount a refund takes back once credited of the invoice's total has been credited (a void credits all of it):
// the commission in proportion, rounded half up, never more than the commission. The reversal to record is the target minus what was reversed already.
func ReversalTarget(amount, credited, total int64) int64 {
	if amount <= 0 || total <= 0 || credited <= 0 {
		return 0
	}
	if credited >= total {
		return amount
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(credited))
	n.Add(n, big.NewInt(total/2))
	n.Quo(n, big.NewInt(total))
	return min(n.Int64(), amount)
}

// freeDomains are mailbox providers many unrelated people share: the same domain there says nothing, so it is not a sign of self-referral.
var freeDomains = map[string]struct{}{
	"gmail.com": {}, "googlemail.com": {}, "outlook.com": {}, "hotmail.com": {}, "live.com": {}, "msn.com": {}, "yahoo.com": {}, "ymail.com": {}, "icloud.com": {},
	"me.com": {}, "mac.com": {}, "aol.com": {}, "proton.me": {}, "protonmail.com": {}, "pm.me": {}, "gmx.com": {}, "gmx.de": {}, "gmx.net": {}, "web.de": {},
	"yandex.com": {}, "yandex.ru": {}, "mail.ru": {}, "hotmail.co.uk": {}, "yahoo.co.uk": {}, "outlook.com.tr": {}, "hotmail.com.tr": {}, "yahoo.com.tr": {},
	"hotmail.fr": {}, "yahoo.fr": {}, "orange.fr": {}, "free.fr": {}, "t-online.de": {}, "libero.it": {}, "qq.com": {}, "163.com": {}, "zoho.com": {}, "fastmail.com": {},
	"tutanota.com": {}, "tuta.io": {}, "hey.com": {},
}

// NormalizeEmail lower-cases an address and drops a "+tag" from its local part, so that me+shop@example.com and Me@example.com are the same mailbox.
func NormalizeEmail(email string) string {
	e := strings.ToLower(strings.TrimSpace(email))
	local, domain, ok := strings.Cut(e, "@")
	if !ok {
		return e
	}
	if i := strings.IndexByte(local, '+'); i > 0 {
		local = local[:i]
	}
	return local + "@" + domain
}

// Domain is the part of an address after the @, lower case; "" when there is none.
func Domain(email string) string {
	_, d, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok {
		return ""
	}
	return d
}

// IsFreeDomain reports whether many unrelated people have addresses at the domain.
func IsFreeDomain(domain string) bool {
	_, ok := freeDomains[strings.ToLower(domain)]
	return ok
}

// SelfReferral says why a customer looks like the affiliate who brought them, or "" when there is no sign of it: the same mailbox, the same company domain (a
// shared mailbox provider is not one), or a payment method the affiliate's own customer record uses.
func SelfReferral(affiliateEmail, customerEmail string, affiliatePaymentMethods []string, customerPaymentMethod string) string {
	if NormalizeEmail(affiliateEmail) == NormalizeEmail(customerEmail) {
		return ReasonSameEmail
	}
	if d := Domain(affiliateEmail); d != "" && d == Domain(customerEmail) && !IsFreeDomain(d) {
		return ReasonSameDomain
	}
	if customerPaymentMethod != "" {
		for _, pm := range affiliatePaymentMethods {
			if pm == customerPaymentMethod {
				return ReasonSamePaymentMethod
			}
		}
	}
	return ""
}

// MaskEmail hides most of an address for a partner who sees who they brought: j***@example.com.
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(email), "@")
	if !ok || local == "" {
		return "***"
	}
	r := []rune(local)
	if len(r) == 1 {
		return "*@" + domain
	}
	return string(r[0]) + "***@" + domain
}
