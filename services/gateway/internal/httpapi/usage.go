package httpapi

import (
	"net/http"
	"regexp"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

var periodParam = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

type usageJSON struct {
	CustomerExternalID string          `json:"customer_external_id"`
	PeriodID           string          `json:"period_id"`
	Usage              []meterUsageRow `json:"usage"`
}

type meterUsageRow struct {
	MeterCode  string `json:"meter_code"`
	Quantity   string `json:"quantity"`
	EventCount int64  `json:"event_count"`
}

// getCustomerUsage is GET /v1/customers/{external_id}/usage?period=YYYY-MM: the on-time usage per meter. The period
// defaults to the current one in the tenant's time zone.
func (s *Server) getCustomerUsage(w http.ResponseWriter, r *http.Request) {
	periodID := r.URL.Query().Get("period")
	if periodID == "" {
		loc := time.UTC
		if tz := principalFrom(r.Context()).Tenant.Timezone; tz != "" {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
			}
		}
		periodID = period.PeriodOf(time.Now(), loc)
	} else if !periodParam.MatchString(periodID) {
		writeError(w, r, invalid("period", "must look like 2026-11"))
		return
	}
	cust, err := s.d.Plans.ResolveCustomer(r.Context(), &plansv1.ResolveCustomerRequest{ExternalId: r.PathValue("external_id")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.d.Usage.GetCustomerUsage(r.Context(), &aggregatorv1.GetCustomerUsageRequest{
		CustomerId: cust.GetCustomer().GetId(), PeriodId: periodID})
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := usageJSON{CustomerExternalID: cust.GetCustomer().GetExternalId(), PeriodID: periodID, Usage: []meterUsageRow{}}
	for _, u := range resp.GetUsage() {
		out.Usage = append(out.Usage, meterUsageRow{MeterCode: u.GetMeterCode(), Quantity: u.GetQuantity(), EventCount: u.GetEventCount()})
	}
	writeJSONValue(w, http.StatusOK, out)
}
