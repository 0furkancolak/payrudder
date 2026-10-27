// Package onboarding keeps what the console remembers for a person: how far they are in the first-run wizard, and which page tours they have seen. It is a
// person-level record (the table has no tenant_id and, like users, is on the system pool only), so it follows the person to another browser.
//
// Writes are merges, never replacements, because two browsers may write at about the same time and neither may undo the other: the seen tours are a set that
// only grows (a person asking to see a tour again says so with forget_tours), the wizard is a document of which a write changes the fields it names, and a
// flag the wizard has set stays set. The merge is pure (this file); the row is locked while it is applied (onboarding.go), so the writers take turns and the
// one that comes last wins a field both changed.
package onboarding

import (
	"slices"
	"strings"
	"unicode"

	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// MaxTours bounds the tours one person can have seen. The console has a few dozen pages; this is room to grow, not a quota anyone meets.
const MaxTours = 200

// The statuses and steps of the wizard, as the console names them (web/admin/src/features/onboarding/state.ts).
var (
	statuses = []string{"active", "skipped", "done"}
	steps    = []string{"welcome", "organisation", "app", "meter", "package", "event", "done"}
)

// MergeWizard applies the fields present in patch to current (nil: the wizard was never started) and returns the result; current is not changed. A boolean
// the wizard has set is not unset by a patch that says false: that is a browser that has not heard yet.
func MergeWizard(current, patch *plansv1.OnboardingWizard) (*plansv1.OnboardingWizard, error) {
	var v domain.Violations
	if s := patch.Status; s != nil && !slices.Contains(statuses, *s) {
		v.Add("wizard.status", "active, skipped or done")
	}
	if s := patch.Step; s != nil && !slices.Contains(steps, *s) {
		v.Add("wizard.step", "one of the wizard's steps")
	}
	if s := patch.Currency; s != nil && !isCurrency(*s) {
		v.Add("wizard.currency", "three upper-case letters")
	}
	if s := patch.Timezone; s != nil && !isZone(*s) {
		v.Add("wizard.timezone", "an IANA time zone name")
	}
	for field, s := range map[string]*string{"wizard.app": patch.App, "wizard.meter": patch.Meter, "wizard.plan": patch.Plan} {
		if s != nil && !isRef(*s) {
			v.Add(field, "at most 128 printable characters")
		}
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	out := &plansv1.OnboardingWizard{}
	if current != nil {
		out = proto.Clone(current).(*plansv1.OnboardingWizard)
	}
	set := func(dst **string, src *string) {
		if src != nil {
			*dst = proto.String(*src)
		}
	}
	flag := func(dst **bool, src *bool) {
		if src != nil && (*src || *dst == nil) {
			*dst = proto.Bool(*src)
		}
	}
	set(&out.Status, patch.Status)
	set(&out.Step, patch.Step)
	set(&out.Currency, patch.Currency)
	set(&out.Timezone, patch.Timezone)
	set(&out.App, patch.App)
	set(&out.Meter, patch.Meter)
	set(&out.Plan, patch.Plan)
	flag(&out.Customer, patch.Customer)
	flag(&out.Event, patch.Event)
	flag(&out.Hidden, patch.Hidden)
	if out.Status == nil {
		out.Status = proto.String("active")
	}
	if out.Step == nil {
		out.Step = proto.String("welcome")
	}
	return out, nil
}

// MergeTours returns the seen tours with add joined and forget removed (in that order), sorted. Unknown names in forget are ignored.
func MergeTours(seen, add, forget []string) ([]string, error) {
	var v domain.Violations
	for _, id := range slices.Concat(add, forget) {
		if !isTourID(id) {
			v.Add("seen_tours", "tour ids are 1 to 64 letters, digits, dots, dashes and underscores")
			break
		}
	}
	if err := v.Err(); err != nil {
		return nil, err
	}
	set := append(slices.Clone(seen), add...)
	slices.Sort(set)
	set = slices.Compact(set)
	set = slices.DeleteFunc(set, func(id string) bool { return slices.Contains(forget, id) })
	if set == nil {
		set = []string{}
	}
	if len(set) > MaxTours {
		return nil, domain.Invalid("seen_tours", "too many tours")
	}
	return set, nil
}

func isCurrency(s string) bool {
	return len(s) == 3 && strings.IndexFunc(s, func(r rune) bool { return r < 'A' || r > 'Z' }) < 0
}

func isZone(s string) bool {
	return s != "" && len(s) <= 64 && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_/+-", r))
	}) < 0
}

// isRef accepts what the wizard stores of what it made: an id or a code, short and without control characters.
func isRef(s string) bool {
	return s != "" && len(s) <= 128 && strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}

func isTourID(s string) bool {
	return s != "" && len(s) <= 64 && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_')
	}) < 0
}
