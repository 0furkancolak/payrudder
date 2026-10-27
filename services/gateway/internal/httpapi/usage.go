package httpapi

import (
	"net/http"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

type usageJSON struct {
	CustomerExternalID string          `json:"customer_external_id"`
	PeriodID           string          `json:"period_id"`
	Usage              []meterUsageRow `json:"usage"`
}

type meterUsageRow struct {
	MeterCode  string `json:"meter_code"`
	Quantity   string `json:"quantity"`
	EventCount int64  `json:"event_count,string"`
}

// getCustomerUsage is GET /v1/customers/{external_id}/usage?period=YYYY-MM: the on-time usage per meter. The period
// defaults to the current one in the tenant's time zone.
func (s *Server) getCustomerUsage(w http.ResponseWriter, r *http.Request) {
	periodID := r.URL.Query().Get("period")
	if periodID == "" {
		periodID = period.PeriodOf(s.now(), tenantLocation(principalFrom(r.Context()).Tenant.Timezone))
	} else if !period.Valid(periodID) {
		writeError(w, r, invalid("period", "must look like 2026-11"))
		return
	}
	cust, ok := s.resolveCustomer(w, r, r.PathValue("external_id"))
	if !ok {
		return
	}
	resp, err := s.d.Usage.GetCustomerUsage(r.Context(), &aggregatorv1.GetCustomerUsageRequest{
		CustomerId: cust.GetId(), PeriodId: periodID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := usageJSON{CustomerExternalID: cust.GetExternalId(), PeriodID: periodID, Usage: []meterUsageRow{}}
	for _, u := range resp.GetUsage() {
		out.Usage = append(out.Usage, meterUsageRow{MeterCode: u.GetMeterCode(), Quantity: u.GetQuantity(), EventCount: u.GetEventCount()})
	}
	writeJSONValue(w, http.StatusOK, out)
}
