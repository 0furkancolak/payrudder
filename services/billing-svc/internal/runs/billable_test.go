package runs

import (
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// The scheduler never invoices a live tenant that has not been activated, or one an operator suspended; a status it was not sent counts as active so a
// rollout of plans-svc and billing-svc in either order does not stop billing.
func TestBillableFollowsTheTenantStatus(t *testing.T) {
	for st, want := range map[plansv1.TenantStatus]bool{
		plansv1.TenantStatus_TENANT_STATUS_ACTIVE:      true,
		plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED: true,
		plansv1.TenantStatus_TENANT_STATUS_INACTIVE:    false,
		plansv1.TenantStatus_TENANT_STATUS_SUSPENDED:   false,
	} {
		if got := billable(&plansv1.Tenant{Status: st}); got != want {
			t.Errorf("billable(%v) = %v, want %v", st, got, want)
		}
	}
}
