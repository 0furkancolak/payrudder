package httpapi

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// The address lists: read by any signed-in person, the same for everybody, cached for a day and revalidated by ETag.

func TestGeoListsForASignedInPerson(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "GET", "/app/v1/geo/countries", tokAda, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("countries: %d %v", resp.StatusCode, m)
	}
	if !strings.Contains(m["attribution"].(string), "GeoNames") || !strings.Contains(m["attribution"].(string), "CC BY 4.0") {
		t.Errorf("attribution: %v", m["attribution"])
	}
	countries := m["countries"].([]any)
	if len(countries) < 240 {
		t.Fatalf("only %d countries", len(countries))
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
	if istanbul == nil || istanbul["code"] != "34" {
		t.Fatalf("TR.34 missing: %v", istanbul)
	}

	resp, m = p.call(t, "GET", "/app/v1/geo/regions/TR.34/districts", tokAda, "", "")
	if resp.StatusCode != 200 || m["region"] != "TR.34" {
		t.Fatalf("districts: %d %v", resp.StatusCode, m)
	}
	found := false
	for _, d := range m["districts"].([]any) {
		if d.(map[string]any)["name"] == "Kadıköy" {
			found = true
		}
	}
	if !found {
		t.Error("Kadikoy is not a district of Istanbul")
	}
}

func TestGeoAnswersTheSameToEverybodyAndNothingAboutThem(t *testing.T) {
	p := newProduct(t, nil, withOperators("boss@example.com"))
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
	p := newProduct(t, nil, nil)
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
	p := newProduct(t, nil, nil)
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
