// Package httpx is what the HTTP routes of payments-worker's modules share: the JSON and error-envelope writers, the path and query readers, the operator
// token check and the mapping of a command's refusal (refuse) to an answer.
package httpx

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/refuse"
)

// MaxBody is the most a request body may hold.
const MaxBody = 64 << 10

// ProviderTrouble is what a caller is told when a provider call failed; the cause is in the log.
const ProviderTrouble = "the payment provider could not complete the request"

// WriteJSON answers with v as JSON.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("response not written", "err", err) // the client went away
	}
}

// Fail answers with the keyed error envelope, the one the gateway writes ({"error": {"code", "key", "message"}}), so the console can translate the key.
// The code name follows the HTTP status; key is listed in docs/error-keys.md.
func Fail(w http.ResponseWriter, code int, key, msg string) {
	KeyFail(w, code, CodeName(code), key, msg)
}

// KeyFail is Fail with an API error code that does not follow from the status.
func KeyFail(w http.ResponseWriter, status int, code, key, msg string) {
	WriteJSON(w, status, map[string]any{"error": map[string]string{"code": code, "key": key, "message": msg}})
}

// CodeName is the API error code that goes with an HTTP status.
func CodeName(httpStatus int) string {
	switch httpStatus {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "FAILED_PRECONDITION"
	case http.StatusRequestEntityTooLarge:
		return "REQUEST_TOO_LARGE"
	case http.StatusNotImplemented:
		return "UNIMPLEMENTED"
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	default:
		return "INTERNAL"
	}
}

// DecodeBody reads the JSON body into v, or answers 400 and reports false.
func DecodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBody)).Decode(v); err != nil {
		Fail(w, http.StatusBadRequest, "payments.request.bad_body", "the body must be JSON: "+err.Error())
		return false
	}
	return true
}

// Tenant reads the {tenant} of the path, or answers 400 and reports false.
func Tenant(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	t, err := uuid.Parse(r.PathValue("tenant"))
	if err != nil {
		Fail(w, http.StatusBadRequest, "payments.request.bad_tenant", "tenant must be a UUID")
		return uuid.Nil, false
	}
	return t, true
}

// Environment reads ?environment= (sandbox or live), or answers 400 and reports false.
func Environment(w http.ResponseWriter, r *http.Request) (string, bool) {
	mode, err := env.Parse(r.URL.Query().Get("environment"))
	if err != nil {
		Fail(w, http.StatusBadRequest, "payments.request.bad_environment", "environment must be sandbox or live")
		return "", false
	}
	return mode.String(), true
}

// Guard returns the operator check: a request must carry the internal token. An empty token disables the endpoints entirely.
func Guard(token string) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Internal-Token")), []byte(token)) != 1 {
				Fail(w, http.StatusUnauthorized, errkey.Key("common.unauthenticated"), "unauthorized")
				return
			}
			next(w, r)
		}
	}
}

// Unsupported answers for a provider whose adapter lacks the interface an endpoint needs.
func Unsupported(w http.ResponseWriter, provider, what string) {
	Fail(w, http.StatusNotImplemented, "payments.provider.not_supported", provider+" does not support "+what)
}

// Reply answers for an error a command or query returned: a refusal as it says, a failure of another service relayed or hidden, anything else as an
// internal error, which is logged here because nothing else saw it.
func Reply(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	var (
		refusal *refuse.Error
		plans   *refuse.Plans
		billing *refuse.Billing
		prov    *refuse.Provider
	)
	switch {
	case errors.As(err, &refusal):
		Fail(w, refusal.Status, refusal.Key, refusal.Message)
	case errors.As(err, &plans):
		Relay(log, w, r, "plans-svc", errkey.Key("payments.plans.failed"), plans.Err)
	case errors.As(err, &billing):
		Relay(log, w, r, "billing-svc", errkey.Key("payments.billing.failed"), billing.Err)
	case errors.As(err, &prov):
		ProviderFail(log, w, r, prov.What, prov.Err)
	default:
		log.ErrorContext(r.Context(), "request failed", "path", r.URL.Path, "err", err)
		refusal = refuse.Internal()
		Fail(w, refusal.Status, refusal.Key, refusal.Message)
	}
}

// ProviderFail relays an error from a provider: a request it refused is the caller's to fix, anything else is a bad gateway.
func ProviderFail(log *slog.Logger, w http.ResponseWriter, r *http.Request, what string, err error) {
	log.WarnContext(r.Context(), what+" failed", "err", err)
	if errors.Is(err, payments.ErrUnsupported) {
		Fail(w, http.StatusConflict, "payments.provider.not_supported", err.Error())
		return
	}
	Fail(w, http.StatusBadGateway, "payments.provider.failed", ProviderTrouble)
}

// Relay answers for a refusal of another service. A refusal about the request (not found, invalid, wrong state) keeps its status, its key (the code's
// common.* default when it has none) and its message, which was written for the caller. Anything else is the other service's trouble: the cause goes to
// the log and the caller gets a bad gateway with a fixed message and failKey.
func Relay(log *slog.Logger, w http.ResponseWriter, r *http.Request, svc, failKey string, err error) {
	key := errkey.Of(err)
	if key == "" {
		key = errkey.Default(status.Code(err))
	}
	switch status.Code(err) {
	case codes.NotFound:
		Fail(w, http.StatusNotFound, key, status.Convert(err).Message())
	case codes.InvalidArgument:
		Fail(w, http.StatusBadRequest, key, status.Convert(err).Message())
	case codes.FailedPrecondition, codes.AlreadyExists, codes.Aborted:
		Fail(w, http.StatusConflict, key, status.Convert(err).Message())
	default:
		log.WarnContext(r.Context(), svc+" request failed", "err", err)
		Fail(w, http.StatusBadGateway, failKey, svc+" could not complete the request")
	}
}
