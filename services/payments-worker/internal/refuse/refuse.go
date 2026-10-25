// Package refuse is how a command or query of the operator API says no. The routes of a module turn the error into the HTTP answer (httpx.Reply), so the
// rules stay out of the handlers and the handlers stay out of the rules.
package refuse

import (
	"net/http"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// Error is a refusal the caller is told about: the HTTP status, the stable key (docs/error-keys.md) and the English message of the answer.
type Error struct {
	Status  int
	Key     string
	Message string
}

func (e *Error) Error() string { return e.Key + ": " + e.Message }

// New is a refusal with the status, key and message.
func New(status int, key, msg string) *Error { return &Error{Status: status, Key: key, Message: msg} }

// Internal is the answer to a failure of ours that the caller cannot act on; the cause is logged where it happened.
func Internal() *Error {
	return New(http.StatusInternalServerError, errkey.Key("common.internal"), "internal error")
}

// Plans wraps a refusal or failure of plans-svc; the answer relays it (httpx.Relay).
type Plans struct{ Err error }

func (e *Plans) Error() string { return "plans-svc: " + e.Err.Error() }
func (e *Plans) Unwrap() error { return e.Err }

// Billing wraps a refusal or failure of billing-svc; the answer relays it.
type Billing struct{ Err error }

func (e *Billing) Error() string { return "billing-svc: " + e.Err.Error() }
func (e *Billing) Unwrap() error { return e.Err }

// Provider wraps a failure of a payment provider while doing What. The cause is logged and never shown.
type Provider struct {
	What string
	Err  error
}

func (e *Provider) Error() string { return e.What + ": " + e.Err.Error() }
func (e *Provider) Unwrap() error { return e.Err }
