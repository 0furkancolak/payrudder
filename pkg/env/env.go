// Package env is the one place that parses, compares and requires an environment (phase 45, ADR 0011 and ADR 0022).
//
// A tenant is a sandbox or a live one. The tenant stays the unit of isolation, so nothing in a table says "environment"; what keeps the two from being
// confused is that every edge asserts it: a request, a command or an event whose environment is missing, or disagrees with its tenant's, is refused
// (fail closed). The string literals "sandbox" and "live" are spelled here and nowhere else.
package env

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

// Env is the environment of a tenant.
type Env string

const (
	// Sandbox is the tenant that talks to mock-psp, numbers invoices SBX- and mails only to the sandbox sink.
	Sandbox Env = "sandbox"
	// Live is the tenant that moves real money.
	Live Env = "live"
)

var (
	// ErrMissing: no environment, or one that is neither sandbox nor live.
	ErrMissing = errors.New("env: the environment is missing")
	// ErrMismatch: two environments that must be the same are not.
	ErrMismatch = errors.New("env: the environment does not match the tenant's")
)

// Parse reads "sandbox" or "live" exactly. Anything else, the empty string included, is ErrMissing: there is no default environment.
func Parse(s string) (Env, error) {
	switch Env(s) {
	case Sandbox, Live:
		return Env(s), nil
	}
	return "", fmt.Errorf("%w: %q", ErrMissing, s)
}

// MustParse is Parse for a constant the caller knows to be valid; it panics otherwise.
func MustParse(s string) Env {
	e, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return e
}

// String is the stored and wire spelling.
func (e Env) String() string { return string(e) }

// Valid reports whether e is sandbox or live.
func (e Env) Valid() bool { return e == Sandbox || e == Live }

// IsSandbox reports whether e is the sandbox.
func (e Env) IsSandbox() bool { return e == Sandbox }

// IsLive reports whether e is live.
func (e Env) IsLive() bool { return e == Live }

// Proto is the API enum; the zero value for an environment that is not valid.
func (e Env) Proto() plansv1.Environment {
	switch e {
	case Sandbox:
		return plansv1.Environment_ENVIRONMENT_SANDBOX
	case Live:
		return plansv1.Environment_ENVIRONMENT_LIVE
	}
	return plansv1.Environment_ENVIRONMENT_UNSPECIFIED
}

// FromProto reads the API enum. UNSPECIFIED is ErrMissing, never "live" by default.
func FromProto(e plansv1.Environment) (Env, error) {
	switch e {
	case plansv1.Environment_ENVIRONMENT_SANDBOX:
		return Sandbox, nil
	case plansv1.Environment_ENVIRONMENT_LIVE:
		return Live, nil
	case plansv1.Environment_ENVIRONMENT_UNSPECIFIED:
	}
	return "", fmt.Errorf("%w: unspecified", ErrMissing)
}

// KeyEnv is the environment an API key prefix names: qk_test_ is the sandbox, qk_live_ is live.
func KeyEnv(k apikey.Env) (Env, error) {
	switch k {
	case apikey.EnvTest:
		return Sandbox, nil
	case apikey.EnvLive:
		return Live, nil
	}
	return "", fmt.Errorf("%w: key environment %q", ErrMissing, k)
}

// ForKey is the key prefix environment of e; "" for an environment that is not valid.
func (e Env) ForKey() apikey.Env {
	switch e {
	case Sandbox:
		return apikey.EnvTest
	case Live:
		return apikey.EnvLive
	}
	return ""
}

// Require returns e if it is valid and ErrMissing if not.
func Require(e Env) (Env, error) {
	if !e.Valid() {
		return "", fmt.Errorf("%w: %q", ErrMissing, string(e))
	}
	return e, nil
}

// Same fails closed: both must be valid and equal. what names the two things being compared, for the message.
func Same(a, b Env, what string) error {
	if _, err := Require(a); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if _, err := Require(b); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if a != b {
		return fmt.Errorf("%s: %w (%s, %s)", what, ErrMismatch, a, b)
	}
	return nil
}

// SameProto is Same for a command's enum and a tenant's stored spelling.
func SameProto(cmd plansv1.Environment, tenant Env, what string) error {
	c, err := FromProto(cmd)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return Same(c, tenant, what)
}

// ParseList reads a comma-separated QUOTIFY_ALLOWED_ENVIRONMENTS. An empty or unknown entry is an error.
func ParseList(s string) ([]Env, error) {
	var out []Env
	for _, part := range strings.Split(s, ",") {
		e, err := Parse(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, ErrMissing
	}
	return out, nil
}

// Allowed reports whether e is one of the environments a deployment serves.
func Allowed(list []string, e Env) bool { return slices.Contains(list, string(e)) }

// Refuse turns ErrMissing and ErrMismatch into the API error a client sees; any other error is returned as it is.
func Refuse(err error) error {
	switch {
	case errors.Is(err, ErrMissing):
		return errkey.New(codes.FailedPrecondition, "env.request.missing", err.Error())
	case errors.Is(err, ErrMismatch):
		return errkey.New(codes.FailedPrecondition, "env.request.mismatch", err.Error())
	}
	return err
}
