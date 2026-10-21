package oidc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0furkancolak/quotify/pkg/oidctest"
	"github.com/0furkancolak/quotify/services/gateway/internal/oidc"
)

const (
	clientID     = "client-1"
	clientSecret = "secret-1"
	redirect     = "http://console.test/gw/auth/google/callback"
)

type rig struct {
	idp    *oidctest.Provider
	srv    *httptest.Server
	client *oidc.Client
	hits   *atomic.Int64 // requests the provider received for its key set
}

func newRig(t *testing.T, now func() time.Time) *rig {
	t.Helper()
	r := &rig{idp: oidctest.New(clientID, clientSecret, redirect), hits: &atomic.Int64{}}
	r.idp.Account = &oidctest.Account{Email: "ada@example.com", Subject: "sub-1", Name: "Ada"}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/jwks" {
			r.hits.Add(1)
		}
		r.idp.ServeHTTP(w, req)
	}))
	t.Cleanup(r.srv.Close)
	r.idp.SetIssuer(r.srv.URL)
	r.client = oidc.New(oidc.Config{Issuer: r.srv.URL, ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirect, Now: now})
	return r
}

// login runs the browser's part against the provider and returns the code, with the request's verifier and nonce.
func (r *rig) login(t *testing.T) (code string, req oidc.Request) {
	t.Helper()
	req, err := r.client.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(req.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("state") != req.State {
		t.Fatalf("the provider did not return the state: %v %v", loc, err)
	}
	return loc.Query().Get("code"), req
}

func TestExchangeReturnsVerifiedClaims(t *testing.T) {
	r := newRig(t, nil)
	code, req := r.login(t)
	got, err := r.client.Exchange(context.Background(), code, req.Verifier, req.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "sub-1" || got.Email != "ada@example.com" || !got.EmailVerified || got.Name != "Ada" {
		t.Fatalf("claims = %+v", got)
	}
}

func TestBeginUsesPKCEStateAndNonce(t *testing.T) {
	r := newRig(t, nil)
	a, err := r.client.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := r.client.Begin(context.Background())
	if a.State == b.State || a.Nonce == b.Nonce || a.Verifier == b.Verifier || len(a.Verifier) < 43 {
		t.Fatal("every login needs its own state, nonce and a long verifier")
	}
	u, _ := url.Parse(a.URL)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == a.Verifier || q.Get("code_challenge") == "" {
		t.Fatalf("the verifier must travel only as its S256 challenge: %v", q)
	}
}

func TestAWrongVerifierOrSecretIsRefusedByTheProvider(t *testing.T) {
	r := newRig(t, nil)
	code, req := r.login(t)
	if _, err := r.client.Exchange(context.Background(), code, "not-the-verifier-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", req.Nonce); err == nil {
		t.Fatal("a wrong PKCE verifier was accepted")
	}
	code, req = r.login(t)
	bad := oidc.New(oidc.Config{Issuer: r.srv.URL, ClientID: clientID, ClientSecret: "wrong", RedirectURL: redirect})
	if _, err := bad.Exchange(context.Background(), code, req.Verifier, req.Nonce); err == nil {
		t.Fatal("a wrong client secret was accepted")
	}
}

func TestADifferentIssuerInTheDiscoveryDocumentIsRefused(t *testing.T) {
	r := newRig(t, nil)
	r.idp.SetIssuer("https://somewhere-else.example")
	if _, err := r.client.Begin(context.Background()); err == nil {
		t.Fatal("a discovery document naming another issuer was accepted")
	}
}

func TestAnExpiredTokenIsRefusedAndASmallSkewIsNot(t *testing.T) {
	// The client's clock is ahead of the provider's by 30 seconds (inside the skew) and then by two hours (outside it).
	ahead := time.Now().Add(30 * time.Second)
	r := newRig(t, func() time.Time { return ahead })
	code, req := r.login(t)
	if _, err := r.client.Exchange(context.Background(), code, req.Verifier, req.Nonce); err != nil {
		t.Fatalf("a clock 30 seconds ahead must be tolerated: %v", err)
	}
	late := time.Now().Add(2 * time.Hour)
	r2 := newRig(t, func() time.Time { return late })
	code, req = r2.login(t)
	if _, err := r2.client.Exchange(context.Background(), code, req.Verifier, req.Nonce); err == nil {
		t.Fatal("a token that expired two hours ago was accepted")
	}
}

func TestTheKeySetIsCachedAndAForgedKeyIdDoesNotCauseAStreamOfRequests(t *testing.T) {
	r := newRig(t, nil)
	code, req := r.login(t)
	if _, err := r.client.Exchange(context.Background(), code, req.Verifier, req.Nonce); err != nil {
		t.Fatal(err)
	}
	if r.hits.Load() != 1 {
		t.Fatalf("one login reads the key set once, got %d", r.hits.Load())
	}
	for i := 0; i < 3; i++ {
		code, req = r.login(t)
		if _, err := r.client.Exchange(context.Background(), code, req.Verifier, req.Nonce); err != nil {
			t.Fatal(err)
		}
	}
	if r.hits.Load() != 1 {
		t.Fatalf("the key set is cached: %d reads after four logins", r.hits.Load())
	}
	// Tokens naming a key id the provider does not publish: refused, and the key set is read at most once more within the minute.
	forged := "eyJhbGciOiJSUzI1NiIsImtpZCI6Im5vLXN1Y2gta2V5In0." + "eyJzdWIiOiJ4In0" + "." + strings.Repeat("A", 342)
	for i := 0; i < 10; i++ {
		if _, err := r.client.Verify(context.Background(), forged, "n"); err == nil {
			t.Fatal("a token signed by an unknown key was accepted")
		}
	}
	if r.hits.Load() > 2 {
		t.Fatalf("forged key ids made %d key-set reads", r.hits.Load())
	}
}

func TestAnUnconfiguredClientIsDisabled(t *testing.T) {
	var none *oidc.Client
	if none.Enabled() || oidc.New(oidc.Config{Issuer: oidc.GoogleIssuer}).Enabled() {
		t.Fatal("a client without id and secret must be disabled")
	}
	if !oidc.New(oidc.Config{Issuer: oidc.GoogleIssuer, ClientID: "a", ClientSecret: "b"}).Enabled() {
		t.Fatal("a configured client is enabled")
	}
}
