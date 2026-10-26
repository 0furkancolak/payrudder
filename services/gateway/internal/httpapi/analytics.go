package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	"github.com/0furkancolak/payrudder/pkg/period"
)

// getAnalyticsUsage is GET /v1/analytics/usage: a usage series of one meter. The answer is an estimate (precision
// "estimated") unless exact=true, which covers at most one day.
func (s *Server) getAnalyticsUsage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &analyticsv1.QueryUsageRequest{MeterCode: q.Get("meter")}
	if req.MeterCode == "" {
		writeError(w, r, invalid("meter", "required"))
		return
	}
	var err error
	for name, dst := range map[string]**timestamppb.Timestamp{"from": &req.From, "to": &req.To} {
		t, perr := time.Parse(time.RFC3339, q.Get(name))
		if perr != nil {
			writeError(w, r, invalid(name, "required, RFC 3339 (2026-10-01T00:00:00Z)"))
			return
		}
		*dst = timestamppb.New(t)
	}
	switch g := q.Get("granularity"); g {
	case "", "hour":
		req.Granularity = analyticsv1.Granularity_GRANULARITY_HOUR
	case "day":
		req.Granularity = analyticsv1.Granularity_GRANULARITY_DAY
	default:
		writeError(w, r, invalid("granularity", "hour or day"))
		return
	}
	if v := q.Get("exact"); v != "" {
		if req.Exact, err = strconv.ParseBool(v); err != nil {
			writeError(w, r, invalid("exact", "true or false"))
			return
		}
	}
	if c := q.Get("customer"); c != "" {
		cust, ok := s.resolveCustomer(w, r, c)
		if !ok {
			return
		}
		req.CustomerId = cust.GetId()
	}
	resp, err := s.d.Analytics.QueryUsage(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// getTopCustomers is GET /v1/analytics/top-customers?meter=&period=&limit=.
func (s *Server) getTopCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := &analyticsv1.TopCustomersRequest{MeterCode: q.Get("meter"), PeriodId: q.Get("period")}
	if req.MeterCode == "" || !period.Valid(req.PeriodId) {
		writeError(w, r, invalid("meter", "meter and period (2026-11) are required"))
		return
	}
	n, err := intParam(q, "limit")
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.Limit = n
	resp, err := s.d.Analytics.TopCustomers(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}

// adminIngestStats is GET /admin/v1/analytics/ingest-stats?tenant_id=&window_seconds=.
func (s *Server) adminIngestStats(w http.ResponseWriter, r *http.Request) {
	req := &analyticsv1.GetIngestStatsRequest{TenantId: r.URL.Query().Get("tenant_id")}
	if v := r.URL.Query().Get("window_seconds"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			writeError(w, r, invalid("window_seconds", "must be an integer"))
			return
		}
		req.WindowSeconds = int32(n)
	}
	resp, err := s.d.Analytics.GetIngestStats(r.Context(), req)
	respond(w, r, http.StatusOK, resp, err)
}
