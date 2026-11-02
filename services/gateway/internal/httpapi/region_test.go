package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// fakeRegions is the directory of the tests: the home region of each tenant, "" (pinned to none) for one it does not list.
type fakeRegions struct {
	mu    sync.Mutex
	home  map[uuid.UUID]string
	err   error
	asked int
}

func (f *fakeRegions) Placement(_ context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked++
	if f.err != nil {
		return tenantdir.Placement{}, f.err
	}
	return tenantdir.Placement{TenantID: id, Region: f.home[id], Status: tenantdir.StatusReady}, nil
}

func (f *fakeRegions) Invalidate(uuid.UUID) {}

const oneEvent = `{"events":[{"idempotency_key":"k1","customer_external_id":"cus_1","meter_code":"gb","quantity":"1","event_time":"2026-11-03T10:15:00Z"}]}`

// regionServer is a gateway of region "us" over a directory in which liveTenant lives in "eu" and sandboxTenant in "us".
func regionServer(t *testing.T, dir *fakeRegions, urls map[string]string) (*httptest.Server, *fakePlans, *fakeIngest, *fakeLimiter) {
	t.Helper()
	return newTestServerWith(t, func(d *Deps) { d.Region = &Region{Name: "us", URLs: urls, Directory: dir} })
}

// Residency at the edge: a request for a tenant whose home is another region is answered by the gateway and goes no further. Nothing downstream sees the
// request or the key's tenant, and it is not charged against the tenant's rate limit.
func TestRegionGuardRefusesAnotherRegionsTenant(t *testing.T) {
	dir := &fakeRegions{home: map[uuid.UUID]string{liveTenant: "eu", sandboxTenant: "us"}}
	ts, plans, ing, lim := regionServer(t, dir, map[string]string{"eu": "https://eu.api.example.test"})

	resp, m := do(t, ts, "POST", "/v1/events", "live-key", oneEvent)
	e, _ := m["error"].(map[string]any)
	if resp.StatusCode != http.StatusMisdirectedRequest || errCode(m) != "WRONG_REGION" || e["key"] != "gateway.region.wrong" {
		t.Fatalf("a tenant of eu on the us gateway: %d %v", resp.StatusCode, m)
	}
	if resp.Header.Get(RegionHeader) != "eu" || resp.Header.Get(RegionURLHeader) != "https://eu.api.example.test" {
		t.Fatalf("hint headers = %q %q", resp.Header.Get(RegionHeader), resp.Header.Get(RegionURLHeader))
	}
	if !strings.Contains(e["message"].(string), "https://eu.api.example.test") || e["request_id"] == "" {
		t.Fatalf("message = %v", e)
	}
	if ing.last != nil || plans.lastTenant.ID != uuid.Nil {
		t.Fatalf("the request reached a backend: ingest %v, plans saw %+v", ing.last, plans.lastTenant)
	}
	if len(lim.costs) != 0 {
		t.Fatalf("the refused request was charged: %v", lim.costs)
	}

	// A tenant of this region is served as it always was.
	if resp, m := do(t, ts, "POST", "/v1/events", "sandbox-key", oneEvent); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("a tenant of us on the us gateway: %d %v", resp.StatusCode, m)
	}
	if ing.tenant.ID != sandboxTenant {
		t.Fatalf("ingest-svc saw %+v", ing.tenant)
	}
	// Every tenant route is behind the guard, not only the ingest.
	if resp, m := do(t, ts, "GET", "/v1/customers/c", "live-key", ""); resp.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("GET /v1/customers: %d %v", resp.StatusCode, m)
	}
}

// A tenant with no home region is served by every gateway; a region without a configured address still gets the region in the answer, without an address.
func TestRegionGuardLeavesTenantsWithoutARegionAlone(t *testing.T) {
	dir := &fakeRegions{home: map[uuid.UUID]string{liveTenant: "eu"}}
	ts, _, _, lim := regionServer(t, dir, nil)

	// "limited-key" is a tenant the directory has no region for: it gets past the guard and meets its own rate limit.
	if resp, m := do(t, ts, "GET", "/v1/customers/c", "limited-key", ""); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("a tenant with no region: %d %v", resp.StatusCode, m)
	}
	if len(lim.costs) == 0 {
		t.Fatal("the request did not get as far as the rate limit")
	}
	resp, _ := do(t, ts, "GET", "/v1/customers/c", "live-key", "")
	if resp.StatusCode != http.StatusMisdirectedRequest || resp.Header.Get(RegionHeader) != "eu" || resp.Header.Get(RegionURLHeader) != "" {
		t.Fatalf("no address configured: %d %v", resp.StatusCode, resp.Header)
	}
}

// The guard runs after the key is known: a caller with no valid key learns nothing about where tenants live, and an operator acting as a tenant is held to the
// tenant's region like anyone else.
func TestRegionGuardComesAfterAuthentication(t *testing.T) {
	dir := &fakeRegions{home: map[uuid.UUID]string{liveTenant: "eu"}}
	ts, _, ing, _ := regionServer(t, dir, nil)

	if resp, _ := do(t, ts, "POST", "/v1/events", "nope", oneEvent); resp.StatusCode != http.StatusUnauthorized || resp.Header.Get(RegionHeader) != "" {
		t.Fatalf("a bad key: %d %v", resp.StatusCode, resp.Header)
	}
	if dir.asked != 0 {
		t.Fatalf("the directory was asked %d times for a request with no valid key", dir.asked)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/v1/customers/c", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+operatorToken)
	req.Header.Set(ActAsHeader, liveTenant.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMisdirectedRequest || resp.Header.Get(RegionHeader) != "eu" {
		t.Fatalf("an operator acting as an eu tenant on the us gateway: %d %v", resp.StatusCode, resp.Header)
	}
	if ing.last != nil {
		t.Fatal("the request reached ingest-svc")
	}
}

// A directory that cannot answer refuses (503, retry is safe); the gateway does not guess a region.
func TestRegionGuardFailsClosed(t *testing.T) {
	dir := &fakeRegions{err: errors.New("plans-svc is down")}
	ts, _, ing, _ := regionServer(t, dir, nil)
	resp, m := do(t, ts, "POST", "/v1/events", "sandbox-key", oneEvent)
	e, _ := m["error"].(map[string]any)
	if resp.StatusCode != http.StatusServiceUnavailable || e["key"] != "identity.tenant.lookup_unavailable" || ing.last != nil {
		t.Fatalf("no answer from the directory: %d %v (ingest saw %v)", resp.StatusCode, m, ing.last)
	}
}

// Off by default: a gateway without PAYRUDDER_REGION serves the tenant of any region and never asks the directory.
func TestRegionGuardIsOffByDefault(t *testing.T) {
	ts, _, ing, lim := newFullTestServer(t)
	if resp, m := do(t, ts, "POST", "/v1/events", "live-key", oneEvent); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("%d %v", resp.StatusCode, m)
	}
	if ing.tenant.ID != liveTenant || len(lim.costs[ratelimit.ClassIngest]) != 1 {
		t.Fatalf("ingest saw %+v, charges %v", ing.tenant, lim.costs)
	}
}
