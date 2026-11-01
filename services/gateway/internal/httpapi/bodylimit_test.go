package httpapi

import (
	"net/http"
	"testing"
)

// A settlement report may be 5 MiB (payments-worker takes as much); every other operator route keeps the ordinary limit.
func TestSettlementImportMayBeLargerThanOtherBodies(t *testing.T) {
	if got := bodyLimit(http.MethodPost, "/admin/v1/tenants/{tenant_id}/settlement/import"); got != 5<<20 {
		t.Errorf("import limit = %d", got)
	}
	if got := bodyLimit(http.MethodPost, "/admin/v1/tenants/{tenant_id}/reconciliation/runs"); got != MaxBodyBytes {
		t.Errorf("run limit = %d, want the ordinary %d", got, MaxBodyBytes)
	}
}
