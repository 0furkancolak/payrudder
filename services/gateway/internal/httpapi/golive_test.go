package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withPayments points the gateway at a fake payments-worker whose go-live answer is ready(); it counts how often it was asked.
func withPayments(ready func() bool, asked *atomic.Int32) func(*Deps) {
	return func(d *Deps) {
		pw := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/go-live") {
				http.NotFound(w, r)
				return
			}
			asked.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"ready": ready(), "providers": []string{"iyzico"}})
		}))
		u, _ := url.Parse(pw.URL)
		d.PaymentsURL = u
	}
}

// What the browser sends never decides the provider item: the gateway asks payments-worker itself and tells plans-svc, and an unreachable
// payments-worker is a "no", not a pass.
func TestGoLiveProviderItemComesFromPaymentsWorkerNotTheBrowser(t *testing.T) {
	var asked atomic.Int32
	ready := false
	p := newProduct(t, nil, withPayments(func() bool { return ready }, &asked))

	resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live", tokAda, "", "")
	if resp.StatusCode != 200 || m["go_live"] == nil {
		t.Fatalf("go-live: %d %v", resp.StatusCode, m)
	}
	if got := p.ident.goLive[0]; got.GetProviderReady() || got.GetUserId() != adaUser || got.GetAppId() != adaApp {
		t.Errorf("request = %v: the provider is not ready yet, and the user comes from the session", got)
	}

	// A body that claims readiness changes nothing.
	resp, _ = p.call(t, "POST", "/app/v1/apps/"+adaApp+"/go-live", tokAda, "", `{"accept_terms":true,"provider_ready":true}`)
	if resp.StatusCode != 200 {
		t.Fatalf("activate: %d", resp.StatusCode)
	}
	if got := p.ident.activates[0]; got.GetProviderReady() || !got.GetAcceptTerms() {
		t.Errorf("activation = %v, want terms accepted and the provider not ready", got)
	}

	ready = true
	p.call(t, "POST", "/app/v1/apps/"+adaApp+"/go-live", tokAda, "", `{}`)
	if got := p.ident.activates[1]; !got.GetProviderReady() || got.GetAcceptTerms() {
		t.Errorf("activation = %v, want the provider ready (payments-worker said so) and no terms", got)
	}
	if asked.Load() != 3 {
		t.Errorf("payments-worker asked %d times, want once per call", asked.Load())
	}
}

func TestGoLiveWithoutPaymentsWorkerIsNeverReady(t *testing.T) {
	p := newProduct(t, nil, nil)
	p.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live", tokAda, "", "")
	if p.ident.goLive[0].GetProviderReady() {
		t.Error("a deployment without payments-worker reported a ready provider")
	}
}

func TestGoLiveIsTheMembersAndOwnersOnly(t *testing.T) {
	var asked atomic.Int32
	p := newProduct(t, nil, withPayments(func() bool { return true }, &asked))
	if resp, _ := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live", "", "", ""); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
	if resp, m := p.call(t, "GET", "/app/v1/apps/"+adaApp+"/go-live", tokBob, "", ""); resp.StatusCode != 404 || errKeyOf(m) != "identity.app.not_found" {
		t.Errorf("another account's app: %d %v", resp.StatusCode, m)
	}
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/go-live", tokViewer, "", `{}`); resp.StatusCode != 403 || errKeyOf(m) != "identity.go_live.owner_only" {
		t.Errorf("a viewer activates: %d %v", resp.StatusCode, m)
	}
	if asked.Load() != 0 {
		t.Errorf("payments-worker was asked %d times on behalf of a person with no access", asked.Load())
	}
}

func TestKeyRoleAndRotationRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	for body, want := range map[string]string{
		`{"environment":"sandbox"}`:                                                     "admin",
		`{"environment":"sandbox","role":"billing"}`:                                    "billing",
		`{"environment":"sandbox","scope":"ingest"}`:                                    "ingest_only",
		`{"environment":"sandbox","scope":"read"}`:                                      "read_only",
		`{"environment":"sandbox","permissions":["customers:read","invoices:read"]}`:    "custom",
		`{"environment":"sandbox","role":"custom","permissions":["customers:read"]}`:    "custom",
		`{"environment":"sandbox","role":"read_only","permissions":["customers:read"]}`: "read_only",
	} {
		if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", body); resp.StatusCode != 201 {
			t.Fatalf("%s: %d", body, resp.StatusCode)
		}
		if p.ident.lastRole != want && (want != "custom" || len(p.ident.lastPerms) == 0) {
			t.Errorf("%s: role %q, want %q", body, p.ident.lastRole, want)
		}
	}
	for _, bad := range []string{
		`{"environment":"sandbox","scope":"root"}`,
		`{"environment":"sandbox","role":"root"}`,
		`{"environment":"sandbox","role":"custom"}`,
		`{"environment":"sandbox","role":"custom","permissions":["nope:read"]}`,
		`{"environment":"sandbox","role":"read_only","permissions":["customers:write"]}`,
	} {
		if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", bad); resp.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", bad, resp.StatusCode)
		}
	}

	resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys/key-ada/rotate", tokAda, "", `{"overlap_hours":2}`)
	if resp.StatusCode != 201 || m["plaintext"] == nil || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("rotate: %d %v (a secret must not be cached)", resp.StatusCode, m)
	}
	if got := p.ident.rotates[0]; got.GetOverlap().AsDuration() != 2*time.Hour || got.GetApiKeyId() != "key-ada" || got.GetUserId() != adaUser {
		t.Errorf("rotation request = %v", got)
	}
	p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys/key-ada/rotate", tokAda, "", ``)
	if p.ident.rotates[1].GetOverlap() != nil {
		t.Errorf("no overlap given must reach plans-svc as unset (its default), got %v", p.ident.rotates[1].GetOverlap())
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys/key-ada/rotate", tokAda, "", `{"overlap_hours":500}`); resp.StatusCode != 400 {
		t.Errorf("an overlap of 500 hours: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+bobApp+"/api-keys/key-ada/rotate", tokAda, "", ``); resp.StatusCode != 404 {
		t.Errorf("another account's app: %d", resp.StatusCode)
	}
}

// Promotion takes the app from the path and the person from the session; the body carries only the mode and the hash of the preview. The answer holds a secret, so
// it is not cached.
func TestPromoteRoute(t *testing.T) {
	p := newProduct(t, nil, nil)
	resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/promote", tokAda, "", `{"dry_run":true,"tenant_id":"`+bobLive+`","user_id":"`+bobUser+`"}`)
	if resp.StatusCode != 200 || m["plan_hash"] != "h1" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("promote: %d %v", resp.StatusCode, m)
	}
	got := p.ident.promotes[0]
	if !got.GetDryRun() || got.GetUserId() != adaUser || got.GetAppId() != adaApp {
		t.Errorf("request = %v: the user comes from the session and the app from the path", got)
	}
	p.call(t, "POST", "/app/v1/apps/"+adaApp+"/promote", tokAda, "", `{"plan_hash":"h1"}`)
	if got := p.ident.promotes[1]; got.GetDryRun() || got.GetPlanHash() != "h1" {
		t.Errorf("apply request = %v", got)
	}
	if resp, m := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/promote", tokViewer, "", `{}`); resp.StatusCode != 403 || errKeyOf(m) != "identity.account.forbidden" {
		t.Errorf("a viewer: %d %v", resp.StatusCode, m)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+bobApp+"/promote", tokAda, "", `{}`); resp.StatusCode != 404 {
		t.Errorf("another account's app: %d", resp.StatusCode)
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/promote", "", "", `{}`); resp.StatusCode != 401 {
		t.Errorf("without a session: %d", resp.StatusCode)
	}
}
