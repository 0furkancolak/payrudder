package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrorBody is the single error envelope of the public API:
//
//	{"error": {"code": "INVALID_ARGUMENT", "message": "…", "details": [{"field": "…", "description": "…"}], "request_id": "…"}}
type ErrorBody struct {
	Error ErrorInfo `json:"error"`
}

// ErrorInfo is the content of ErrorBody.
type ErrorInfo struct {
	Code      string        `json:"code"`
	Message   string        `json:"message"`
	Details   []FieldDetail `json:"details,omitempty"`
	RequestID string        `json:"request_id,omitempty"`
}

// FieldDetail is one field violation.
type FieldDetail struct {
	Field       string `json:"field"`
	Description string `json:"description"`
}

var httpStatus = map[codes.Code]int{
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.Aborted:            http.StatusConflict,
	codes.FailedPrecondition: http.StatusConflict,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.Canceled:           499,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.Unimplemented:      http.StatusNotImplemented,
}

// HTTPStatus maps a gRPC code to an HTTP status (500 for anything unmapped).
func HTTPStatus(c codes.Code) int {
	if s, ok := httpStatus[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// writeError renders err (a gRPC status or anything else) as the error envelope.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge errTooLarge
	if errors.As(err, &tooLarge) {
		writeJSONValue(w, http.StatusRequestEntityTooLarge, ErrorBody{Error: ErrorInfo{
			Code: "REQUEST_TOO_LARGE", Message: tooLarge.Error(), RequestID: requestID(r.Context())}})
		return
	}
	var tooMany errTooManyEvents
	if errors.As(err, &tooMany) {
		writeJSONValue(w, http.StatusRequestEntityTooLarge, ErrorBody{Error: ErrorInfo{
			Code: "REQUEST_TOO_LARGE", Message: tooMany.Error(), RequestID: requestID(r.Context())}})
		return
	}
	st := status.Convert(err)
	body := ErrorBody{Error: ErrorInfo{Code: codeName(st.Code()), Message: st.Message(), RequestID: requestID(r.Context())}}
	switch st.Code() {
	case codes.Internal, codes.Unknown, codes.DataLoss:
		body.Error.Message = "internal error" // never leak upstream internals
	}
	for _, d := range st.Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetReason() != "" {
			body.Error.Code = ei.GetReason() // an application error code such as STORAGE_QUOTA_EXCEEDED
		}
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, fv := range br.GetFieldViolations() {
				body.Error.Details = append(body.Error.Details, FieldDetail{Field: fv.GetField(), Description: fv.GetDescription()})
			}
		}
	}
	writeJSONValue(w, HTTPStatus(st.Code()), body)
}

// codeName renders codes as UPPER_SNAKE (codes.String gives "InvalidArgument").
func codeName(c codes.Code) string {
	names := map[codes.Code]string{
		codes.OK: "OK", codes.Canceled: "CANCELLED", codes.Unknown: "UNKNOWN", codes.InvalidArgument: "INVALID_ARGUMENT",
		codes.DeadlineExceeded: "DEADLINE_EXCEEDED", codes.NotFound: "NOT_FOUND", codes.AlreadyExists: "ALREADY_EXISTS",
		codes.PermissionDenied: "PERMISSION_DENIED", codes.ResourceExhausted: "RESOURCE_EXHAUSTED",
		codes.FailedPrecondition: "FAILED_PRECONDITION", codes.Aborted: "ABORTED", codes.OutOfRange: "OUT_OF_RANGE",
		codes.Unimplemented: "UNIMPLEMENTED", codes.Internal: "INTERNAL", codes.Unavailable: "UNAVAILABLE",
		codes.DataLoss: "DATA_LOSS", codes.Unauthenticated: "UNAUTHENTICATED",
	}
	if n, ok := names[c]; ok {
		return n
	}
	return "UNKNOWN"
}

func writeJSONValue(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// invalid builds an InvalidArgument status with one field violation.
func invalid(field, description string) error {
	st := status.New(codes.InvalidArgument, field+": "+description)
	if d, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{
		{Field: field, Description: description}}}); err == nil {
		st = d
	}
	return st.Err()
}
