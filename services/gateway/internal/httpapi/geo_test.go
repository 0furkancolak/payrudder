package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/0furkancolak/payrudder/services/gateway/internal/geo"
)

// The address lists: read by any signed-in person, the same for everybody, cached for a day and revalidated by ETag. nafru-geo is a fake here.

const fakeGeoKey = "geo-test-key"

// withGeo points Deps.Geo at a fake nafru-geo that answers only with the right key; more tweaks run after it.
func withGeo(t *testing.T, more ...func(*Deps)) func(*Deps) {
	t.Helper()
	lists := map[string]any{
		"/countries": []map[string]any{
			{"code": "TR", "name": "Turkey", "iso3": "TUR", "continent": "AS", "geonameId": 298795},
			{"code": "US", "name": "United States", "iso3": "USA", "continent": "NA", "geonameId": 6252001},
			{"code": "DE", "name": "Germany", "iso3": "DEU", "continent": "EU", "geonameId": 2921044},
		},
		"/regions?country=TR": []map[string]any{
			{"code": "34", "name": "İstanbul", "asciiName": "Istanbul", "geonameId": 745042},
			{"code": "06", "name": "Ankara", "asciiName": "Ankara", "geonameId": 323784},
		},
		"/regions?country=US": []map[string]any{{"code": "CA", "name": "California", "asciiName": "California", "geonameId": 5332921}},
		"/districts?country=TR&region=34": []map[string]any{
			{"code": "1", "name": "Kadıköy", "asciiName": "Kadikoy", "kind": "admin2"},
			{"code": "2", "name": "Beşiktaş", "asciiName": "Besiktas", "kind": "admin2"},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != fakeGeoKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		items, ok := lists[r.URL.RequestURI()]
		if !ok {
			items = []any{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	}))
	t.Cleanup(srv.Close)
	return func(d *Deps) {
		d.Geo = geo.New(srv.URL, fakeGeoKey, srv.Client())
		for _, f := range more {
			f(d)
		}
	}
}

func TestGeoListsForASignedInPerson(t *testing.T) {
	p := newProduct(t, nil, withGeo(t))
	resp, m := p.call(t, "GET", "/app/v1/geo/countries", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("countries: %d %v", resp.StatusCode, m)
	}
	if !strings.Contains(m["attribution"].(string), "GeoNames") || !strings.Contains(m["attribution"].(string), "CC BY 4.0") {
		t.Errorf("attribution: %v", m["attribution"])
	}
	countries := m["countries"].([]any)
	if len(countries) != 3 || countries[0].(map[string]any)["iso2"] != "DE" || countries[1].(map[string]any)["iso3"] != "TUR" {
		t.Fatalf("countries (by ISO2, with ISO3): %v", countries)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != geoCacheControl {
		t.Errorf("Cache-Control %q", cc)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("no ETag")
	}

	resp, m = p.call(t, "GET", "/app/v1/geo/countries/tr/regions", tokAda, "", "")
	if resp.StatusCode != 200 || m["country"] != "TR" {
		t.Fatalf("regions: %d %v", resp.StatusCode, m["country"])
	}
	var istanbul map[string]any
	for _, r := range m["regions"].([]any) {
		if r.(map[string]any)["id"] == "TR.34" {
			istanbul = r.(map[string]any)
		}
	}
	if istanbul == nil || istanbul["code"] != "34" || istanbul["ascii_name"] != "Istanbul" {
		t.Fatalf("TR.34 missing: %v", istanbul)
	}

	resp, m = p.call(t, "GET", "/app/v1/geo/regions/TR.34/districts", tokAda, "", "")
	if resp.StatusCode != 200 || m["region"] != "TR.34" {
		t.Fatalf("districts: %d %v", resp.StatusCode, m)
	}
	districts := m["districts"].([]any)
	if len(districts) != 2 || districts[0].(map[string]any)["name"] != "Beşiktaş" {
		t.Fatalf("districts (by ASCII name): %v", districts)
	}
	if k := districts[1].(map[string]any); k["id"] != "TR.34.1" || k["kind"] != "admin2" {
		t.Errorf("Kadıköy: %v", k)
	}
}

func TestGeoAnswersTheSameToEverybodyAndNothingAboutThem(t *testing.T) {
	p := newProduct(t, nil, withGeo(t, withOperators("boss@example.com")))
	read := func(tok string) (string, http.Header) {
		req, _ := http.NewRequest("GET", p.ts.URL+"/app/v1/geo/countries/US/regions", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d %s", tok, resp.StatusCode, b)
		}
		return string(b), resp.Header
	}
	ada, h1 := read(tokAda)
	bob, h2 := read(tokBob)
	op, _ := read(tokOperator)
	if ada != bob || ada != op || h1.Get("ETag") != h2.Get("ETag") {
		t.Fatal("the lists must not depend on who asks")
	}
	for _, leak := range []string{adaUser, bobUser, adaAcct, "ada@example.com", "boss@example.com"} {
		if strings.Contains(ada, leak) {
			t.Errorf("answer carries %s", leak)
		}
	}
	if strings.Contains(h1.Get("Cache-Control"), "public") || h1.Get("Set-Cookie") != "" {
		t.Errorf("headers: %v", h1)
	}
}

func TestGeoRevalidatesByETag(t *testing.T) {
	p := newProduct(t, nil, withGeo(t))
	resp, _ := p.call(t, "GET", "/app/v1/geo/countries", tokAda, "", "")
	tag := resp.Header.Get("ETag")
	req, _ := http.NewRequest("GET", p.ts.URL+"/app/v1/geo/countries", nil)
	req.Header.Set("Authorization", "Bearer "+tokAda)
	req.Header.Set("If-None-Match", tag)
	again, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Body.Close()
	if again.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", again.StatusCode)
	}
}

func TestGeoRefusals(t *testing.T) {
	p := newProduct(t, nil, withGeo(t))
	for _, c := range []struct {
		path, tok string
		code      int
		key       string
	}{
		{"/app/v1/geo/countries", "", 401, "identity.session.invalid"},
		{"/app/v1/geo/countries", "qs_unknown", 401, ""},
		{"/app/v1/geo/countries/ZZ/regions", tokAda, 404, "gateway.geo.country_not_found"},
		{"/app/v1/geo/countries/TUR/regions", tokAda, 400, "gateway.request.invalid"},
		{"/app/v1/geo/regions/TR.999/districts", tokAda, 404, "gateway.geo.region_not_found"},
		{"/app/v1/geo/regions/TR/districts", tokAda, 400, "gateway.request.invalid"},
		{"/app/v1/geo/regions/TR.3%2F4/districts", tokAda, 400, "gateway.request.invalid"},
	} {
		resp, m := p.call(t, "GET", c.path, c.tok, "", "")
		if resp.StatusCode != c.code || (c.key != "" && errKeyOf(m) != c.key) {
			t.Errorf("%s (%q): %d %v, want %d %s", c.path, c.tok, resp.StatusCode, m, c.code, c.key)
		}
	}
}

func TestGeoUnavailableWithoutAKey(t *testing.T) {
	p := newProduct(t, nil, nil)
	for _, path := range []string{"/app/v1/geo/countries", "/app/v1/geo/countries/TR/regions", "/app/v1/geo/regions/TR.34/districts"} {
		resp, m := p.call(t, "GET", path, tokAda, "", "")
		if resp.StatusCode != http.StatusServiceUnavailable || errKeyOf(m) != "gateway.geo.unavailable" {
			t.Errorf("%s: %d %v", path, resp.StatusCode, m)
		}
	}
}
