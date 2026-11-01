package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"strings"
)

// Failing over to another account is dangerous in one way only: charging twice. The rule that keeps it safe (docs/provider-health.md):
//
//	a charge may be tried at a second account only when the first provider DEFINITELY did not process it, and then as a new attempt under a new key.
//
// "Definitely" is narrow on purpose. A timeout is never definite (the provider may have charged and be slow to say), and neither is an HTTP 5xx: a gateway can answer 502
// after the provider behind it has charged. What an adapter can prove is that the request never left: the connection to the provider was not opened. An adapter or a
// provider that has a documented signal for "refused before processing" returns a *NotProcessedError itself.

// NotProcessedError wraps an error to say that the provider definitely did not process the request. It is the only thing that allows a failover.
type NotProcessedError struct{ Err error }

func (e *NotProcessedError) Error() string { return "request not processed: " + e.Err.Error() }
func (e *NotProcessedError) Unwrap() error { return e.Err }

// ErrCircuitOpen: the account's circuit breaker is open, so the provider was not asked. It is a *TransientError (retried through the delay queues) and says nothing
// about the charge: it was never sent.
var ErrCircuitOpen = errors.New("payments: the provider account's circuit is open; the provider was not called")

// NotProcessed reports whether err proves the provider did not process the request: an error that says so (*NotProcessedError), or a transport failure in which the
// connection was never opened (a dial or DNS error), or an open circuit (the provider was not called). Anything else is not proof. A timeout is an ErrTimeout and never reaches this test as a failure to dial, because
// the adapters turn every timeout into ErrTimeout first.
func NotProcessed(err error) bool {
	if errors.Is(err, ErrCircuitOpen) {
		return true // the provider was not called at all
	}
	if err == nil || errors.Is(err, ErrTimeout) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var np *NotProcessedError
	if errors.As(err, &np) {
		return true
	}
	var tr *TransientError
	if !errors.As(err, &tr) {
		return false
	}
	var op *net.OpError
	if errors.As(tr.Err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(tr.Err, &dns)
}

// IsOutage reports whether err is the kind of failure that counts against an account's health: the provider could not be reached, did not answer in time, or
// answered with a server error. A decline, a refusal of the request and a pending payment are the provider working.
func IsOutage(err error) bool {
	if err == nil {
		return false
	}
	var tr *TransientError
	return errors.Is(err, ErrTimeout) || errors.Is(err, ErrUnreachable) || errors.As(err, &tr)
}

const failoverMark = "~fo~"

// FailoverKey is the idempotency key of the attempt at the second account: new (the first provider may know the original key), and a function of the original and the
// account, so that a command delivered again goes to the same second attempt and an idempotent provider returns its first answer instead of charging twice.
func FailoverKey(key, account string) string {
	h := sha256.Sum256([]byte(account))
	return key + failoverMark + hex.EncodeToString(h[:6])
}

// FailoverPrefix is what every failover key of key starts with, to find an attempt that failed over without knowing where to.
func FailoverPrefix(key string) string { return key + failoverMark }

// IsFailoverKey reports whether key is a failover key.
func IsFailoverKey(key string) bool { return strings.Contains(key, failoverMark) }
