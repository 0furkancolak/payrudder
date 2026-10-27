// Package model holds the rules of Payrudder as a SaaS (phase 55, ADR 0024) that need no database: the resources a plan limits, the limit of one resource, how
// a plan's limits and an operator's own limits combine, and the three plans the platform tenant is seeded with.
package model

import (
	"fmt"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// Resource is something a plan limits. Its name is the code of the meter in the platform tenant whose entitlement holds the limit, so the plans are
// edited with the ordinary package pages.
type Resource string

const (
	// Events are the usage events the organisation's live tenants accept in a calendar month (counted by limits-svc).
	Events Resource = "events"
	// Apps are the applications of the account.
	Apps Resource = "apps"
	// Members are the people of the account plus its open invitations.
	Members Resource = "members"
	// APIKeys are the active API keys of all the account's tenants.
	APIKeys Resource = "api_keys"
)

// Resources lists them in the order they are shown.
var Resources = []Resource{Events, Apps, Members, APIKeys}

// Meter is the code of the platform-tenant meter of the resource.
func (r Resource) Meter() string { return string(r) }

// ParseResource reads a resource name.
func ParseResource(s string) (Resource, bool) {
	for _, r := range Resources {
		if string(r) == s {
			return r, true
		}
	}
	return "", false
}

// Mode is how a limit is enforced, as the quota engine names it.
type Mode string

const (
	Hard   Mode = "hard"
	Soft   Mode = "soft"
	Notify Mode = "notify"
)

// ParseMode reads a stored mode; anything unknown is hard, the safe reading of a limit.
func ParseMode(s string) Mode {
	switch Mode(s) {
	case Soft:
		return Soft
	case Notify:
		return Notify
	}
	return Hard
}

// Limit is the limit on one resource. Units 0 is unlimited, as in the entitlements.
type Limit struct {
	Units int64
	Mode  Mode
}

// Unlimited reports whether nothing is limited.
func (l Limit) Unlimited() bool { return l.Units == 0 }

// Blocks reports whether creating one more of the resource, when `used` exist, is refused. Only a hard limit refuses: a soft or notify limit is
// the quota engine's business (a bill, an alert), never a refusal.
func (l Limit) Blocks(used int64) bool {
	return !l.Unlimited() && l.Mode == Hard && used+1 > l.Units
}

// Over reports whether the usage has reached the limit, whatever the mode.
func (l Limit) Over(used int64) bool { return !l.Unlimited() && used >= l.Units }

// Fits reports whether `used` of the resource can stay under the limit when the organisation moves to a plan with it.
func (l Limit) Fits(used int64) bool { return l.Unlimited() || used <= l.Units }

// Limits is the limit of every resource that has one. A resource that is not in it is unlimited.
type Limits map[Resource]Limit

// Of returns the limit of r.
func (l Limits) Of(r Resource) Limit {
	if v, ok := l[r]; ok {
		return v
	}
	return Limit{Mode: Hard}
}

// Merge returns the limits an organisation lives by: its plan's, with every limit of its own replacing the plan's on that resource. It reports which
// resources are the organisation's own.
func Merge(plan, own Limits) (Limits, map[Resource]bool) {
	out := Limits{}
	for r, l := range plan {
		out[r] = l
	}
	overridden := map[Resource]bool{}
	for r, l := range own {
		out[r] = l
		overridden[r] = true
	}
	return out, overridden
}

// FromEntitlements reads the limits out of the entitlements of a plan version or the overrides of a customer: meter code, limit units and mode. Meters
// that are not resources are ignored (the operator may add others to a plan).
func FromEntitlements(rows []Entitlement) Limits {
	out := Limits{}
	for _, e := range rows {
		if r, ok := ParseResource(e.Meter); ok {
			out[r] = Limit{Units: e.Units, Mode: ParseMode(e.Mode)}
		}
	}
	return out
}

// Entitlement is a stored limit, as the queries return it.
type Entitlement struct {
	Meter string
	Units int64
	Mode  string
}

// QuotaError is the refusal to create one more of a resource, with the key the console shows an upgrade prompt for.
func QuotaError(r Resource) error {
	switch r {
	case Apps:
		return errkey.New(codes.ResourceExhausted, "plans.saas.quota.apps", "the plan's limit of apps is reached: upgrade the plan to add more")
	case Members:
		return errkey.New(codes.ResourceExhausted, "plans.saas.quota.members", "the plan's limit of members is reached: upgrade the plan to invite more people")
	case APIKeys:
		return errkey.New(codes.ResourceExhausted, "plans.saas.quota.api_keys", "the plan's limit of API keys is reached: revoke a key or upgrade the plan")
	}
	// The events quota is the quota engine's and is answered by the gateway at ingest; no other resource is created through the guard.
	return errkey.New(codes.InvalidArgument, "plans.saas.bad_resource", fmt.Sprintf("%q is not a resource a plan refuses to create", r))
}

// Plan codes of the seeded plans.
const (
	PlanFree  = "free"
	PlanPro   = "pro"
	PlanScale = "scale"
)

// PlanSpec is a plan as it is seeded into the platform tenant.
type PlanSpec struct {
	Code        string
	Name        string
	MonthlyFee  int64 // minor units of the platform currency
	Limits      Limits
	Description string
}

// DefaultPlans are Free, Pro and Scale. They are only the first version: the operator edits them as packages of the platform tenant, and a new version
// there replaces these for every organisation that moves to the plan afterwards.
func DefaultPlans() []PlanSpec {
	return []PlanSpec{
		{Code: PlanFree, Name: "Free", MonthlyFee: 0, Limits: Limits{
			Events: {Units: 10_000, Mode: Hard}, Apps: {Units: 1, Mode: Hard}, Members: {Units: 2, Mode: Hard}, APIKeys: {Units: 4, Mode: Hard}}},
		{Code: PlanPro, Name: "Pro", MonthlyFee: 2900, Limits: Limits{
			Events: {Units: 1_000_000, Mode: Hard}, Apps: {Units: 5, Mode: Hard}, Members: {Units: 10, Mode: Hard}, APIKeys: {Units: 20, Mode: Hard}}},
		{Code: PlanScale, Name: "Scale", MonthlyFee: 9900, Limits: Limits{
			Events: {Units: 10_000_000, Mode: Notify}, Apps: {Units: 25, Mode: Hard}, Members: {Units: 50, Mode: Hard}, APIKeys: {Units: 100, Mode: Hard}}},
	}
}

// Meter describes a platform-tenant meter the plans' entitlements hang on.
type Meter struct {
	Code  string
	Count bool // COUNT (no value property); otherwise SUM of the event quantity
	Unit  string
}

// Meters are the four meters of the platform tenant.
func Meters() []Meter {
	return []Meter{
		{Code: Events.Meter(), Unit: "events"},
		{Code: Apps.Meter(), Count: true, Unit: "apps"},
		{Code: Members.Meter(), Count: true, Unit: "members"},
		{Code: APIKeys.Meter(), Count: true, Unit: "keys"},
	}
}
