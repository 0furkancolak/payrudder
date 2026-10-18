// Package psp is mock-psp: a small payment provider that behaves like a real one where it matters for testing the saga.
// Charges are idempotent by Idempotency-Key (the same key returns the original result), a charge can commit and then
// hang (the "did my timed-out charge go through?" case), and sandbox payment-method tokens script deterministic
// outcomes (docs/03-services/workers.md#payments-worker).
package psp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds the environment-wide failure knobs (used by chaos and load tests). Scenario tokens take precedence.
type Config struct {
	DeclineRate float64
	ErrorRate   float64
	// TimeoutRate is the share of charges that commit and then hang.
	TimeoutRate float64
	Latency     time.Duration
	// HangFor is how long a hanging request blocks (default 30 s, longer than payments-worker's client timeout).
	HangFor time.Duration
}

// Server is the HTTP API.
type Server struct {
	cfg  Config
	db   *pgxpool.Pool
	mux  *http.ServeMux
	rand func() float64
}

// New returns a Server over the database.
func New(db *pgxpool.Pool, cfg Config) *Server {
	if cfg.HangFor <= 0 {
		cfg.HangFor = 30 * time.Second
	}
	s := &Server{cfg: cfg, db: db, mux: http.NewServeMux(), rand: rand.Float64}
	s.mux.HandleFunc("POST /v1/charges", s.charge)
	s.mux.HandleFunc("GET /v1/charges", s.getCharges)
	s.mux.HandleFunc("POST /v1/refunds", s.refund)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

type chargeBody struct {
	InvoiceID     string `json:"invoice_id"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency"`
	PaymentMethod string `json:"payment_method"`
}

type stored struct {
	ID          string
	Status      string
	DeclineCode string
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func decline(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusPaymentRequired, map[string]any{"error": map[string]string{"code": code}})
}

func (s *Server) lookup(ctx context.Context, key string) (*stored, error) {
	var c stored
	var dc *string
	err := s.db.QueryRow(ctx, `UPDATE charges SET requests = requests + 1 WHERE idempotency_key = $1 RETURNING id, status, decline_code`, key).Scan(&c.ID, &c.Status, &dc)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if dc != nil {
		c.DeclineCode = *dc
	}
	return &c, nil
}

// attempt counts a request for a key that has no charge yet and returns its ordinal (1, 2, ...).
func (s *Server) attempt(ctx context.Context, key string) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `INSERT INTO attempts (idempotency_key, requests) VALUES ($1, 1)
		ON CONFLICT (idempotency_key) DO UPDATE SET requests = attempts.requests + 1 RETURNING requests`, key).Scan(&n)
	return n, err
}

func (s *Server) commit(ctx context.Context, key string, b chargeBody, status, declineCode string) (*stored, error) {
	id := "ch_" + uuid.NewString()
	var dc *string
	if declineCode != "" {
		dc = &declineCode
	}
	_, err := s.db.Exec(ctx, `INSERT INTO charges (idempotency_key, id, invoice_id, payment_method, amount_minor, currency, status, decline_code)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (idempotency_key) DO NOTHING`, key, id, b.InvoiceID, b.PaymentMethod, b.AmountMinor, b.Currency, status, dc)
	if err != nil {
		return nil, err
	}
	// A concurrent request with the same key may have won: the stored row is the truth.
	var out stored
	var d *string
	if err := s.db.QueryRow(ctx, `SELECT id, status, decline_code FROM charges WHERE idempotency_key = $1`, key).Scan(&out.ID, &out.Status, &d); err != nil {
		return nil, err
	}
	if d != nil {
		out.DeclineCode = *d
	}
	return &out, nil
}

func (s *Server) respond(w http.ResponseWriter, c *stored) {
	if c.Status == "declined" {
		decline(w, c.DeclineCode)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": c.ID, "status": c.Status})
}

// hang blocks like an unresponsive provider; the client's timeout ends the request.
func (s *Server) hang(w http.ResponseWriter, r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-time.After(s.cfg.HangFor):
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
	}
}

func (s *Server) charge(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		http.Error(w, "Idempotency-Key is required", http.StatusBadRequest)
		return
	}
	var b chargeBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.InvoiceID == "" || b.AmountMinor < 0 {
		http.Error(w, "invalid charge", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if s.cfg.Latency > 0 {
		time.Sleep(s.cfg.Latency)
	}
	// The same key returns the original result, like a real provider.
	if c, err := s.lookup(ctx, key); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	} else if c != nil {
		s.respond(w, c)
		return
	}
	n, err := s.attempt(ctx, key)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}

	token, scripted := strings.CutPrefix(b.PaymentMethod, "pm_sandbox_")
	if scripted {
		switch token {
		case "declined":
			s.finish(w, r, key, b, "declined", "card_declined")
		case "insufficient_funds_once":
			var earlier int
			_ = s.db.QueryRow(ctx, `SELECT count(*) FROM charges WHERE invoice_id = $1 AND status = 'declined'`, b.InvoiceID).Scan(&earlier)
			if earlier == 0 {
				s.finish(w, r, key, b, "declined", "insufficient_funds")
			} else {
				s.finish(w, r, key, b, "succeeded", "")
			}
		case "error_then_success":
			if n <= 2 {
				http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
				return
			}
			s.finish(w, r, key, b, "succeeded", "")
		case "timeout_committed":
			if _, err := s.commit(ctx, key, b, "succeeded", ""); err != nil {
				http.Error(w, "database error", http.StatusInternalServerError)
				return
			}
			s.hang(w, r)
		case "timeout_not_committed":
			if n == 1 {
				s.hang(w, r)
				return
			}
			s.finish(w, r, key, b, "succeeded", "")
		default: // pm_sandbox_success and unknown scenarios
			s.finish(w, r, key, b, "succeeded", "")
		}
		return
	}

	// Environment-wide knobs for tokens without a script.
	switch roll := s.rand(); {
	case roll < s.cfg.TimeoutRate:
		if _, err := s.commit(ctx, key, b, "succeeded", ""); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		s.hang(w, r)
	case roll < s.cfg.TimeoutRate+s.cfg.ErrorRate:
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	case roll < s.cfg.TimeoutRate+s.cfg.ErrorRate+s.cfg.DeclineRate:
		s.finish(w, r, key, b, "declined", "card_declined")
	default:
		s.finish(w, r, key, b, "succeeded", "")
	}
}

func (s *Server) finish(w http.ResponseWriter, r *http.Request, key string, b chargeBody, status, code string) {
	c, err := s.commit(r.Context(), key, b, status, code)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	s.respond(w, c)
}

// getCharges answers the status query (?idempotency_key=) and lists a customer's charges for tests (?invoice_id=).
func (s *Server) getCharges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if key := q.Get("idempotency_key"); key != "" {
		var id, status string
		var dc *string
		err := s.db.QueryRow(r.Context(), `SELECT id, status, decline_code FROM charges WHERE idempotency_key = $1`, key).Scan(&id, &status, &dc)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"found": false})
			return
		}
		if err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		out := map[string]any{"found": true, "id": id, "status": status}
		if dc != nil {
			out["decline_code"] = *dc
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.db.Query(r.Context(), `SELECT idempotency_key, id, status, amount_minor, requests FROM charges WHERE invoice_id = $1 ORDER BY created_at`, q.Get("invoice_id"))
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	type item struct {
		Key         string `json:"idempotency_key"`
		ID          string `json:"id"`
		Status      string `json:"status"`
		AmountMinor int64  `json:"amount_minor"`
		Requests    int    `json:"requests"`
	}
	items := []item{}
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.Key, &it.ID, &it.Status, &it.AmountMinor, &it.Requests); err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		items = append(items, it)
	}
	writeJSON(w, http.StatusOK, map[string]any{"charges": items})
}

func (s *Server) refund(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	var b struct {
		InvoiceID   string `json:"invoice_id"`
		ChargeID    string `json:"charge_id"`
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if key == "" || json.NewDecoder(r.Body).Decode(&b) != nil || b.InvoiceID == "" {
		http.Error(w, "invalid refund", http.StatusBadRequest)
		return
	}
	id := fmt.Sprintf("re_%s", uuid.NewString())
	if _, err := s.db.Exec(r.Context(), `INSERT INTO refunds (idempotency_key, id, invoice_id, charge_id, amount_minor, currency)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6) ON CONFLICT (idempotency_key) DO NOTHING`, key, id, b.InvoiceID, b.ChargeID, b.AmountMinor, b.Currency); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	var out string
	if err := s.db.QueryRow(r.Context(), `SELECT id FROM refunds WHERE idempotency_key = $1`, key).Scan(&out); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": out, "status": "succeeded"})
}
