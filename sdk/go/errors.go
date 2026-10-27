package payrudder

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrorKind classifies an Error. Use it with errors.Is and the Err* sentinels, or read Error.Kind.
type ErrorKind int

// The kinds of Error. Only KindConnection has no HTTP status.
const (
	// KindAPI is any other error answer, for example 409 or 413.
	KindAPI ErrorKind = iota
	// KindAuthentication is 401: the API key is missing, malformed, revoked or expired.
	KindAuthentication
	// KindPermission is 403: the key lacks the permission, or the tenant is not active.
	KindPermission
	// KindNotFound is 404.
	KindNotFound
	// KindValidation is 400 or 422.
	KindValidation
	// KindConflict is 409.
	KindConflict
	// KindRateLimit is 429.
	KindRateLimit
	// KindServer is 5xx.
	KindServer
	// KindConnection is a transport failure or a timeout: no answer was read.
	KindConnection
)

func (k ErrorKind) String() string {
	switch k {
	case KindAuthentication:
		return "authentication"
	case KindPermission:
		return "permission"
	case KindNotFound:
		return "not_found"
	case KindValidation:
		return "validation"
	case KindConflict:
		return "conflict"
	case KindRateLimit:
		return "rate_limit"
	case KindServer:
		return "server"
	case KindConnection:
		return "connection"
	default:
		return "api"
	}
}

// ErrorDetail points at one field of a rejected request.
type ErrorDetail struct {
	Field       string `json:"field"`
	Description string `json:"description"`
}

// Error is every failure of a call. Use errors.As to reach it:
//
//	var perr *payrudder.Error
//	if errors.As(err, &perr) && perr.Key == "billing.credit_note.nothing_to_refund" { ... }
type Error struct {
	// Kind classifies the failure; see the Err* sentinels.
	Kind ErrorKind
	// Status is the HTTP status, 0 for a connection failure or timeout.
	Status int
	// Code is the gRPC-style code of the envelope, such as INVALID_ARGUMENT.
	Code string
	// Key is the stable, translatable error key, such as billing.credit_note.nothing_to_refund. Branch on this, not on
	// Message.
	Key string
	// Message is a human-readable explanation, not stable.
	Message string
	// Details lists the offending fields of a validation error.
	Details []ErrorDetail
	// RequestID is the request id of the call, from the envelope or the X-Request-Id header.
	RequestID string
	// RetryAfter is the delay the server asked for (429, 503), zero when it did not.
	RetryAfter time.Duration
	// Timeout is true when a connection failure was a deadline.
	Timeout bool

	cause    error
	sentinel bool
}

// Sentinels for errors.Is(err, payrudder.ErrNotFound) and the like.
var (
	ErrAuthentication = &Error{Kind: KindAuthentication, sentinel: true}
	ErrPermission     = &Error{Kind: KindPermission, sentinel: true}
	ErrNotFound       = &Error{Kind: KindNotFound, sentinel: true}
	ErrValidation     = &Error{Kind: KindValidation, sentinel: true}
	ErrConflict       = &Error{Kind: KindConflict, sentinel: true}
	ErrRateLimit      = &Error{Kind: KindRateLimit, sentinel: true}
	ErrServer         = &Error{Kind: KindServer, sentinel: true}
	ErrConnection     = &Error{Kind: KindConnection, sentinel: true}
)

// Error implements error.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("payrudder: ")
	if e.Status != 0 {
		b.WriteString(strconv.Itoa(e.Status))
		b.WriteByte(' ')
	}
	switch {
	case e.Key != "":
		b.WriteString(e.Key)
	case e.Code != "":
		b.WriteString(e.Code)
	default:
		b.WriteString(e.Kind.String())
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" (request ")
		b.WriteString(e.RequestID)
		b.WriteByte(')')
	}
	return b.String()
}

// Unwrap returns the transport error of a connection failure.
func (e *Error) Unwrap() error { return e.cause }

// Is matches the Err* sentinels by kind.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.sentinel && t.Kind == e.Kind
}

func kindOf(status int) ErrorKind {
	switch {
	case status == http.StatusUnauthorized:
		return KindAuthentication
	case status == http.StatusForbidden:
		return KindPermission
	case status == http.StatusNotFound:
		return KindNotFound
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		return KindValidation
	case status == http.StatusConflict:
		return KindConflict
	case status == http.StatusTooManyRequests:
		return KindRateLimit
	case status >= 500:
		return KindServer
	default:
		return KindAPI
	}
}

// newAPIError builds the error of a non-2xx answer from its body (the Error envelope) and headers.
func newAPIError(status int, header http.Header, body []byte) *Error {
	e := &Error{Kind: kindOf(status), Status: status, RequestID: header.Get("X-Request-Id")}
	var env struct {
		Error struct {
			Code      string        `json:"code"`
			Key       string        `json:"key"`
			Message   string        `json:"message"`
			Details   []ErrorDetail `json:"details"`
			RequestID string        `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && (env.Error.Code != "" || env.Error.Message != "" || env.Error.Key != "") {
		e.Code, e.Key, e.Message, e.Details = env.Error.Code, env.Error.Key, env.Error.Message, env.Error.Details
		if env.Error.RequestID != "" {
			e.RequestID = env.Error.RequestID
		}
	} else if t := strings.TrimSpace(string(body)); t != "" {
		// Not the envelope: a proxy in front of the gateway, say. Keep a bounded excerpt.
		if len(t) > 200 {
			t = t[:200]
		}
		e.Message = t
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	e.RetryAfter = parseRetryAfter(header.Get("Retry-After"))
	return e
}

// parseRetryAfter reads the delay in seconds the gateway sends. An HTTP date is not used by the API; it reads as 0.
func parseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 0
}

func newConnectionError(err error) *Error {
	timeout := errors.Is(err, context.DeadlineExceeded)
	var te interface{ Timeout() bool }
	if errors.As(err, &te) && te.Timeout() {
		timeout = true
	}
	msg := err.Error()
	if timeout {
		msg = "request timed out: " + msg
	}
	return &Error{Kind: KindConnection, Message: msg, Timeout: timeout, cause: err}
}

// errNoAPIKey is returned by New when no key is given and PAYRUDDER_API_KEY is unset.
var errNoAPIKey = errors.New("payrudder: an API key is required: use WithAPIKey or set PAYRUDDER_API_KEY")
