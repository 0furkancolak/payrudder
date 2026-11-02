package mailx

import (
	"context"
	"errors"
	"net"
	"testing"
)

// zone is a DNS with the records of a few names; a name it does not hold is "no such name", and "fail." is a resolver that fails.
type zone map[string][]string

func (z zone) lookup(_ context.Context, name string) ([]string, error) {
	if name == "_dmarc.fail.example" || name == "fail.example" {
		return nil, &net.DNSError{Err: "server misbehaving", Name: name, IsTemporary: true}
	}
	if r, ok := z[name]; ok {
		return r, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

func TestCheckIdentity(t *testing.T) {
	z := zone{
		"acme.example":                  {"google-site-verification=x", "v=spf1 include:_spf.nafru.example -all"},
		"_dmarc.acme.example":           {"v=DMARC1; p=reject; rua=mailto:d@acme.example"},
		"nafru._domainkey.acme.example": {"v=DKIM1; k=rsa; p=MIGfMA0GCSqGSIb3DQEBAQUAA4GN"},
		"weak.example":                  {"v=spf1 +all"},
		"_dmarc.weak.example":           {"v=DMARC1; p=none"},
		"two.example":                   {"v=spf1 -all", "v=spf1 ~all"},
		"soft.example":                  {"v=SPF1 mx ~all"},
		"open.example":                  {"v=spf1 mx"},
		"_dmarc.parent.example":         {"v=DMARC1; p=quarantine"},
		"parent.example":                {"v=spf1 ?all"},
		"k._domainkey.revoked.example":  {"v=DKIM1; p="},
		"nokey._domainkey.acme.example": {"some other record"},
		"bare._domainkey.acme.example":  {"k=rsa; p=ABCDEF"},
	}
	type want struct{ spf, spfDetail, dkim, dkimDetail, dmarc, dmarcDetail string }
	for name, c := range map[string]struct {
		from, selector string
		domain         string
		want           want
	}{
		"everything published":             {"Acme Billing <billing@Acme.Example>", "nafru", "acme.example", want{CheckOK, "hard_fail", CheckOK, "", CheckOK, "reject"}},
		"no selector, DKIM is not guessed": {"billing@acme.example", "", "acme.example", want{CheckOK, "hard_fail", CheckUnchecked, "no_selector", CheckOK, "reject"}},
		"a key with no v= is still a key":  {"billing@acme.example", "bare", "acme.example", want{CheckOK, "hard_fail", CheckOK, "", CheckOK, "reject"}},
		"no key under that selector":       {"billing@acme.example", "nokey", "acme.example", want{CheckOK, "hard_fail", CheckMissing, "", CheckOK, "reject"}},
		"a revoked key":                    {"billing@revoked.example", "k", "revoked.example", want{CheckMissing, "", CheckMissing, "revoked", CheckMissing, ""}},
		"an SPF that passes everything":    {"a@weak.example", "", "weak.example", want{CheckWeak, "pass_all", CheckUnchecked, "no_selector", CheckWeak, "policy_none"}},
		"two SPF records are an error":     {"a@two.example", "", "two.example", want{CheckInvalid, "multiple", CheckUnchecked, "no_selector", CheckMissing, ""}},
		"soft fail is accepted":            {"a@soft.example", "", "soft.example", want{CheckOK, "soft_fail", CheckUnchecked, "no_selector", CheckMissing, ""}},
		"an SPF with no all is weak":       {"a@open.example", "", "open.example", want{CheckWeak, "no_all", CheckUnchecked, "no_selector", CheckMissing, ""}},
		"DMARC of the parent domain":       {"a@mail.parent.example", "", "mail.parent.example", want{CheckMissing, "", CheckUnchecked, "no_selector", CheckOK, "quarantine_inherited"}},
		"nothing published":                {"a@bare.example", "", "bare.example", want{CheckMissing, "", CheckUnchecked, "no_selector", CheckMissing, ""}},
		"a failing resolver says nothing":  {"a@fail.example", "", "fail.example", want{CheckError, "", CheckUnchecked, "no_selector", CheckError, ""}},
	} {
		got, err := CheckIdentity(context.Background(), z.lookup, c.from, c.selector)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		have := want{got.SPF.Status, got.SPF.Detail, got.DKIM.Status, got.DKIM.Detail, got.DMARC.Status, got.DMARC.Detail}
		if got.Domain != c.domain || have != c.want {
			t.Errorf("%s: %s %+v, want %s %+v", name, got.Domain, have, c.domain, c.want)
		}
	}
}

func TestCheckIdentityRefusesWhatCannotBeLookedUp(t *testing.T) {
	never := func(context.Context, string) ([]string, error) {
		t.Error("a name that cannot be looked up was looked up")
		return nil, errors.New("no")
	}
	for _, c := range []struct{ from, selector string }{
		{"", ""}, {"not an address", ""}, {"a@localhost", ""}, {"a@[10.0.0.1]", ""}, {"a@10.0.0.1", ""}, {"a@acme.example", "bad selector"},
		{"a@acme.example", "../x"}, {"a@-acme.example", ""}, {"a@acme_.example", ""},
	} {
		if _, err := CheckIdentity(context.Background(), never, c.from, c.selector); !errors.Is(err, ErrIdentityInput) {
			t.Errorf("%+v: %v", c, err)
		}
	}
}
