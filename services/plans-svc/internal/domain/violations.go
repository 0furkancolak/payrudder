// Package domain validates plans-svc requests and converts between API and storage shapes.
// It has no I/O.
package domain

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
func (v *Violations) Empty() bool { return len(v.list) == 0 }

// Err returns an InvalidArgument status with BadRequest details, or nil.
func (v *Violations) Err() error {
	if v.Empty() {
		return nil
	}
	st := status.New(codes.InvalidArgument, "invalid request: "+v.list[0].GetField()+": "+v.list[0].GetDescription())
	if withDetails, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: v.list}); err == nil {
		st = withDetails
	}
	return st.Err()
}

// Invalid is a shortcut for a single violation.
func Invalid(field, description string) error {
	var v Violations
	v.Add(field, description)
	return v.Err()
}
