package httpapi

import (
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/gateway/internal/auth"
)

// RegionHeader and RegionURLHeader are what a refused request learns about where to go: the tenant's home region and, if the deployment knows it, the address of
// that region's gateway. They are a hint; the gateway never redirects (a redirect would carry the key and the body to another host).
const (
	RegionHeader    = "X-Payrudder-Region"
	RegionURLHeader = "X-Payrudder-Region-Url"
)

// Region makes this gateway serve only the tenants whose home region it is (PAYRUDDER_REGION, plan 17). A tenant's home region is tenants.region in plans-svc,
// asked through the directory (cached, stale-if-error). A tenant with no region is not pinned to one and is served by every gateway. Off by default: Deps.Region
// is nil and nothing here runs.
type Region struct {
	// Name is the region this gateway serves.
	Name string
	// URLs maps a region name to the public address of the gateway there; used only for the hint header.
	URLs map[string]string
	// Directory answers where a tenant lives (tenantdir.NewGRPC over plans-svc's DirectoryService).
	Directory tenantdir.Source
}

// inRegion reports whether this gateway may serve the authenticated tenant, and answers the request itself when it may not. It runs before the permission check
// and the rate limit, so a request sent to the wrong region costs the tenant nothing and touches no backend: the residency guarantee is that nothing downstream
// of the gateway sees it. A directory that cannot be asked (and has no earlier answer) refuses with 503 rather than guess.
func (s *Server) inRegion(w http.ResponseWriter, r *http.Request, p auth.Principal) bool {
	g := s.d.Region
	if g == nil {
		return true
	}
	pl, err := g.Directory.Placement(r.Context(), p.Tenant.ID)
	if err != nil {
		s.d.Log.WarnContext(r.Context(), "region of the tenant not known", "tenant_id", p.Tenant.ID, "error", err)
		writeError(w, r, errkey.New(codes.Unavailable, "identity.tenant.lookup_unavailable", "tenant lookup unavailable"))
		return false
	}
	if pl.Region == "" || pl.Region == g.Name {
		return true
	}
	w.Header().Set(RegionHeader, pl.Region)
	msg := "this environment is served from the region " + pl.Region + ", not " + g.Name
	if u, ok := g.URLs[pl.Region]; ok {
		w.Header().Set(RegionURLHeader, u)
		msg += ": send the request to " + u
	}
	writeJSONValue(w, http.StatusMisdirectedRequest, ErrorBody{Error: ErrorInfo{Code: "WRONG_REGION", Key: errkey.Key("gateway.region.wrong"), Message: msg, RequestID: requestID(r.Context())}})
	return false
}
