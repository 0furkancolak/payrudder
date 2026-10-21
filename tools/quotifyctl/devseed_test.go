package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCheckDevTarget(t *testing.T) {
	for _, tc := range []struct {
		gateway string
		force   bool
		env     string
		ok      bool
	}{
		{"http://localhost:42800", false, "", true},
		{"http://127.0.0.1:42800", false, "local", true},
		{"http://[::1]:42800", false, "", true},
		{"http://api.localhost:8080", false, "", true},
		{"https://api.quotify.example.com", false, "", false},
		{"https://api.quotify.example.com", false, "production", false},
		{"http://10.0.0.5:42800", false, "", false},
		{"http://localhost.evil.com", false, "", false},
		{"http://dev-vm:42800", true, "", true},         // the explicit flag lets a dev stack on another name through
		{"http://localhost:42800", true, "prod", false}, // ...but never a production environment
		{"http://localhost:42800", false, "staging", false},
		{"not a url", false, "", false},
	} {
		err := checkDevTarget(tc.gateway, tc.force, tc.env)
		if (err == nil) != tc.ok {
			t.Errorf("checkDevTarget(%q, force=%v, env=%q) = %v, want ok=%v", tc.gateway, tc.force, tc.env, err, tc.ok)
		}
	}
}

func TestRunDevSeedRefusesBeforeAnyRequest(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	// The test server is on 127.0.0.1, so it passes; a production environment name must stop the run before a request.
	err := runDevSeed(devSeedOptions{Gateway: srv.URL, Token: "x", Env: "production", Out: io.Discard})
	if err == nil || hits != 0 {
		t.Fatalf("err = %v, hits = %d; a production environment must stop the seed before any request", err, hits)
	}
}

func TestUsageEventsAreDeterministicAndInThePast(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	data := dataFor("store", "TRY")
	a, b := usageEvents(now, data), usageEvents(now, data)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("events = %d and %d", len(a), len(b))
	}
	seen := map[string]bool{}
	for i, e := range a {
		key := e["idempotency_key"].(string)
		if seen[key] {
			t.Fatalf("key %s used twice", key)
		}
		seen[key] = true
		if e["quantity"] != b[i]["quantity"] || key != b[i]["idempotency_key"] {
			t.Fatalf("event %d differs between two builds", i)
		}
		at, err := time.Parse(time.RFC3339, e["event_time"].(string))
		if err != nil || !at.Before(now) || at.Month() != now.Month() {
			t.Fatalf("event time %v must be in the past and inside the current month", e["event_time"])
		}
	}
	// A day later the same events are all there again (same keys, same payloads) plus the new day's.
	next := usageEvents(now.AddDate(0, 0, 1), data)
	byKey := map[string]string{}
	for _, e := range next {
		byKey[e["idempotency_key"].(string)] = e["quantity"].(string)
	}
	for _, e := range a {
		if byKey[e["idempotency_key"].(string)] != e["quantity"] {
			t.Fatalf("event %v changed a day later: a retry would be refused as a different payload", e["idempotency_key"])
		}
	}
	if len(next) <= len(a) {
		t.Fatalf("a new day brought no new events: %d <= %d", len(next), len(a))
	}
}

func TestDevPeopleAreUniqueAndDocumented(t *testing.T) {
	emails, apps := map[string]bool{}, map[string]bool{}
	for _, p := range devPeople {
		if emails[p.Email] || !strings.HasSuffix(p.Email, "@example.com") {
			t.Errorf("e-mail %q repeated or not on example.com", p.Email)
		}
		emails[p.Email] = true
		for _, a := range p.Apps {
			if apps[a.Name] {
				t.Errorf("app name %q repeated", a.Name)
			}
			apps[a.Name] = true
		}
	}
	if devSeedUserOne != "user1@example.com" || devPassword != "password123" {
		t.Fatal("the documented dev credentials changed: update docs/10-local-dev.md and the README")
	}
}

// fakeStack is the part of the gateway the seed uses, kept in memory: collections that grow on POST and answer GET, idempotent
// grants and events, a sandbox close that makes one paid invoice per customer. It counts the requests that create things.
type fakeStack struct {
	mu      sync.Mutex
	coll    map[string][]map[string]any
	events  map[string]bool
	grants  map[string]bool
	creates map[string]int
	limited int // answer the next n requests with 429
}

func newFakeStack() *fakeStack {
	return &fakeStack{coll: map[string][]map[string]any{}, events: map[string]bool{}, grants: map[string]bool{}, creates: map[string]int{}}
}

var listKeys = map[string]string{"/v1/meters": "meters", "/v1/plans": "plans", "/v1/customers": "customers", "/v1/subscriptions": "subscriptions",
	"/v1/expenses": "expenses", "/v1/webhook-endpoints": "endpoints", "/v1/credit-packs": "packs", "/v1/payment-links": "links",
	"/v1/credit-notes": "credit_notes", "/v1/invoices": "invoices"}

func (f *fakeStack) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if f.limited > 0 {
		f.limited--
		w.WriteHeader(429)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	path := r.URL.Path
	switch {
	case path == "/v1/events":
		var res []map[string]any
		for _, e := range body["events"].([]any) {
			k := e.(map[string]any)["idempotency_key"].(string)
			st := "accepted"
			if f.events[k] {
				st = "duplicate"
			}
			f.events[k] = true
			res = append(res, map[string]any{"status": st})
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": res})
	case strings.HasSuffix(path, "/credits/grant"):
		k := body["idempotency_key"].(string)
		replayed := f.grants[k]
		f.grants[k] = true
		if !replayed {
			f.creates["grant"]++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"replayed": replayed})
	case strings.HasSuffix(path, "/credits/consume"), path == "/v1/credits/settings":
		_, _ = w.Write([]byte("{}"))
	case path == "/v1/sandbox/close-period":
		f.creates["close"]++
		for _, c := range f.coll["/v1/customers"] {
			f.coll["/v1/invoices"] = append(f.coll["/v1/invoices"], map[string]any{"id": "inv-" + c["external_id"].(string), "kind": "REGULAR",
				"status": "paid", "saga_state": "COMPLETED", "number": "SBX-" + c["external_id"].(string), "total_minor": "10000", "customer_external_id": c["external_id"]})
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte("{}"))
	case strings.HasPrefix(path, "/admin/v1/invoices/") && strings.HasSuffix(path, "/credit-notes"):
		f.creates["credit-note"]++
		f.coll["/v1/credit-notes"] = append(f.coll["/v1/credit-notes"], map[string]any{"id": path})
		w.WriteHeader(201)
		_, _ = w.Write([]byte("{}"))
	case strings.HasPrefix(path, "/v1/plans/") && strings.HasSuffix(path, "/versions"):
		f.creates["plan-version"]++
		code := strings.Split(path, "/")[3]
		for _, p := range f.coll["/v1/plans"] {
			if p["code"] == code {
				p["latest_version"] = float64(1)
			}
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte("{}"))
	default:
		key, ok := listKeys[path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if r.Method == http.MethodPost {
			f.creates[path]++
			if body == nil {
				body = map[string]any{}
			}
			if path == "/v1/credit-packs" || path == "/v1/payment-links" {
				body["id"] = path + "-id"
			}
			f.coll[path] = append(f.coll[path], body)
			w.WriteHeader(201)
			_, _ = w.Write([]byte("{}"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{key: f.coll[path], "next_page_token": ""})
	}
}

func TestTenantSeedIsIdempotent(t *testing.T) {
	fake := newFakeStack()
	srv := httptest.NewServer(fake)
	defer srv.Close()
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	s := &devSeeder{base: srv.URL, o: devSeedOptions{HTTP: srv.Client(), Out: io.Discard, Now: func() time.Time { return now }, PollWait: time.Millisecond, Timeout: 5 * time.Second}}
	run := func(app devApp) {
		ts := &tenantSeed{s: s, label: app.Name, tenant: "t1", session: "qs_x", app: app, appID: "a1"}
		if err := ts.seed(); err != nil {
			t.Fatal(err)
		}
	}
	app := devApp{Name: "Storefront", Currency: "TRY", Timezone: "Europe/Istanbul", Profile: "store"}
	run(app)
	first := map[string]int{}
	for k, v := range fake.creates {
		first[k] = v
	}
	if first["/v1/customers"] != 7 || first["/v1/expenses"] != 9 || first["close"] != 1 || first["credit-note"] != 2 || first["/v1/webhook-endpoints"] != 1 || first["/v1/payment-links"] < 2 {
		t.Fatalf("first run created %v", first)
	}
	if len(fake.events) == 0 {
		t.Fatal("no usage events were sent")
	}
	events := len(fake.events)

	run(app) // the second run must not create anything
	for k, v := range fake.creates {
		if v != first[k] {
			t.Errorf("second run created %d more of %s", v-first[k], k)
		}
	}
	if len(fake.events) != events {
		t.Errorf("second run added %d usage events", len(fake.events)-events)
	}
}

func TestExpensesMatchedByVendorNotAmount(t *testing.T) {
	fake := newFakeStack()
	fake.coll["/v1/expenses"] = []map[string]any{{"vendor": "Hetzner Cloud", "currency": "TRY", "amount_minor": "1"}} // edited by hand
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s := &devSeeder{base: srv.URL, o: devSeedOptions{HTTP: srv.Client(), Out: io.Discard, Now: time.Now, PollWait: time.Millisecond}}
	ts := &tenantSeed{s: s, label: "x", tenant: "t", session: "s", app: devApp{Currency: "TRY", Profile: "store"}}
	ts.data = dataFor("store", "TRY")
	if err := ts.expenses(); err != nil {
		t.Fatal(err)
	}
	if got := fake.creates["/v1/expenses"]; got != len(ts.data.Expenses)-1 {
		t.Fatalf("created %d expenses, want %d (the hand-edited one is left alone)", got, len(ts.data.Expenses)-1)
	}
}

func TestRateLimitIsWaitedOut(t *testing.T) {
	fake := newFakeStack()
	fake.limited = 3
	srv := httptest.NewServer(fake)
	defer srv.Close()
	s := &devSeeder{base: srv.URL, o: devSeedOptions{HTTP: srv.Client(), Out: io.Discard, Now: time.Now, PollWait: time.Millisecond}}
	var out map[string]any
	if err := s.do("GET", srv.URL, "/v1/meters", nil, nil, &out); err != nil {
		t.Fatalf("a 429 must be retried: %v", err)
	}
}

func TestVerifyEmailReadsTheMailAndSpendsTheLink(t *testing.T) {
	var verified string
	gw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/verify" {
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			verified = b["token"]
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer gw.Close()
	mail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/search"):
			if !strings.Contains(r.URL.RawQuery, "user1%40example.com") {
				t.Errorf("mail search %q does not name the address", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"messages":[{"ID":"m1"}]}`))
		case r.URL.Path == "/api/v1/message/m1":
			_, _ = w.Write([]byte(`{"Text":"Open http://localhost:42301/verify#token=qv_abcdefghijklmnopqrstuvwxyz0123456789_-AB to confirm","HTML":""}`))
		}
	}))
	defer mail.Close()
	s := &devSeeder{base: gw.URL, o: devSeedOptions{HTTP: gw.Client(), Out: io.Discard, Mailpit: mail.URL, PollWait: time.Millisecond}}
	if err := s.verifyEmail("user1@example.com", "qs_x"); err != nil {
		t.Fatal(err)
	}
	if verified != "qv_abcdefghijklmnopqrstuvwxyz0123456789_-AB" {
		t.Fatalf("spent token %q", verified)
	}
}
