package conformance

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/brand"
	"github.com/0furkancolak/payrudder/pkg/einvoice/mock"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
)

// WebhookServer is a fake integrator that speaks the protocol of the generic webhook adapter (pkg/einvoice/webhook/doc.go) and so documents it by example: every
// request must carry a valid signature, issue is idempotent on the document UUID, and status, cancel, pdf and ubl answer for the documents it holds.
//
// It also injects the failures an integrator has: Fail makes every request answer an HTTP status, Delay holds each answer back, SetPending keeps new documents
// pending until Settle.
type WebhookServer struct {
	*httptest.Server
	// Secret signs the requests; the server refuses a request whose signature does not verify (401).
	Secret string

	mu       sync.Mutex
	docs     map[string]*whDoc
	requests int
	fail     int
	delay    time.Duration
	pending  bool
}

type whDoc struct {
	ubl       []byte
	legal     string
	state     string
	cancelled bool
}

// NewWebhookServer starts the fake. It stops with the test.
func NewWebhookServer(t testing.TB) *WebhookServer {
	s := &WebhookServer{Secret: "conformance-secret-key", docs: map[string]*whDoc{}} //nolint:gosec // a fake integrator's test secret
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Fail makes every request answer with the status (an error body that repeats the secret) until Fail(0).
func (s *WebhookServer) Fail(status int) { s.with(func() { s.fail = status }) }

// Delay holds every answer back by d.
func (s *WebhookServer) Delay(d time.Duration) { s.with(func() { s.delay = d }) }

// SetPending keeps documents issued from now on pending until Settle.
func (s *WebhookServer) SetPending(on bool) { s.with(func() { s.pending = on }) }

// Settle confirms every pending document: it becomes issued with a legal number.
func (s *WebhookServer) Settle() {
	s.with(func() {
		for _, d := range s.docs {
			if d.state == "pending" {
				d.state = "issued"
			}
		}
	})
}

// Created is the number of documents held.
func (s *WebhookServer) Created() (n int) { s.with(func() { n = len(s.docs) }); return n }

// Requests is the number of requests received.
func (s *WebhookServer) Requests() (n int) { s.with(func() { n = s.requests }); return n }

func (s *WebhookServer) with(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f()
}

type whAnswer struct {
	State       string `json:"state,omitempty"`
	LegalNumber string `json:"legal_number,omitempty"`
	ExternalID  string `json:"external_id,omitempty"`
	PDFBase64   string `json:"pdf_base64,omitempty"`
	UBLBase64   string `json:"ubl_base64,omitempty"`
	Message     string `json:"message,omitempty"`
}

func (s *WebhookServer) serve(w http.ResponseWriter, r *http.Request) {
	var fail int
	var delay time.Duration
	s.with(func() { s.requests++; fail, delay = s.fail, s.delay })
	if delay > 0 {
		time.Sleep(delay)
	}
	body, _ := io.ReadAll(r.Body)
	if err := webhooksig.Verify(r.Header.Get(webhooksig.Header()), body, s.Secret, time.Now(), 0); err != nil {
		reply(w, http.StatusUnauthorized, whAnswer{Message: "bad signature"})
		return
	}
	if fail != 0 {
		reply(w, fail, whAnswer{Message: "refused; the key " + s.Secret + " is not accepted"})
		return
	}
	action := r.URL.Query().Get("action")
	if action != r.Header.Get(brand.Header("Action")) {
		reply(w, http.StatusBadRequest, whAnswer{Message: "the action header and query disagree"})
		return
	}
	if action == "issue" {
		s.issue(w, r, body)
		return
	}
	var req struct {
		UUID string `json:"uuid"`
	}
	if json.Unmarshal(body, &req) != nil || req.UUID == "" {
		reply(w, http.StatusBadRequest, whAnswer{Message: "uuid is required"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[req.UUID]
	if !ok {
		reply(w, http.StatusNotFound, whAnswer{Message: "unknown document"})
		return
	}
	switch action {
	case "status":
		reply(w, http.StatusOK, whAnswer{State: d.current(), LegalNumber: d.number()})
	case "cancel":
		d.cancelled = true
		reply(w, http.StatusOK, whAnswer{State: "cancelled"})
	case "pdf":
		reply(w, http.StatusOK, whAnswer{PDFBase64: base64.StdEncoding.EncodeToString(mock.PlaceholderPDF(d.legal))})
	case "ubl":
		reply(w, http.StatusOK, whAnswer{UBLBase64: base64.StdEncoding.EncodeToString(d.ubl)})
	default:
		reply(w, http.StatusNotImplemented, whAnswer{Message: "unknown action"})
	}
}

func (s *WebhookServer) issue(w http.ResponseWriter, r *http.Request, body []byte) {
	id := r.Header.Get(brand.Header("Document-Uuid"))
	if id == "" {
		reply(w, http.StatusBadRequest, whAnswer{Message: "the document uuid header is required"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.docs[id]
	if !ok {
		d = &whDoc{ubl: append([]byte(nil), body...), legal: legalNumber(id), state: "issued"}
		if s.pending {
			d.state = "pending"
		}
		s.docs[id] = d
	}
	reply(w, http.StatusOK, whAnswer{State: d.current(), LegalNumber: d.number(), ExternalID: "wh-" + id, PDFBase64: base64.StdEncoding.EncodeToString(mock.PlaceholderPDF(d.legal))})
}

func (d *whDoc) current() string {
	if d.cancelled {
		return "cancelled"
	}
	return d.state
}

// number is the legal number once the document is issued; a pending document has none.
func (d *whDoc) number() string {
	if d.current() == "pending" {
		return ""
	}
	return d.legal
}

func legalNumber(uuid string) string {
	sum := sha256.Sum256([]byte(uuid))
	return fmt.Sprintf("WHK2026%09d", binary.BigEndian.Uint64(sum[:8])%1_000_000_000)
}

func reply(w http.ResponseWriter, status int, a whAnswer) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(a)
}
