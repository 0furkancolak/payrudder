package convert

import (
	"testing"

	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// An account ban reads a live tenant as banned and leaves a sandbox tenant as it is (plan 14).
func TestBanLiveOnlyTouchesLiveTenants(t *testing.T) {
	live, sandbox := sqlc.Tenant{Environment: "live", Status: "active"}, sqlc.Tenant{Environment: "sandbox", Status: "active"}
	BanLive(&live)
	BanLive(&sandbox)
	if live.Status != "banned" || sandbox.Status != "active" {
		t.Fatalf("live %q, sandbox %q", live.Status, sandbox.Status)
	}
}
