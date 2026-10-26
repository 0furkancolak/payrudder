// Package domain validates plans-svc requests and converts between API and storage shapes.
// It has no I/O.
package domain

import (
	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// Violations collects field-level validation errors. The gateway renders them as error.details.
type Violations struct {
	list []*errdetails.BadRequest_FieldViolation
}

// Add records a violation for field.
func (v *Violations) Add(field, description string) {
	v.list = append(v.list, &errdetails.BadRequest_FieldViolation{Field: field, Description: description})
}

// Empty reports whether nothing was recorded.
func (v *Violations) empty() bool { return len(v.list) == 0 }

// Err returns an InvalidArgument status with BadRequest details and the key plans.request.invalid (the console puts the first field and its
// description into the translated text), or nil.
func (v *Violations) Err() error {
	if v.empty() {
		return nil
	}
	st := status.Convert(errkey.New(codes.InvalidArgument, "plans.request.invalid", "invalid request: "+v.list[0].GetField()+": "+v.list[0].GetDescription()))
	if withDetails, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: v.list}); err == nil {
		st = withDetails
	}
	return st.Err()
}

// ParseID reads a UUID from a request field; a malformed one is a violation of that field.
func ParseID(field, raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, Invalid(field, "must be a UUID")
	}
	return id, nil
}

// Invalid is a shortcut for a single violation.
func Invalid(field, description string) error {
	var v Violations
	v.Add(field, description)
	return v.Err()
}
