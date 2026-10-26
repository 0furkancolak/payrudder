package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/0furkancolak/payrudder/pkg/errkey"
)

var (
	unmarshal = protojson.UnmarshalOptions{}
	marshal   = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
)

// tooLargeError is a request the gateway refuses with 413: too many bytes, too many files, too many events.
type tooLargeError struct{ msg string }

func (e tooLargeError) Error() string { return e.msg }

// errBodyTooLarge is a request body larger than limit bytes.
func errBodyTooLarge(limit int64) error {
	return tooLargeError{fmt.Sprintf("request body larger than %d bytes", limit)}
}

// errTooManyFiles is a request with more than limit files.
func errTooManyFiles(limit int) error {
	return tooLargeError{fmt.Sprintf("more than %d files in one request", limit)}
}

// errTooManyEvents is a batch of got events, more than MaxEventsPerRequest.
func errTooManyEvents(got int) error {
	return tooLargeError{fmt.Sprintf("a request may carry at most %d events, got %d", MaxEventsPerRequest, got)}
}

// readBody reads the whole request body, which the route's limit has capped (http.MaxBytesReader). A body over the limit is the 413 answer.
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, errBodyTooLarge(mbe.Limit)
		}
		return nil, errkey.New(codes.InvalidArgument, "gateway.request.unreadable_body", "unreadable body")
	}
	return body, nil
}

// decode reads a JSON body into msg. An empty body leaves msg unchanged.
func decode(r *http.Request, msg proto.Message) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := unmarshal.Unmarshal(body, msg); err != nil {
		return errkey.Newf(codes.InvalidArgument, "gateway.request.malformed_json", "malformed JSON: %v", err)
	}
	return nil
}

// decodeBody reads a JSON body into v for a handler that has its own words for a body it cannot use: bad is what it answers when the body is empty or not
// JSON. A body over the limit is the 413 answer like everywhere else, never bad.
func decodeBody(r *http.Request, v any, bad error) error {
	raw, err := readBody(r)
	if err != nil {
		return err
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(v); err != nil {
		return bad
	}
	return nil
}

// decodeJSON reads a JSON object body into v. Fields v does not name are ignored: the handler never takes a user, account or tenant id from a body.
func decodeJSON(r *http.Request, v any) error {
	raw, err := readBody(r)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errkey.New(codes.InvalidArgument, "gateway.request.malformed_json", "malformed JSON body")
	}
	return nil
}
