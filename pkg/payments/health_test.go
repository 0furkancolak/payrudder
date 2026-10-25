package payments_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/banktransfer"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/payments/paddle"
)

func TestVerifyRefusesWhatAPersonMayNotConnect(t *testing.T) {
	ctx := context.Background()
	mk := func(url string) payments.Provider {
		c, err := paddle.New(paddle.Settings{APIKey: "k", BaseURL: url, WebhookSecret: "w", UsageProductID: "pro_1"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// An endpoint that is not one of the documented hosts would receive the secret key: refused before any request.
	if _, err := payments.Verify(ctx, mk("http://127.0.0.1:9"), "live"); !errors.Is(err, payments.ErrUnknownEndpoint) {
		t.Errorf("unknown host: %v", err)
	}
	// The provider's test host is not a live account.
	if _, err := payments.Verify(ctx, mk(paddle.SandboxURL), "live"); !errors.Is(err, payments.ErrLivemodeMismatch) {
		t.Errorf("test host as live: %v", err)
	}
	// Bank details point nowhere and have no call to make: they pass, and say nothing was asked remotely.
	bt, err := banktransfer.Factory([]byte(`{"bank_name":"Bank","account_holder":"Ada Labs","iban":"DE89370400440532013000","currencies":["EUR"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if h, err := payments.Verify(ctx, bt, "live"); err != nil || h.Remote {
		t.Errorf("bank transfer: %+v %v", h, err)
	}
}

func TestCheckClassifiesTheAnswer(t *testing.T) {
	ctx := context.Background()
	status := http.StatusOK
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	c, err := paddle.New(paddle.Settings{APIKey: "pdl_live_key", BaseURL: srv.URL, WebhookSecret: "w", UsageProductID: "pro_1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := payments.Check(ctx, c); err != nil || !h.Remote {
		t.Fatalf("a working account: %+v %v", h, err)
	}
	if path != "/event-types" || auth != "Bearer pdl_live_key" {
		t.Errorf("request %s %q", path, auth)
	}
	status = http.StatusForbidden
	if _, err := payments.Check(ctx, c); !errors.Is(err, payments.ErrBadCredentials) {
		t.Errorf("403 is the provider refusing the credentials: %v", err)
	}
	status = http.StatusBadGateway
	if _, err := payments.Check(ctx, c); !errors.Is(err, payments.ErrUnreachable) {
		t.Errorf("502 says nothing about the credentials: %v", err)
	}
	srv.Close()
	if _, err := payments.Check(ctx, c); !errors.Is(err, payments.ErrUnreachable) {
		t.Errorf("a closed port: %v", err)
	}
}

func TestIyzicoHealthIsASignedBinLookup(t *testing.T) {
	var path, auth, body string
	reply := `{"status":"success"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		path, auth, body = r.URL.Path, r.Header.Get("Authorization"), string(b)
		_, _ = io.WriteString(w, reply)
	}))
	defer srv.Close()
	c, err := iyzico.New(iyzico.Settings{APIKey: "api", SecretKey: "secret", BaseURL: srv.URL}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := payments.Check(context.Background(), c); err != nil {
		t.Fatalf("working account: %v", err)
	}
	if path != "/payment/bin/check" || auth == "" || body == "" {
		t.Errorf("request %s auth=%q body=%q", path, auth, body)
	}
	reply = `{"status":"failure","errorCode":"1000","errorMessage":"invalid signature"}`
	if _, err := payments.Check(context.Background(), c); !errors.Is(err, payments.ErrBadCredentials) {
		t.Errorf("a refused signature: %v", err)
	}
}
