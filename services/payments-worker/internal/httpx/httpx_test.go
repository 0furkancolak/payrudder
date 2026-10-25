package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
)

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Key     string `json:"key"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return e
}

func planFail(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	Reply(log, w, r, &refuse.Plans{Err: err})
}

func billingFail(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	Reply(log, w, r, &refuse.Billing{Err: err})
}

func TestFailWritesTheKeyedEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	Fail(rec, http.StatusNotFound, "payments.account.not_found", "no such account")
	e := decode(t, rec)
	if rec.Code != http.StatusNotFound || e.Error.Code != "NOT_FOUND" || e.Error.Key != "payments.account.not_found" || e.Error.Message != "no such account" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestPlanFailKeepsTheUpstreamKey(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	rec := httptest.NewRecorder()
	planFail(log, rec, r, errkey.New(codes.FailedPrecondition, "plans.subscription.provider_currency", "no"))
	if e := decode(t, rec); rec.Code != http.StatusConflict || e.Error.Key != "plans.subscription.provider_currency" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	planFail(log, rec, r, status.Error(codes.NotFound, "missing"))
	if e := decode(t, rec); rec.Code != http.StatusNotFound || e.Error.Key != "common.not_found" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

// A refusal that is the other service's trouble is logged, not relayed: no cause, no address, and no wrong service name.
func TestRelayHidesTheCauseOfAnUpstreamFailure(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	cause := status.Error(codes.Internal, "pq: password authentication failed for user billing at 10.0.0.9")

	rec := httptest.NewRecorder()
	billingFail(log, rec, r, cause)
	e := decode(t, rec)
	if rec.Code != http.StatusBadGateway || e.Error.Key != "payments.billing.failed" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(e.Error.Message, "10.0.0.9") || strings.Contains(e.Error.Message, "plans-svc") || !strings.HasPrefix(e.Error.Message, "billing-svc") {
		t.Fatalf("message = %q", e.Error.Message)
	}
	if !strings.Contains(logs.String(), "10.0.0.9") {
		t.Fatalf("the cause is not in the log: %s", logs.String())
	}

	rec = httptest.NewRecorder()
	planFail(log, rec, r, errors.New("dial tcp 10.0.0.7:9090: connection refused"))
	if e := decode(t, rec); rec.Code != http.StatusBadGateway || e.Error.Key != "payments.plans.failed" || strings.Contains(e.Error.Message, "10.0.0.7") {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestProviderFailHidesTheProviderText(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rec := httptest.NewRecorder()
	ProviderFail(log, rec, httptest.NewRequest(http.MethodGet, "/", nil), "ensure customer", errors.New(`paddle: 500 {"secret":"pdl_live_abc"}`))
	e := decode(t, rec)
	if rec.Code != http.StatusBadGateway || e.Error.Key != "payments.provider.failed" || strings.Contains(e.Error.Message, "pdl_live") {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "pdl_live_abc") {
		t.Fatalf("the cause is not in the log: %s", logs.String())
	}
}

// An error nobody classified is an internal error to the caller, with the cause in the log.
func TestReplyHidesAnUnclassifiedError(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	rec := httptest.NewRecorder()
	Reply(log, rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("pq: connection to 10.0.0.5 lost"))
	e := decode(t, rec)
	if rec.Code != http.StatusInternalServerError || e.Error.Key != "common.internal" || strings.Contains(e.Error.Message, "10.0.0.5") {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if !strings.Contains(logs.String(), "10.0.0.5") {
		t.Fatalf("the cause is not in the log: %s", logs.String())
	}
}

func TestGuardRefusesWithoutTheToken(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	for name, tc := range map[string]struct {
		token, header string
		want          int
	}{
		"right token": {"secret", "secret", http.StatusNoContent},
		"wrong token": {"secret", "other", http.StatusUnauthorized},
		"no header":   {"secret", "", http.StatusUnauthorized},
		"disabled":    {"", "", http.StatusUnauthorized},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("X-Internal-Token", tc.header)
		rec := httptest.NewRecorder()
		Guard(tc.token)(ok)(rec, r)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
	}
}
