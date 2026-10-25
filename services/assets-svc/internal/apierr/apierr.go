// Package apierr builds the errors the AssetsService methods return: application failures that carry both an error key
// and an application error code, and the catch-all internal error.
package apierr

import (
	"context"
	"log/slog"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
)

// violationKeys are the error keys of the application error codes (ErrorInfo.Reason) that policy violations carry.
var violationKeys = map[string]string{
	policy.UnsupportedContentType: errkey.Key("assets.upload.unsupported_content_type"),
	policy.ContentTypeMismatch:    errkey.Key("assets.upload.content_type_mismatch"),
	policy.FileTooLarge:           errkey.Key("assets.upload.file_too_large"),
	policy.StorageQuotaExceeded:   errkey.Key("assets.storage.quota_exceeded"),
	policy.InvalidFilename:        errkey.Key("assets.upload.invalid_filename"),
	policy.PartMismatch:           errkey.Key("assets.upload.part_mismatch"),
	policy.SizeMismatch:           errkey.Key("assets.upload.size_mismatch"),
}

// Failure builds a gRPC error that carries an error key (the console translates it) and also an application error code (ErrorInfo.Reason), which the
// gateway puts in the `code` of its error envelope.
func Failure(c codes.Code, reason, msg string) error {
	key, ok := violationKeys[reason]
	if !ok {
		key = errkey.Default(c)
	}
	// The application code comes first: clients that read the first ErrorInfo keep getting it.
	st := status.New(c, msg)
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "payrudder.assets"},
		&errdetails.ErrorInfo{Domain: errkey.Domain, Metadata: map[string]string{errkey.MetaKey: key}}); err == nil {
		st = d
	}
	return st.Err()
}

// FromViolation turns a policy violation into its failure.
func FromViolation(v *policy.Violation) error {
	c := codes.InvalidArgument
	if v.Code == policy.StorageQuotaExceeded {
		c = codes.FailedPrecondition
	}
	return Failure(c, v.Code, v.Message)
}

// Internal passes a gRPC status through and hides anything else behind an internal error, after logging it.
func Internal(ctx context.Context, err error) error {
	if _, ok := status.FromError(err); ok {
		return err
	}
	slog.ErrorContext(ctx, "assets-svc internal error", "error", err)
	return status.Error(codes.Internal, "internal error")
}
