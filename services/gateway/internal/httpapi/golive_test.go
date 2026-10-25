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

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
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

func TestKeyScopeAndRotationRoutes(t *testing.T) {
	p := newProduct(t, nil, nil)
	for body, want := range map[string]plansv1.ApiKey_Scope{
		`{"environment":"sandbox"}`:                  plansv1.ApiKey_SCOPE_FULL,
		`{"environment":"sandbox","scope":"ingest"}`: plansv1.ApiKey_SCOPE_INGEST,
		`{"environment":"sandbox","scope":"read"}`:   plansv1.ApiKey_SCOPE_READ,
	} {
		if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", body); resp.StatusCode != 201 {
			t.Fatalf("%s: %d", body, resp.StatusCode)
		}
		if p.ident.lastScope != want {
			t.Errorf("%s: scope %v, want %v", body, p.ident.lastScope, want)
		}
	}
	if resp, _ := p.call(t, "POST", "/app/v1/apps/"+adaApp+"/api-keys", tokAda, "", `{"environment":"sandbox","scope":"root"}`); resp.StatusCode != 400 {
		t.Errorf("an unknown scope: %d", resp.StatusCode)
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
