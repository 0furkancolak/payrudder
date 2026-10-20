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

	"github.com/0furkancolak/quotify/tools/mockpsp/internal/store/sqlc"
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
	q    *sqlc.Queries
	mux  *http.ServeMux
	rand func() float64
}

// New returns a Server over the database.
func New(db *pgxpool.Pool, cfg Config) *Server {
	if cfg.HangFor <= 0 {
		cfg.HangFor = 30 * time.Second
	}
	s := &Server{cfg: cfg, q: sqlc.New(db), mux: http.NewServeMux(), rand: rand.Float64}
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

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Server) lookup(ctx context.Context, key string) (*stored, error) {
	row, err := s.q.BumpCharge(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &stored{ID: row.ID, Status: row.Status, DeclineCode: deref(row.DeclineCode)}, nil
}

// attempt counts a request for a key that has no charge yet and returns its ordinal (1, 2, ...).
func (s *Server) attempt(ctx context.Context, key string) (int, error) {
	n, err := s.q.CountAttempt(ctx, key)
	return int(n), err
}

func (s *Server) commit(ctx context.Context, key string, b chargeBody, status, declineCode string) (*stored, error) {
	id := "ch_" + uuid.NewString()
	var dc *string
	if declineCode != "" {
		dc = &declineCode
	}
	err := s.q.InsertCharge(ctx, sqlc.InsertChargeParams{IdempotencyKey: key, ID: id, InvoiceID: b.InvoiceID, PaymentMethod: b.PaymentMethod,
		AmountMinor: b.AmountMinor, Currency: b.Currency, Status: status, DeclineCode: dc})
	if err != nil {
		return nil, err
	}
	// A concurrent request with the same key may have won: the stored row is the truth.
	row, err := s.q.GetCharge(ctx, key)
	if err != nil {
		return nil, err
	}
	return &stored{ID: row.ID, Status: row.Status, DeclineCode: deref(row.DeclineCode)}, nil
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
			earlier, _ := s.q.CountDeclinedCharges(ctx, b.InvoiceID)
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
		row, err := s.q.GetCharge(r.Context(), key)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]any{"found": false})
			return
		}
		if err != nil {
			http.Error(w, "database error", http.StatusInternalServerError)
			return
		}
		out := map[string]any{"found": true, "id": row.ID, "status": row.Status}
		if row.DeclineCode != nil {
			out["decline_code"] = *row.DeclineCode
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.q.ListInvoiceCharges(r.Context(), q.Get("invoice_id"))
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	type item struct {
		Key         string `json:"idempotency_key"`
		ID          string `json:"id"`
		Status      string `json:"status"`
		AmountMinor int64  `json:"amount_minor"`
		Requests    int32  `json:"requests"`
	}
	items := make([]item, 0, len(rows))
	for _, c := range rows {
		items = append(items, item{Key: c.IdempotencyKey, ID: c.ID, Status: c.Status, AmountMinor: c.AmountMinor, Requests: c.Requests})
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
	ctx := r.Context()
	if err := s.q.InsertRefund(ctx, sqlc.InsertRefundParams{IdempotencyKey: key, ID: id, InvoiceID: b.InvoiceID, ChargeID: b.ChargeID,
		AmountMinor: b.AmountMinor, Currency: b.Currency}); err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	out, err := s.q.GetRefundID(ctx, key)
	if err != nil {
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": out, "status": "succeeded"})
}
