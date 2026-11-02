package command

import (
	"testing"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
)

// Which change of status mails the owners, and with what: going out of service says which way, coming back from it says reinstated, and the rest says nothing.
func TestStatusMailKind(t *testing.T) {
	a, i, s, f, b := domain.StatusActive, domain.StatusInactive, domain.StatusSuspended, domain.StatusFrozen, domain.StatusBanned
	for _, tc := range []struct {
		from, to string
		want     usermail.Kind
	}{
		{a, s, usermail.KindTenantSuspended}, {a, f, usermail.KindTenantFrozen}, {a, b, usermail.KindTenantBanned},
		{s, b, usermail.KindTenantBanned}, {b, f, usermail.KindTenantFrozen},
		{s, a, usermail.KindTenantReinstated}, {f, a, usermail.KindTenantReinstated}, {b, a, usermail.KindTenantReinstated},
		{i, a, ""}, {a, a, ""}, {s, s, ""},
	} {
		got, ok := statusMailKind(tc.from, tc.to)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%s -> %s: %q, %v; want %q", tc.from, tc.to, got, ok, tc.want)
		}
	}
}
