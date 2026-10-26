package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// intParam reads an optional integer query parameter: 0 when it is absent. The service behind the route decides what range it accepts.
func intParam(q url.Values, name string) (int32, error) {
	v := q.Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return 0, invalid(name, "must be an integer")
	}
	return int32(n), nil
}

// rangedParam reads an optional integer query parameter that must lie in [lo, hi]: 0 when it is absent. message is what the person is told when it does not.
func rangedParam(q url.Values, name string, lo, hi int32, message string) (int32, error) {
	v := q.Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < int64(lo) || n > int64(hi) {
		return 0, invalid(name, message)
	}
	return int32(n), nil
}

// resolveCustomer finds the customer with the given external id, or writes the error and reports false.
func (s *Server) resolveCustomer(w http.ResponseWriter, r *http.Request, externalID string) (*plansv1.Customer, bool) {
	resp, err := s.d.Plans.ResolveCustomer(r.Context(), &plansv1.ResolveCustomerRequest{ExternalId: externalID})
	if err != nil {
		writeError(w, r, err)
		return nil, false
	}
	return resp.GetCustomer(), true
}

// respondSignedURL answers a presigned download link: {"url", "expires_at"}, or the error the service gave instead.
func respondSignedURL(w http.ResponseWriter, r *http.Request, url string, expires *timestamppb.Timestamp, err error) {
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSONValue(w, http.StatusOK, map[string]any{"url": url, "expires_at": expires.AsTime().UTC().Format(time.RFC3339)})
}

// normalizeEmail is the form an e-mail address is compared in: trimmed and lower case.
func normalizeEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// tenantLocation is the tenant's time zone, UTC when it names none that can be loaded.
func tenantLocation(zone string) *time.Location {
	if loc, err := time.LoadLocation(zone); err == nil {
		return loc
	}
	return time.UTC
}
