// Package fakenafru is a stand-in for Nafru Email for tests: a Resend-compatible server (POST /emails, GET /domains) that records what it was sent and can
// post delivery events to the notifier's mail-event endpoint, signed like a real translating layer would.
//
// It is built from Resend's public API reference, NOT from Nafru's documentation, which Payrudder has not seen: whether a real Nafru behaves like this is
// UNVERIFIED (see mailx.Nafru). It exists so that the code around the sender (suppression, quota, rate limit) is tested against the shape the adapter speaks.
package fakenafru

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/0furkancolak/payrudder/pkg/mailx"
	"github.com/0furkancolak/payrudder/pkg/webhooksig"
)

// Sent is one message the server accepted.
type Sent struct {
	ID             string
	IdempotencyKey string
	Auth           string
	From           string
	To             []string
	Subject        string
	Tags           map[string]string
	Headers        map[string]string
}

// Server is the fake. Create it with New and close it with Close.
type Server struct {
	*httptest.Server
	// APIKey, when set, is the only key accepted (Bearer); others get 401.
	APIKey string

	mu     sync.Mutex
	sent   []Sent
	byKey  map[string]string
	status int // when not 0, POST /emails answers with it (a refusal or an outage)
}

// New starts a fake.
func New() *Server {
	s := &Server{byKey: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /emails", s.send)
	mux.HandleFunc("GET /domains", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorised(w, r) {
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	s.Server = httptest.NewServer(mux)
	return s
}

func (s *Server) authorised(w http.ResponseWriter, r *http.Request) bool {
	if s.APIKey != "" && r.Header.Get("Authorization") != "Bearer "+s.APIKey {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"API key is invalid"}`))
		return false
	}
	return true
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(w, r) {
		return
	}
	var body struct {
		From    string            `json:"from"`
		To      []string          `json:"to"`
		Subject string            `json:"subject"`
		Headers map[string]string `json:"headers"`
		Tags    []struct{ Name, Value string }
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.From == "" || len(body.To) == 0 || body.Subject == "" {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Missing required field"}`))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status != 0 {
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(`{"message":"refused"}`))
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if id, ok := s.byKey[key]; ok && key != "" { // Resend: the same key answers with the first message's id
		_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
		return
	}
	id := fmt.Sprintf("fake-%d", len(s.sent)+1)
	m := Sent{ID: id, IdempotencyKey: key, Auth: r.Header.Get("Authorization"), From: body.From, To: body.To, Subject: body.Subject, Headers: body.Headers, Tags: map[string]string{}}
	for _, t := range body.Tags {
		m.Tags[t.Name] = t.Value
	}
	s.sent = append(s.sent, m)
	if key != "" {
		s.byKey[key] = id
	}
	_, _ = fmt.Fprintf(w, `{"id":%q}`, id)
}

// Sent returns the messages accepted so far, oldest first.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// Fail makes POST /emails answer with the status (0 stops it).
func (s *Server) Fail(status int) {
	s.mu.Lock()
	s.status = status
	s.mu.Unlock()
}

// Emit posts the event to the notifier's mail-event endpoint (the base URL of its listener), signed with secret, and returns the HTTP status.
func Emit(ctx context.Context, baseURL, secret string, e mailx.Event) (int, error) {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	body, err := json.Marshal(e)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/mail-events", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(webhooksig.Header(), webhooksig.SignRequest(time.Now(), http.MethodPost, req.URL, body, secret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// ResendWebhook is the body of a Resend webhook for a message: typ is the provider's event type ("email.bounced", "email.complained", "email.delivered",
// "email.delivery_delayed"), bounceType the bounce's type ("Permanent" or "Temporary"; ignored for other types). Built from the examples on Resend's pages for the events
// (read 2026-11-02); whether Nafru's webhooks look like this is UNVERIFIED.
func ResendWebhook(typ, emailID, to, bounceType string) []byte {
	data := map[string]any{"email_id": emailID, "to": []string{to}, "from": "Acme <billing@acme.example>", "subject": "Your invoice"}
	if typ == "email.bounced" {
		data["bounce"] = map[string]string{"type": bounceType, "subType": "General", "message": "550 5.1.1 mailbox unavailable"}
	}
	b, _ := json.Marshal(map[string]any{"type": typ, "created_at": time.Now().UTC().Format(time.RFC3339), "data": data})
	return b
}

// PostSigned posts a webhook body to url the way the provider does: signed with Svix (svix-id, svix-timestamp, svix-signature) with secret ("whsec_..."). It returns the HTTP
// status.
func PostSigned(ctx context.Context, url, secret string, body []byte) (int, error) {
	id := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	now := time.Now()
	sig, err := mailx.SvixSign(secret, id, now, body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", fmt.Sprint(now.Unix()))
	req.Header.Set("svix-signature", sig)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}
