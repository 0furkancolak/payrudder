// Package errkey gives an error a stable key next to its gRPC code and message. A client (the console, an SDK) translates the key; the message is English
// for people reading logs. A key is lower-case and dotted, service first: "billing.credit_note.not_paid". Keys are listed in docs/error-keys.md.
package errkey

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Domain is the ErrorInfo domain every key travels under.
const Domain = "payrudder"

// MetaKey is the ErrorInfo metadata entry that holds the key.
const MetaKey = "key"

// New is a status error of code c with message msg and the key.
func New(c codes.Code, key, msg string) error {
	st := status.New(c, msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Domain: Domain, Reason: "", Metadata: map[string]string{MetaKey: key}}); err == nil {
		st = d
	}
	return st.Err()
}

// Key declares a key that travels outside a gRPC status (in a worker's reply, or in an HTTP body a service writes itself). It returns k, and exists so that
// the key is found by the test that lists docs/error-keys.md.
func Key(k string) string { return k }

// Newf is New with a formatted message.
func Newf(c codes.Code, key, format string, args ...any) error {
	return New(c, key, fmt.Sprintf(format, args...))
}

// ParseUUID reads a UUID from a request field. A malformed one is InvalidArgument with the key and the message "<field> must be a UUID". The key is
// a literal at the call, as for New, so that the test which lists docs/error-keys.md finds it.
func ParseUUID(key, field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, New(codes.InvalidArgument, key, field+" must be a UUID")
	}
	return id, nil
}

// Of returns the key of err, or "" if it has none.
func Of(err error) string {
	var se interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &se) {
		return ""
	}
	for _, d := range se.GRPCStatus().Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetDomain() == Domain {
			return ei.GetMetadata()[MetaKey]
		}
	}
	return ""
}

// Default is the key of an error that has none: "common." and the code in lower snake case.
func Default(c codes.Code) string {
	switch c {
	case codes.InvalidArgument:
		return "common.invalid_argument"
	case codes.NotFound:
		return "common.not_found"
	case codes.AlreadyExists:
		return "common.already_exists"
	case codes.FailedPrecondition:
		return "common.failed_precondition"
	case codes.PermissionDenied:
		return "common.permission_denied"
	case codes.Unauthenticated:
		return "common.unauthenticated"
	case codes.ResourceExhausted:
		return "common.rate_limited"
	case codes.Unavailable:
		return "common.unavailable"
	case codes.DeadlineExceeded:
		return "common.timeout"
	case codes.Aborted:
		return "common.conflict"
	case codes.Unimplemented:
		return "common.not_implemented"
	case codes.Canceled:
		return "common.canceled"
	default:
		return "common.internal"
	}
}
