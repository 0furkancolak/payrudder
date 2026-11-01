package nilvera_test

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/einvoice"
	"github.com/0furkancolak/payrudder/pkg/einvoice/conformance"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/einvoice/nilvera"
)

// fake is a Nilvera that replays ONLY the shapes the adapter's own tests replay, which are the ones read from Nilvera's public reference in 2026-10: the upload
// answer {UUID, InvoiceNumber}, the two status shapes, the 409 on a repeated UUID, raw bytes for /pdf and /xml. Anything the adapter marks UNVERIFIED stays
// unverified here: this proves the adapter honours the contract against those shapes, not that Nilvera answers so.
type fake struct {
	srv *httptest.Server

	mu       sync.Mutex
	docs     map[string]*nvDoc
	requests int
	fail     int
	pending  bool
}

type nvDoc struct {
	typ       string // "einvoice" or "earchive"
	xml       []byte
	number    *string
	succeeded bool
	cancelled bool
}

func newFake(t testing.TB) *fake {
	f := &fake{docs: map[string]*nvDoc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) with(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }

func (f *fake) serve(w http.ResponseWriter, r *http.Request) {
	var fail int
	f.with(func() { f.requests++; fail = f.fail })
	if r.Header.Get("Authorization") != "Bearer "+key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if fail != 0 {
		w.WriteHeader(fail)
		_, _ = w.Write([]byte(`{"Message":"the key ` + key + ` is not accepted"}`))
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodPost && (r.URL.Path == "/einvoice/Send/Xml" || r.URL.Path == "/earchive/Send/Xml"):
		f.upload(w, r, parts[0])
	case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "Status":
		f.status(w, parts)
	case r.Method == http.MethodGet && len(parts) == 4 && parts[3] == "Details":
		f.details(w, parts[2])
	case r.Method == http.MethodGet && len(parts) == 4 && (parts[3] == "pdf" || parts[3] == "xml"):
		f.download(w, parts)
	case r.Method == http.MethodPut && len(parts) == 4 && parts[0] == "earchive" && parts[3] == "Cancel":
		f.cancel(w, parts[2])
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fake) upload(w http.ResponseWriter, r *http.Request, family string) {
	mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/form-data" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	part, err := multipart.NewReader(r.Body, params["boundary"]).NextPart()
	if err != nil || part.FormName() != "file" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	id := strings.TrimSuffix(part.FileName(), ".xml")
	body, _ := io.ReadAll(part)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.docs[id]; dup {
		w.WriteHeader(http.StatusConflict)
		return
	}
	d := &nvDoc{typ: family, xml: body, succeeded: !f.pending}
	// e-fatura answers with its number at once; e-arsiv is pending without one (the adapter's tests replay both). Details has the number either way.
	if family == "einvoice" && !f.pending {
		n := "NLV2026" + id[:8]
		d.number = &n
	}
	f.docs[id] = d
	_ = json.NewEncoder(w).Encode(map[string]any{"UUID": id, "InvoiceNumber": d.number})
}

func (f *fake) status(w http.ResponseWriter, parts []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[parts[2]]
	code := "waiting"
	if ok && d.succeeded {
		code = "succeed"
	}
	switch {
	case !ok:
		w.WriteHeader(http.StatusNotFound)
	case parts[0] == "einvoice":
		_ = json.NewEncoder(w).Encode(map[string]any{"InvoiceStatus": map[string]any{"Code": code}, "Answer": map[string]any{"AnswerCode": "approved"}})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"StatusCode": code, "CancelStatus": d.cancelled, "StatusDetail": nil})
	}
}

// details answers GET .../{UUID}/Details: the invoice number once the document has succeeded, null before.
func (f *fake) details(w http.ResponseWriter, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var n *string
	if d.succeeded {
		v := "NLV2026" + id[:8]
		n = &v
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"UUID": id, "InvoiceNumber": n})
}

func (f *fake) download(w http.ResponseWriter, parts []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[parts[2]]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if parts[3] == "pdf" {
		_, _ = w.Write(mock.PlaceholderPDF("NLV"))
		return
	}
	_, _ = w.Write(d.xml)
}

func (f *fake) cancel(w http.ResponseWriter, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.docs[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	d.cancelled = true
}

func TestConformance(t *testing.T) {
	var cur *fake
	conformance.Run(t, conformance.Harness{
		New: func(t testing.TB) einvoice.Provider {
			cur = newFake(t)
			r := einvoice.NewRegistry()
			nilvera.RegisterWithOptions(r, nilvera.Options{AllowPrivate: true, Timeout: 2 * time.Second})
			p, err := r.Build("nilvera", []byte(`{"api_key":"`+key+`","mode":"test","base_url":"`+cur.srv.URL+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
		Secret:     key,
		Fail:       func(s int) { cur.with(func() { cur.fail = s }) },
		SetPending: func(on bool) { cur.with(func() { cur.pending = on }) },
		Settle: func() {
			cur.with(func() {
				for _, d := range cur.docs {
					d.succeeded = true
				}
			})
		},
		Created:  func() (n int) { cur.with(func() { n = len(cur.docs) }); return n },
		Requests: func() (n int) { cur.with(func() { n = cur.requests }); return n },
	})
}
