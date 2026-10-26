package oidc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/services/gateway/internal/oidc"
)

// When the provider is down every sign-in that arrives while its configuration is being read waits for that read and then shares its answer. They used to
// queue on a mutex held during the HTTP call and each make the request again in turn: ten people signing in during an outage were ten sequential timeouts.
func TestSignInsDuringAProviderOutageShareOneRead(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(150 * time.Millisecond)
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	c := oidc.New(oidc.Config{Issuer: srv.URL, ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirect})

	const people = 6
	var wg sync.WaitGroup
	start := time.Now()
	for range people {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Begin(context.Background()); err == nil {
				t.Error("a sign-in succeeded against a provider that is down")
			}
		}()
	}
	wg.Wait()
	if n := hits.Load(); n > 2 {
		t.Errorf("the provider was asked %d times by %d simultaneous sign-ins, want one shared read", n, people)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("the sign-ins took %s: they waited for each other's timeouts", took)
	}
}
