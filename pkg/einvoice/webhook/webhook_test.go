package webhook_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/webhook"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
)

const secret = "s3cret-value"

func provider(t *testing.T, h http.HandlerFunc) (einvoice.Provider, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r := einvoice.NewRegistry()
	webhook.RegisterWithOptions(r, webhook.Options{AllowPrivate: true, Timeout: 2 * time.Second})
	p, err := r.Build("webhook", []byte(`{"url":"`+srv.URL+`/hook","secret":"`+secret+`","allow_private":true}`))
	if err != nil {
		t.Fatal(err)
	}
	return p, srv
}

func verify(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, _ := io.ReadAll(r.Body)
	if err := webhooksig.Verify(r.Header.Get(webhooksig.Header()), body, secret, time.Now(), 0); err != nil {
		t.Errorf("signature: %v", err)
	}
	return body
}

func doc() einvoice.Document {
	return einvoice.Document{Kind: einvoice.KindInvoice, Type: einvoice.TypePeppol, UUID: "uuid-1", Number: "INV-1"}
}

func TestIssueSignedAndParsed(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-x"))
	p, _ := provider(t, func(w http.ResponseWriter, r *http.Request) {
		body := verify(t, r)
		if string(body) != "<Invoice/>" || r.Header.Get("Content-Type") != "application/xml" || r.URL.Query().Get("action") != "issue" {
			t.Errorf("request: %q %s %s", body, r.Header.Get("Content-Type"), r.URL.RawQuery)
		}
		if r.Header.Get("Payrudder-Document-Uuid") != "uuid-1" || r.Header.Get("Payrudder-Document-Type") != "peppol" || r.Header.Get("Payrudder-Document-Kind") != "invoice" || r.Header.Get("Payrudder-Action") != "issue" {
			t.Errorf("headers: %v", r.Header)
		}
		_, _ = w.Write([]byte(`{"state":"issued","legal_number":"L1","external_id":"E1","pdf_base64":"` + pdf + `"}`))
	})
	res, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(), UBL: []byte("<Invoice/>")})
	if err != nil {
		t.Fatal(err)
	}
	if res.LegalNumber != "L1" || res.ExternalID != "E1" || res.State != einvoice.StateIssued || string(res.PDF) != "%PDF-x" || res.UUID != "uuid-1" {
		t.Errorf("%+v", res)
	}
}

func TestIssueErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"rejected", 200, `{"state":"rejected","message":"bad tax id"}`, true},
		{"unknown state", 200, `{"state":"huh"}`, true},
		{"issued without number", 200, `{"state":"issued"}`, true},
		{"not json", 200, `<html>`, true},
		{"400", 400, `{"message":"nope"}`, true},
		{"401", 401, ``, true},
		{"408", 408, ``, false},
		{"429", 429, ``, false},
		{"500", 500, ``, false},
		{"503", 503, ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _ := provider(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})
			_, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(), UBL: []byte("<x/>")})
			var pe *einvoice.PermanentError
			var te *einvoice.TransientError
			switch {
			case c.permanent && !errors.As(err, &pe):
				t.Errorf("want permanent, got %v", err)
			case !c.permanent && !errors.As(err, &te):
				t.Errorf("want transient, got %v", err)
			}
			if err != nil && strings.Contains(err.Error(), secret) {
				t.Error("secret in error")
			}
		})
	}
}

func TestNetworkErrorIsTransientAndHidesURL(t *testing.T) {
	p, srv := provider(t, func(http.ResponseWriter, *http.Request) {})
	srv.Close()
	_, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc()})
	var te *einvoice.TransientError
	if !errors.As(err, &te) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("%v", err)
	}
}

func TestActions(t *testing.T) {
	p, _ := provider(t, func(w http.ResponseWriter, r *http.Request) {
		body := verify(t, r)
		var in map[string]string
		_ = json.Unmarshal(body, &in)
		if in["uuid"] != "uuid-1" || in["external_id"] != "E1" || in["action"] != r.URL.Query().Get("action") {
			t.Errorf("body %s", body)
		}
		switch in["action"] {
		case "status":
			_, _ = w.Write([]byte(`{"state":"pending","legal_number":"","message":"queued"}`))
		case "cancel":
			if in["reason"] != "oops" {
				t.Error("reason lost")
			}
			_, _ = w.Write([]byte(`{"state":"cancelled"}`))
		case "pdf":
			_, _ = w.Write([]byte(`{"pdf_base64":"` + base64.StdEncoding.EncodeToString([]byte("PDF")) + `"}`))
		case "ubl":
			w.WriteHeader(http.StatusNotImplemented)
		}
	})
	ref := einvoice.Ref{ExternalID: "E1", UUID: "uuid-1", Type: einvoice.TypePeppol}
	st, err := p.Status(t.Context(), ref)
	if err != nil || st.State != einvoice.StatePending || st.Detail != "queued" {
		t.Errorf("%+v %v", st, err)
	}
	if err := p.Cancel(t.Context(), ref, "oops"); err != nil {
		t.Error(err)
	}
	if b, err := p.FetchPDF(t.Context(), ref); err != nil || string(b) != "PDF" {
		t.Errorf("%q %v", b, err)
	}
	if _, err := p.FetchUBL(t.Context(), ref); !errors.Is(err, einvoice.ErrUnsupported) {
		t.Errorf("%v", err)
	}
}

func TestSettingsValidation(t *testing.T) {
	r := einvoice.NewRegistry()
	webhook.Register(r)
	for name, s := range map[string]string{
		"not json":       `x`,
		"http":           `{"url":"http://example.com/h","secret":"longenough"}`,
		"private":        `{"url":"https://127.0.0.1/h","secret":"longenough"}`,
		"short secret":   `{"url":"https://example.com/h","secret":"x"}`,
		"private forced": `{"url":"http://127.0.0.1/h","secret":"longenough","allow_private":true}`, // the tenant cannot lift the check
	} {
		if _, err := r.Build("webhook", []byte(s)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := r.Build("webhook", []byte(`{"url":"https://example.com/h","secret":"longenough"}`)); err != nil {
		t.Error(err)
	}
}
