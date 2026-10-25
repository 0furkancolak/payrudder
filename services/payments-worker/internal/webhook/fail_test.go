package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/quotify/pkg/errkey"
)

type envelope struct {
	Error struct {
		Code    string `json:"code"`
		Key     string `json:"key"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestFailWritesTheKeyedEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	fail(rec, http.StatusNotFound, "payments.account.not_found", "no such account")
	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound || e.Error.Code != "NOT_FOUND" || e.Error.Key != "payments.account.not_found" || e.Error.Message != "no such account" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestPlanFailKeepsTheUpstreamKey(t *testing.T) {
	rec := httptest.NewRecorder()
	planFail(rec, errkey.New(codes.FailedPrecondition, "plans.subscription.provider_currency", "no"))
	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusConflict || e.Error.Key != "plans.subscription.provider_currency" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	planFail(rec, status.Error(codes.NotFound, "missing"))
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || rec.Code != http.StatusNotFound || e.Error.Key != "common.not_found" {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
}
