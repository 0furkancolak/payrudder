package geo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeGeo is a nafru-geo that serves fixed lists to the right key and counts the reads; down makes it answer 503.
type fakeGeo struct {
	reads atomic.Int32
	down  atomic.Bool
}

func (f *fakeGeo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.reads.Add(1)
	if r.Header.Get("x-api-key") != "k" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.down.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	lists := map[string]any{
		"/countries": []map[string]any{
			{"code": "TR", "name": "Turkey", "iso3": "TUR", "continent": "AS", "geonameId": 298795},
			{"code": "DE", "name": "Germany", "iso3": "DEU", "continent": "EU", "geonameId": 2921044},
		},
		"/regions?country=TR": []map[string]any{
			{"code": "34", "name": "İstanbul", "asciiName": "Istanbul", "geonameId": 745042},
			{"code": "06", "name": "Ankara", "asciiName": "Ankara", "geonameId": 323784},
		},
		"/regions?country=DE": []map[string]any{{"code": "16", "name": "Berlin", "asciiName": "Berlin", "geonameId": 2950157}},
		"/districts?country=TR&region=34": []map[string]any{
			{"code": "1", "name": "Üsküdar", "asciiName": "Uskudar", "kind": "admin2"},
			{"code": "2", "name": "Kadıköy", "asciiName": "Kadikoy", "kind": "admin2"},
		},
		"/districts?country=DE&region=16": []map[string]any{{"code": "2950159", "name": "Berlin", "asciiName": "Berlin", "kind": "city"}},
	}
	items, ok := lists[r.URL.RequestURI()]
	if !ok {
		items = []any{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
}

func newClient(t *testing.T, key string) (*Client, *fakeGeo, *time.Time) {
	t.Helper()
	f := &fakeGeo{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New(srv.URL+"/", key, srv.Client())
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, f, &now
}

func TestListsKeepTheConsoleShape(t *testing.T) {
	c, _, _ := newClient(t, "k")
	ctx := context.Background()
	countries, err := c.Countries(ctx)
	if err != nil || len(countries) != 2 || countries[0] != (Country{ISO2: "DE", ISO3: "DEU", Name: "Germany", Continent: "EU", GeonameID: 2921044}) {
		t.Fatalf("countries (by ISO2): %+v %v", countries, err)
	}
	regions, ok, err := c.RegionsOf(ctx, "tr")
	if err != nil || !ok || len(regions) != 2 || regions[1] != (Region{ID: "TR.34", Code: "34", Name: "İstanbul", ASCIIName: "Istanbul", GeonameID: 745042}) {
		t.Fatalf("regions (by ASCII name): %+v %v %v", regions, ok, err)
	}
	districts, ok, err := c.DistrictsOf(ctx, "tr.34")
	if err != nil || !ok || len(districts) != 2 || districts[0] != (District{ID: "TR.34.2", Name: "Kadıköy", ASCIIName: "Kadikoy", Kind: KindAdmin2}) {
		t.Fatalf("districts: %+v %v %v", districts, ok, err)
	}
	towns, _, _ := c.DistrictsOf(ctx, "DE.16")
	if len(towns) != 1 || towns[0].ID != "2950159" || towns[0].Kind != KindCity {
		t.Fatalf("a town keeps its GeoNames id: %+v", towns)
	}
}

func TestUnknownCountryAndRegion(t *testing.T) {
	c, _, _ := newClient(t, "k")
	ctx := context.Background()
	if _, ok, err := c.RegionsOf(ctx, "ZZ"); ok || err != nil {
		t.Errorf("ZZ: %v %v", ok, err)
	}
	if _, ok, err := c.DistrictsOf(ctx, "TR.99"); ok || err != nil {
		t.Errorf("TR.99: %v %v", ok, err)
	}
	if _, ok, err := c.DistrictsOf(ctx, "TR"); ok || err != nil {
		t.Errorf("TR: %v %v", ok, err)
	}
}

func TestCachedForADayAndServedStaleWhileDown(t *testing.T) {
	c, f, now := newClient(t, "k")
	ctx := context.Background()
	for range 3 {
		if _, err := c.Countries(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.reads.Load(); n != 1 {
		t.Fatalf("read %d times within a day", n)
	}
	f.down.Store(true)
	*now = now.Add(TTL)
	if got, err := c.Countries(ctx); err != nil || len(got) != 2 {
		t.Fatalf("stale copy while down: %v %v", got, err)
	}
	if _, err := c.Countries(ctx); err != nil || f.reads.Load() != 2 {
		t.Fatalf("a failed read waits RetryAfter before the next: %d reads, %v", f.reads.Load(), err)
	}
	*now = now.Add(RetryAfter)
	f.down.Store(false)
	if _, err := c.Countries(ctx); err != nil || f.reads.Load() != 3 {
		t.Fatalf("read again after RetryAfter: %d reads, %v", f.reads.Load(), err)
	}
}

func TestUnavailableWithoutACopy(t *testing.T) {
	c, _, _ := newClient(t, "wrong")
	if _, err := c.Countries(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a refused key: %v", err)
	}
}
