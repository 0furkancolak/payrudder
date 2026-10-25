// Command mockoidc serves pkg/oidctest as a stand-in for Google in the local stack: "Continue with Google" works end to end without a Google client, an
// account or the internet. It is for development only (it signs in anyone who types an address).
//
//	MOCKOIDC_ADDR           listen address (default :8099)
//	MOCKOIDC_ISSUER         the address the gateway reaches it at, e.g. http://mockoidc:8099 (also PAYRUDDER_GOOGLE_ISSUER of the gateway)
//	MOCKOIDC_BROWSER_URL    the address the person's browser reaches it at, e.g. http://localhost:42899
//	MOCKOIDC_CLIENT_ID      the client id the gateway is configured with
//	MOCKOIDC_CLIENT_SECRET  the client secret the gateway is configured with
//	MOCKOIDC_REDIRECT_URI   the registered redirect address, e.g. http://localhost:42301/gw/auth/google/callback (comma separated for several)
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/oidctest"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	addr := env("MOCKOIDC_ADDR", ":8099")
	p := oidctest.New(env("MOCKOIDC_CLIENT_ID", "mock-client-id"), env("MOCKOIDC_CLIENT_SECRET", "mock-client-secret"),
		strings.Split(env("MOCKOIDC_REDIRECT_URI", "http://localhost:42301/gw/auth/google/callback"), ",")...)
	p.SetIssuer(env("MOCKOIDC_ISSUER", "http://localhost:42899"))
	p.BrowserURL = env("MOCKOIDC_BROWSER_URL", "")
	srv := &http.Server{Addr: addr, Handler: p, ReadHeaderTimeout: 5 * time.Second}
	fmt.Fprintf(os.Stderr, "mockoidc: listening on %s as %s\n", addr, env("MOCKOIDC_ISSUER", "http://localhost:42899"))
	log.Fatal(srv.ListenAndServe())
}
