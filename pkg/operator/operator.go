// Package operator is the one place that reads the platform operators' address lists (PAYRUDDER_OPERATOR_EMAILS and PAYRUDDER_OPERATOR_SUPPORT_EMAILS), so the
// gateway and plans-svc agree on who is an operator and in which role.
package operator

import "strings"

// Role is what an operator may do on /admin/v1.
type Role string

const (
	// None: not an operator.
	None Role = ""
	// Admin may do everything (the original list; the machine token is an admin too).
	Admin Role = "operator_admin"
	// Support may read every operator route and write none.
	Support Role = "operator_support"
)

// CanWrite says whether the role may call a route that changes something.
func (r Role) CanWrite() bool { return r == Admin }

// Roles are the two lists, lower case, without empty entries.
type Roles struct{ admins, support []string }

// split reads lists whose items may themselves hold comma-separated values, trimmed and lower case, empty entries dropped.
func split(list []string) []string {
	var out []string
	for _, e := range strings.Split(strings.Join(list, ","), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Parse reads the two lists. An address in both lists is a support operator: the more restrictive role wins, so a copy-paste into the wrong list can never widen
// access.
func Parse(admins, support []string) Roles {
	r := Roles{support: split(support)}
	for _, a := range split(admins) {
		if !contains(r.support, a) {
			r.admins = append(r.admins, a)
		}
	}
	return r
}

func contains(l []string, s string) bool {
	for _, e := range l {
		if e == s {
			return true
		}
	}
	return false
}

// Admins are the addresses whose role is Admin (overlap with the support list removed).
func (r Roles) Admins() []string { return r.admins }

// Support are the addresses whose role is Support.
func (r Roles) Support() []string { return r.support }

// Of is the role of an address; a person who has not verified the address is no operator.
func (r Roles) Of(email string, verified bool) Role {
	email = strings.ToLower(strings.TrimSpace(email))
	switch {
	case !verified || email == "":
		return None
	case contains(r.support, email):
		return Support
	case contains(r.admins, email):
		return Admin
	}
	return None
}
