package nilvera_test

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/nilvera"
)

const key = "nv-super-secret-key"

func setup(t *testing.T, h http.HandlerFunc) einvoice.Provider {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	r := einvoice.NewRegistry()
	nilvera.RegisterWithOptions(r, nilvera.Options{AllowPrivate: true, Timeout: 2 * time.Second})
	p, err := r.Build("nilvera", []byte(`{"api_key":"`+key+`","mode":"test","base_url":"`+srv.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func doc(typ einvoice.DocumentType) einvoice.Document {
	return einvoice.Document{Kind: einvoice.KindInvoice, Type: typ, UUID: "11111111-2222-3333-4444-555555555555", Number: "INV-1", Buyer: einvoice.Party{Alias: "urn:mail:buyer@x"}}
}

func checkAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("Authorization") != "Bearer "+key {
		t.Errorf("auth header %q", r.Header.Get("Authorization"))
	}
}

func upload(t *testing.T, r *http.Request) string {
	t.Helper()
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		t.Fatalf("content type %q", r.Header.Get("Content-Type"))
	}
	part, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
	if err != nil || part.FormName() != "file" {
		t.Fatalf("part: %v", err)
	}
	b, _ := io.ReadAll(part)
	return string(b)
}

func TestIssueEFatura(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		if r.Method != "POST" || r.URL.Path != "/einvoice/Send/Xml" || r.URL.Query().Get("Alias") != "urn:mail:buyer@x" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		if upload(t, r) != "<Invoice/>" {
			t.Error("ubl not uploaded")
		}
		_, _ = w.Write([]byte(`{"UUID":"11111111-2222-3333-4444-555555555555","InvoiceNumber":"ABC2026000000001"}`))
	})
	res, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(einvoice.TypeEFatura), UBL: []byte("<Invoice/>")})
	if err != nil {
		t.Fatal(err)
	}
	if res.LegalNumber != "ABC2026000000001" || res.State != einvoice.StateIssued || res.ExternalID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("%+v", res)
	}
}

func TestIssueEArsivPendingWithoutNumber(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/earchive/Send/Xml" || r.URL.Query().Get("Alias") != "" {
			t.Errorf("%s", r.URL)
		}
		_, _ = w.Write([]byte(`{"UUID":"x","InvoiceNumber":null}`))
	})
	res, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(einvoice.TypeEArsiv), UBL: []byte("<x/>")})
	if err != nil || res.State != einvoice.StatePending || res.LegalNumber != "" {
		t.Errorf("%+v %v", res, err)
	}
}

func TestDuplicateFetchesStatus(t *testing.T) {
	var sends int
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			sends++
			w.WriteHeader(http.StatusConflict)
			return
		}
		if r.URL.Path != "/einvoice/Sale/11111111-2222-3333-4444-555555555555/Status" {
			t.Errorf("%s", r.URL)
		}
		_, _ = w.Write([]byte(`{"InvoiceStatus":{"Code":"succeed"},"Answer":{"AnswerCode":"approved"}}`))
	})
	res, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(einvoice.TypeEFatura), UBL: []byte("<x/>")})
	if err != nil || res.State != einvoice.StateIssued || sends != 1 {
		t.Errorf("%+v %v sends=%d", res, err, sends)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, c := range []struct {
		status    int
		permanent bool
	}{{400, true}, {401, true}, {403, true}, {404, true}, {422, true}, {408, false}, {429, false}, {500, false}, {503, false}} {
		p := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"Message":"invalid tax number, key ` + key + ` echoed"}`))
		})
		_, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(einvoice.TypeEFatura), UBL: []byte("<x/>")})
		var pe *einvoice.PermanentError
		var te *einvoice.TransientError
		if c.permanent != errors.As(err, &pe) || !c.permanent != errors.As(err, &te) {
			t.Errorf("%d: %v", c.status, err)
		}
		if err != nil && strings.Contains(err.Error(), key) {
			t.Errorf("%d: key leaked: %v", c.status, err)
		}
		if pe != nil && !strings.Contains(pe.Message, "invalid tax number") {
			t.Errorf("%d: message %q", c.status, pe.Message)
		}
	}
}

func TestLongMessageTruncated(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(422)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	})
	_, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(einvoice.TypeEArsiv)})
	var pe *einvoice.PermanentError
	if !errors.As(err, &pe) || len(pe.Message) > 300 {
		t.Errorf("%v", err)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		typ  einvoice.DocumentType
		body string
		want einvoice.State
	}{
		{einvoice.TypeEFatura, `{"InvoiceStatus":{"Code":"waiting"},"Answer":{"AnswerCode":"unknown"}}`, einvoice.StatePending},
		{einvoice.TypeEFatura, `{"InvoiceStatus":{"Code":"succeed"},"Answer":{"AnswerCode":"rejected"}}`, einvoice.StateRejected},
		{einvoice.TypeEFatura, `{"InvoiceStatus":{"Code":"error","DetailDescription":"bad"},"Answer":{}}`, einvoice.StateRejected},
		{einvoice.TypeEArsiv, `{"StatusCode":"succeed","ReportStatus":"Reported","CancelStatus":false}`, einvoice.StateIssued},
		{einvoice.TypeEArsiv, `{"StatusCode":"succeed","CancelStatus":true}`, einvoice.StateCancelled},
	}
	for _, c := range cases {
		p := setup(t, func(w http.ResponseWriter, r *http.Request) {
			checkAuth(t, r)
			_, _ = w.Write([]byte(c.body))
		})
		st, err := p.Status(t.Context(), einvoice.Ref{UUID: "u", Type: c.typ})
		if err != nil || st.State != c.want {
			t.Errorf("%s: %+v %v", c.body, st, err)
		}
	}
	p := setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	if _, err := p.Status(t.Context(), einvoice.Ref{UUID: "u", Type: einvoice.TypeEArsiv}); !errors.Is(err, einvoice.ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestPDFAndUBL(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		checkAuth(t, r)
		switch {
		case strings.HasSuffix(r.URL.Path, "/pdf"):
			_, _ = w.Write([]byte("%PDF-1.4"))
		case strings.HasSuffix(r.URL.Path, "/xml"):
			_, _ = w.Write([]byte("<Invoice/>"))
		}
	})
	ref := einvoice.Ref{UUID: "u", Type: einvoice.TypeEFatura}
	if b, err := p.FetchPDF(t.Context(), ref); err != nil || string(b) != "%PDF-1.4" {
		t.Errorf("%q %v", b, err)
	}
	if b, err := p.FetchUBL(t.Context(), ref); err != nil || string(b) != "<Invoice/>" {
		t.Errorf("%q %v", b, err)
	}
}

func TestCancel(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/earchive/Invoices/u/Cancel" {
			t.Errorf("%s %s", r.Method, r.URL)
		}
		_, _ = w.Write([]byte(`[]`))
	})
	if err := p.Cancel(t.Context(), einvoice.Ref{UUID: "u", Type: einvoice.TypeEArsiv}, "x"); err != nil {
		t.Error(err)
	}
	if err := p.Cancel(t.Context(), einvoice.Ref{UUID: "u", Type: einvoice.TypeEFatura}, "x"); !errors.Is(err, einvoice.ErrUnsupported) {
		t.Errorf("%v", err)
	}
}

func TestBuyerCheck(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/general/GlobalCompany/Check/TaxNumber/") || r.URL.Query().Get("globalUserType") != "Invoice" {
			t.Errorf("%s", r.URL)
		}
		if strings.HasSuffix(r.URL.Path, "/111") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`[{"TaxNumber":"222","Name":"urn:mail:pk@acme.example","Title":"Acme"}]`))
	})
	bc := p.(einvoice.BuyerChecker)
	if ok, alias, err := bc.IsEInvoiceUser(t.Context(), "222"); err != nil || !ok || alias != "urn:mail:pk@acme.example" {
		t.Errorf("%v %q %v", ok, alias, err)
	}
	if ok, _, err := bc.IsEInvoiceUser(t.Context(), "111"); err != nil || ok {
		t.Errorf("%v %v", ok, err)
	}
}

func TestSettings(t *testing.T) {
	r := einvoice.NewRegistry()
	nilvera.Register(r)
	for name, s := range map[string]string{
		"no key":        `{"mode":"test"}`,
		"bad mode":      `{"api_key":"k","mode":"staging"}`,
		"http override": `{"api_key":"k","base_url":"http://example.com"}`,
		"private":       `{"api_key":"k","base_url":"https://10.0.0.1"}`,
		"not json":      `nope`,
	} {
		if _, err := r.Build("nilvera", []byte(s)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := r.Build("nilvera", []byte(`{"api_key":"k","mode":"production"}`)); err != nil {
		t.Error(err)
	}
}

func TestSandboxAndPeppolRefused(t *testing.T) {
	p := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not call") })
	d := doc(einvoice.TypeEFatura)
	d.Sandbox = true
	var pe *einvoice.PermanentError
	if _, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: d}); !errors.As(err, &pe) {
		t.Error(err)
	}
	if _, err := p.Issue(t.Context(), einvoice.IssueRequest{Doc: doc(einvoice.TypePeppol)}); !errors.As(err, &pe) {
		t.Error(err)
	}
	if c := p.Capabilities(); !c.NeedsBuyerCheck || c.Supports(einvoice.TypePeppol) || c.Countries[0] != "TR" {
		t.Error(c)
	}
}
