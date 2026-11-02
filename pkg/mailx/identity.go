package mailx

import (
	"context"
	"errors"
	"fmt"
	"net"
	stdmail "net/mail"
	"regexp"
	"strings"
)

// Identity is what a sender's domain publishes in DNS for mail sent from it: SPF, DKIM and DMARC. It is the check Payrudder can make without the provider: whether
// these records exist and are not weak, which is what mailbox providers judge a sender by. It says nothing about whether the records name the provider that actually
// sends (an include for the provider is not looked for: Nafru's own names are UNVERIFIED) and nothing about the provider's own verification of the domain.
type Identity struct {
	Domain string `json:"domain"`
	SPF    Check  `json:"spf"`
	DKIM   Check  `json:"dkim"`
	DMARC  Check  `json:"dmarc"`
}

// Statuses of a Check.
const (
	CheckOK        = "ok"        // published and not weak
	CheckWeak      = "weak"      // published, but a receiver may treat the mail as unauthenticated (an SPF that passes everything, a DMARC policy of none)
	CheckMissing   = "missing"   // no such record
	CheckInvalid   = "invalid"   // more than one SPF record is an error for a receiver
	CheckUnchecked = "unchecked" // not looked up: DKIM needs the selector the provider publishes it under
	CheckError     = "error"     // the lookup failed (a timeout, a failing resolver): say nothing about the record
)

// Check is one record's verdict. Detail is a short word the console translates (hard_fail, soft_fail, neutral, pass_all, no_all, multiple, policy_none, quarantine,
// reject, inherited, no_selector, revoked); Record is the text found, clipped.
type Check struct {
	Status   string `json:"status"`
	Record   string `json:"record,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Selector string `json:"selector,omitempty"`
}

// TXTLookup reads the TXT records of a name; net.Resolver.LookupTXT is one (it follows CNAMEs, which is how a provider usually delegates a DKIM key).
type TXTLookup func(ctx context.Context, name string) ([]string, error)

var (
	domainPattern   = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)
	selectorPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
)

// ErrIdentityInput is a sender address or selector that cannot be looked up.
var ErrIdentityInput = errors.New("not a sender address or selector that can be looked up")

// CheckIdentity looks up the records of the domain of from ("Name <a@b.example>" or "a@b.example"). selector is the DKIM selector the provider publishes the key
// under (empty: DKIM is left unchecked, not guessed).
func CheckIdentity(ctx context.Context, lookup TXTLookup, from, selector string) (Identity, error) {
	addr, err := stdmail.ParseAddress(from)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrIdentityInput, err)
	}
	_, domain, _ := strings.Cut(addr.Address, "@")
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	selector = strings.ToLower(strings.TrimSpace(selector))
	if !domainPattern.MatchString(domain) || (selector != "" && !selectorPattern.MatchString(selector)) {
		return Identity{}, ErrIdentityInput
	}
	id := Identity{Domain: domain, SPF: checkSPF(ctx, lookup, domain), DMARC: checkDMARC(ctx, lookup, domain)}
	if selector == "" {
		id.DKIM = Check{Status: CheckUnchecked, Detail: "no_selector"}
	} else {
		id.DKIM = checkDKIM(ctx, lookup, domain, selector)
	}
	return id, nil
}

// txt returns the records of a name that start with prefix (case-insensitive), joined as one string each; found is false when the name has none at all.
func txt(ctx context.Context, lookup TXTLookup, name, prefix string) (records []string, err error) {
	all, err := lookup(ctx, name)
	if err != nil {
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return nil, nil
		}
		return nil, err
	}
	for _, r := range all {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r)), prefix) {
			records = append(records, strings.TrimSpace(r))
		}
	}
	return records, nil
}

func clip(s string) string {
	if len(s) > 300 {
		return strings.ToValidUTF8(s[:300], "") + "…"
	}
	return s
}

func checkSPF(ctx context.Context, lookup TXTLookup, domain string) Check {
	recs, err := txt(ctx, lookup, domain, "v=spf1")
	switch {
	case err != nil:
		return Check{Status: CheckError}
	case len(recs) == 0:
		return Check{Status: CheckMissing}
	case len(recs) > 1:
		return Check{Status: CheckInvalid, Detail: "multiple", Record: clip(recs[0])}
	}
	c := Check{Record: clip(recs[0]), Status: CheckWeak, Detail: "no_all"}
	for _, term := range strings.Fields(strings.ToLower(recs[0])) {
		switch term {
		case "-all":
			c.Status, c.Detail = CheckOK, "hard_fail"
		case "~all":
			c.Status, c.Detail = CheckOK, "soft_fail"
		case "?all":
			c.Detail = "neutral"
		case "+all", "all":
			c.Detail = "pass_all"
		}
	}
	return c
}

func checkDKIM(ctx context.Context, lookup TXTLookup, domain, selector string) Check {
	recs, err := txt(ctx, lookup, selector+"._domainkey."+domain, "v=dkim1")
	if err == nil && len(recs) == 0 {
		// A key record may omit v=; what makes it a key is p=.
		var all []string
		if all, err = lookup(ctx, selector+"._domainkey."+domain); err == nil {
			for _, r := range all {
				if strings.Contains(strings.ToLower(r), "p=") {
					recs = append(recs, strings.TrimSpace(r))
				}
			}
		} else if isNotFound(err) {
			err = nil
		}
	}
	c := Check{Selector: selector}
	switch {
	case err != nil:
		c.Status = CheckError
	case len(recs) == 0:
		c.Status = CheckMissing
	default:
		c.Record = clip(recs[0])
		if p := tag(recs[0], "p"); p == "" {
			c.Status, c.Detail = CheckMissing, "revoked" // p= with nothing in it is a revoked key
		} else {
			c.Status = CheckOK
		}
	}
	return c
}

func isNotFound(err error) bool {
	var dns *net.DNSError
	return errors.As(err, &dns) && dns.IsNotFound
}

// shortcut: the parent of the domain is found by dropping its first label, with no public-suffix list; for a domain under a two-label suffix (acme.co.uk) the parent
// looked up is the suffix, which has no DMARC record, so the answer is "missing", as it would be.
func checkDMARC(ctx context.Context, lookup TXTLookup, domain string) Check {
	names := []string{"_dmarc." + domain}
	if _, parent, ok := strings.Cut(domain, "."); ok && strings.Count(domain, ".") >= 2 {
		names = append(names, "_dmarc."+parent)
	}
	for i, name := range names {
		recs, err := txt(ctx, lookup, name, "v=dmarc1")
		if err != nil {
			return Check{Status: CheckError}
		}
		if len(recs) == 0 {
			continue
		}
		c := Check{Record: clip(recs[0])}
		switch p := strings.ToLower(tag(recs[0], "p")); p {
		case "quarantine", "reject":
			c.Status, c.Detail = CheckOK, p
		default:
			c.Status, c.Detail = CheckWeak, "policy_none"
		}
		if i > 0 {
			c.Detail += "_inherited" // the record of the parent domain applies to it
		}
		return c
	}
	return Check{Status: CheckMissing}
}

// tag returns the value of a tag in a "k=v; k=v" record.
func tag(record, name string) string {
	for _, part := range strings.Split(record, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
