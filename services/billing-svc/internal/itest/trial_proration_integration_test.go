//go:build integration

package itest

import (
	"testing"

	"google.golang.org/protobuf/proto"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/google/uuid"
)

// A trial that ends inside a billing period used to charge the whole fixed fee. A plan version that prorates after a trial (billing-svc-status 17, the default of a new
// version) charges the first paid period from the end of the trial to the end of the period; a version published before the rule keeps charging it whole, and a trial that
// runs past the period or ended before it is what it always was.
func testATrialEndingInsideThePeriodChargesFromItsEnd(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.startWorkers(nil)
	old := e.pv() // base fee 1000, published before the rule: the whole period
	prorating := proto.Clone(old).(*plansv1.PlanVersion)
	prorating.Id, prorating.Version, prorating.ProrateAfterTrial = uuid.NewString(), 2, true
	e.w.mu.Lock()
	e.w.versions[prorating.Id] = prorating
	e.w.mu.Unlock()
	for _, name := range []string{"cus_c", "cus_d", "cus_e"} {
		e.w.customers[name] = &plansv1.Customer{Id: uuid.NewString(), ExternalId: name, Name: "Customer " + name, Email: name + "@example.com", PaymentMethod: "pm_sandbox_success"}
	}
	e.setSubs(
		e.sub("cus_a", prorating, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 10, 12) }), // 20 of 31 days: 645
		e.sub("cus_b", old, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 10, 12) }),       // the whole 1000, as before
		e.sub("cus_c", prorating, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 11, 10) }), // still on trial: waived
		e.sub("cus_d", prorating, func(s *plansv1.Subscription) { s.TrialEndTime = ts(2026, 9, 20) }),  // trial over before the period: whole
		e.sub("cus_e", prorating, nil),
	)
	e.addUsage("2026-10", "cus_c", "calls", "150", 0) // so that the waived customer has an invoice at all: 100

	e.driveRun("2026-10")

	for _, tc := range []struct {
		customer string
		want     int64
	}{{"cus_a", 645}, {"cus_b", 1000}, {"cus_c", 100}, {"cus_d", 1000}, {"cus_e", 1000}} {
		if got := e.invoiceTotal("2026-10", tc.customer); got != tc.want {
			t.Errorf("%s total = %d, want %d", tc.customer, got, tc.want)
		}
	}
	l := e.lines("2026-10", "cus_a")
	if len(l) != 1 || l[0].Kind != "BASE_FEE" || l[0].Description != "Base fee — pro — 2026-10-12 to 2026-11-01" {
		t.Errorf("cus_a lines = %+v; want one base fee line naming the days it covers", l)
	}
	if l := e.lines("2026-10", "cus_b"); len(l) != 1 || l[0].Description != "Base fee" {
		t.Errorf("cus_b lines = %+v; the older version bills as it always did", l)
	}
}
