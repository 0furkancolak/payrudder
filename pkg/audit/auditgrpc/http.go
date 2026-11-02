package auditgrpc

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
)

// A service that only speaks HTTP (payments-worker) answers the same question with the same fields in the query string and the same message as JSON.

// Query is the query string of an HTTP request for req (the gateway writes it, RequestFromQuery reads it). The tenant is in the path, never here.
func Query(req *auditv1.ListAuditEventsRequest) url.Values {
	q := url.Values{}
	if len(req.GetActions()) > 0 {
		q.Set("action", strings.Join(req.GetActions(), ","))
	}
	for k, v := range map[string]string{"actor": req.GetActor(), "target_type": req.GetTargetType(), "target_id": req.GetTargetId(), "page_token": req.GetPageToken()} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if t := req.GetFromTime(); t != nil {
		q.Set("from", t.AsTime().Format(time.RFC3339Nano))
	}
	if t := req.GetToTime(); t != nil {
		q.Set("to", t.AsTime().Format(time.RFC3339Nano))
	}
	if n := req.GetPageSize(); n > 0 {
		q.Set("page_size", strconv.Itoa(int(n)))
	}
	return q
}

// RequestFromQuery is Query's inverse.
func RequestFromQuery(q url.Values) (*auditv1.ListAuditEventsRequest, error) {
	var actions []string
	for _, a := range q["action"] { // action=a,b and action=a&action=b mean the same
		for _, name := range strings.Split(a, ",") {
			if name = strings.TrimSpace(name); name != "" {
				actions = append(actions, name)
			}
		}
	}
	req := &auditv1.ListAuditEventsRequest{Actions: actions, Actor: q.Get("actor"), TargetType: q.Get("target_type"), TargetId: q.Get("target_id"), PageToken: q.Get("page_token")}
	for key, dst := range map[string]**timestamppb.Timestamp{"from": &req.FromTime, "to": &req.ToTime} {
		if v := q.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				return nil, errkey.New(codes.InvalidArgument, "audit.filter.bad_time", key+" must be an RFC 3339 time")
			}
			*dst = timestamppb.New(t)
		}
	}
	if v := q.Get("page_size"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > audit.MaxLimit {
			return nil, errkey.Newf(codes.InvalidArgument, "audit.filter.bad_page_size", "page_size must be between 1 and %d", audit.MaxLimit)
		}
		req.PageSize = int32(n) //nolint:gosec // checked against MaxLimit above
	}
	return req, nil
}

// WriteJSON answers an HTTP request with the rows as a ListAuditEventsResponse.
func WriteJSON(w http.ResponseWriter, rows []audit.Row) {
	resp := &auditv1.ListAuditEventsResponse{Events: make([]*auditv1.AuditEvent, len(rows))}
	for i, r := range rows {
		resp.Events[i] = Message(r)
	}
	b, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(resp)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

// EventsFromJSON reads an answer written by WriteJSON back into messages (the gateway merges them with the other services').
func EventsFromJSON(b []byte) ([]*auditv1.AuditEvent, error) {
	var resp auditv1.ListAuditEventsResponse
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(b, &resp); err != nil {
		return nil, err
	}
	return resp.GetEvents(), nil
}
