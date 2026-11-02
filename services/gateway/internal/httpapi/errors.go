package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/errtrack"
)

// ErrorBody is the single error envelope of the public API:
//
//	{"error": {"code": "INVALID_ARGUMENT", "message": "…", "details": [{"field": "…", "description": "…"}], "request_id": "…"}}
type ErrorBody struct {
	Error ErrorInfo `json:"error"`
}

// ErrorInfo is the content of ErrorBody.
type ErrorInfo struct {
	Code string `json:"code"`
	// Key is stable and dotted ("billing.credit_note.not_paid"): translate it, not the message.
	Key       string        `json:"key"`
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

// statusOf maps a gRPC code to an HTTP status (500 for anything unmapped).
func statusOf(c codes.Code) int {
	if s, ok := httpStatus[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// writeError renders err (a gRPC status or anything else) as the error envelope. An internal error (the caller's request was fine, something behind
// the gateway was not) also goes to the error tracker, which is off unless PAYRUDDER_ERROR_TRACKING_URL is set.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	writeErrorReporting(w, r, err, true)
}

// writeErrorReporting is writeError; report false keeps an internal error from being sent to the tracker (the panic middleware has sent it already).
func writeErrorReporting(w http.ResponseWriter, r *http.Request, err error, report bool) {
	var tooLarge tooLargeError
	if errors.As(err, &tooLarge) {
		writeJSONValue(w, http.StatusRequestEntityTooLarge, ErrorBody{Error: ErrorInfo{
			Code: "REQUEST_TOO_LARGE", Key: errkey.Key("gateway.request.too_large"), Message: tooLarge.Error(), RequestID: requestID(r.Context())}})
		return
	}
	st := status.Convert(err)
	body := ErrorBody{Error: ErrorInfo{Code: codeName(st.Code()), Key: errkey.Default(st.Code()), Message: st.Message(), RequestID: requestID(r.Context())}}
	switch st.Code() {
	case codes.Internal, codes.Unknown, codes.DataLoss:
		body.Error.Message = "internal error" // never leak upstream internals
		if report {
			errtrack.Capture(r.Context(), err, errtrack.Tag("http.method", r.Method), errtrack.Tag("request_id", requestID(r.Context())))
		}
	}
	authored := false
	for _, d := range st.Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetDomain() == errkey.Domain && ei.GetMetadata()[errkey.MetaKey] != "" {
			body.Error.Key = ei.GetMetadata()[errkey.MetaKey]
			authored = true
		}
		if ei, ok := d.(*errdetails.ErrorInfo); ok && ei.GetReason() != "" {
			body.Error.Code = ei.GetReason() // an application error code such as STORAGE_QUOTA_EXCEEDED
		}
		if br, ok := d.(*errdetails.BadRequest); ok {
			for _, fv := range br.GetFieldViolations() {
				body.Error.Details = append(body.Error.Details, FieldDetail{Field: fv.GetField(), Description: fv.GetDescription()})
			}
		}
	}
	if !authored {
		if msg, generic := retryableMessage(st.Code(), body.Error.RequestID); generic {
			// The text is the client library's or a database's, not written for the caller: it names internal hosts, ports and tables. The caller keeps the code, the key
			// and the request id to quote; the text is in the log under the same id.
			slog.WarnContext(r.Context(), "upstream error", "code", st.Code().String(), "error", st.Message(), "request_id", body.Error.RequestID)
			body.Error.Message = msg
		}
	}
	writeJSONValue(w, statusOf(st.Code()), body)
}

// retryableMessage is the plain sentence for a raw error of a code that comes from the transport or a database and means "try again". generic is false for any other
// code, which keeps the message it has.
func retryableMessage(c codes.Code, id string) (msg string, generic bool) {
	quote := " (request id " + id + ")"
	switch c {
	case codes.Unavailable:
		return "the service is temporarily unavailable: try again shortly" + quote, true
	case codes.DeadlineExceeded:
		return "the request took too long: try again" + quote, true
	case codes.ResourceExhausted:
		return "a limit was reached: try again later" + quote, true
	case codes.Aborted:
		return "the request conflicted with another one: try again" + quote, true
	}
	return "", false
}

// codeNames renders codes as UPPER_SNAKE (codes.String gives "InvalidArgument").
var codeNames = map[codes.Code]string{
	codes.OK: "OK", codes.Canceled: "CANCELLED", codes.Unknown: "UNKNOWN", codes.InvalidArgument: "INVALID_ARGUMENT",
	codes.DeadlineExceeded: "DEADLINE_EXCEEDED", codes.NotFound: "NOT_FOUND", codes.AlreadyExists: "ALREADY_EXISTS",
	codes.PermissionDenied: "PERMISSION_DENIED", codes.ResourceExhausted: "RESOURCE_EXHAUSTED",
	codes.FailedPrecondition: "FAILED_PRECONDITION", codes.Aborted: "ABORTED", codes.OutOfRange: "OUT_OF_RANGE",
	codes.Unimplemented: "UNIMPLEMENTED", codes.Internal: "INTERNAL", codes.Unavailable: "UNAVAILABLE",
	codes.DataLoss: "DATA_LOSS", codes.Unauthenticated: "UNAUTHENTICATED",
}

func codeName(c codes.Code) string {
	if n, ok := codeNames[c]; ok {
		return n
	}
	return "UNKNOWN"
}

func writeJSONValue(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// invalid builds an InvalidArgument status with one field violation and the key gateway.request.invalid (the console puts the field and the
// description into the translated text).
func invalid(field, description string) error {
	st := status.Convert(errkey.New(codes.InvalidArgument, "gateway.request.invalid", field+": "+description))
	if d, err := st.WithDetails(&errdetails.BadRequest{FieldViolations: []*errdetails.BadRequest_FieldViolation{
		{Field: field, Description: description}}}); err == nil {
		st = d
	}
	return st.Err()
}
