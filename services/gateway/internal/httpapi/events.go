package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// MaxEventsPerRequest is the batch size limit of POST /v1/events.
const MaxEventsPerRequest = 500

type eventsRequest struct {
	Events []eventJSON `json:"events"`
}

// eventJSON is the wire shape of one event. quantity stays raw so that a JSON number is never converted to a float.
type eventJSON struct {
	IdempotencyKey     string            `json:"idempotency_key"`
	CustomerExternalID string            `json:"customer_external_id"`
	MeterCode          string            `json:"meter_code"`
	Quantity           json.RawMessage   `json:"quantity"`
	EventTime          string            `json:"event_time"`
	Properties         map[string]string `json:"properties"`
}

// quantityText returns the decimal text of a JSON string or number. Numbers are passed through as written ("1.10"
// stays "1.10", never 1.1 as a float64); anything else is returned as-is for the ingest service to reject.
func quantityText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	}
	return string(raw)
}

func parseEvents(r *http.Request) (*ingestv1.IngestEventsRequest, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	var in eventsRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, errkey.Newf(codes.InvalidArgument, "gateway.request.malformed_json", "malformed JSON: %v", err)
	}
	if dec.More() {
		return nil, errkey.New(codes.InvalidArgument, "gateway.request.malformed_json", "malformed JSON: trailing data")
	}
	switch n := len(in.Events); {
	case n == 0:
		return nil, invalid("events", "at least one event is required")
	case n > MaxEventsPerRequest:
		return nil, errTooManyEvents(n)
	}
	out := &ingestv1.IngestEventsRequest{Events: make([]*ingestv1.IncomingEvent, len(in.Events))}
	for i, e := range in.Events {
		ev := &ingestv1.IncomingEvent{
			IdempotencyKey: e.IdempotencyKey, CustomerExternalId: e.CustomerExternalID, MeterCode: e.MeterCode,
			Quantity: quantityText(e.Quantity), Properties: e.Properties,
		}
		if strings.TrimSpace(e.EventTime) != "" {
			t, err := time.Parse(time.RFC3339Nano, e.EventTime)
			if err != nil {
				return nil, invalid(fmt.Sprintf("events[%d].event_time", i), "must be an RFC 3339 timestamp")
			}
			ev.EventTime = timestamppb.New(t)
		}
		out.Events[i] = ev
	}
	return out, nil
}

var resultStatus = map[ingestv1.EventResult_Status]string{
	ingestv1.EventResult_STATUS_ACCEPTED:  "accepted",
	ingestv1.EventResult_STATUS_DUPLICATE: "duplicate",
	ingestv1.EventResult_STATUS_REJECTED:  "rejected",
}

type eventResultJSON struct {
	Status       string `json:"status"`
	EventID      string `json:"event_id,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// ingestEvents is POST /v1/events: 202 with a per-event result for a structurally valid batch.
func (s *Server) ingestEvents(w http.ResponseWriter, r *http.Request) {
	req, err := parseEvents(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !s.charge(w, r, principalFrom(r.Context()), ratelimit.ClassIngest, len(req.Events)) {
		return
	}
	// Payrudder as a SaaS (phase 55): a live tenant of an organisation over its hard events quota is refused here; what it is accepted is metered below.
	pol := s.saasPolicy(r.Context(), principalFrom(r.Context()).Tenant.ID.String())
	if pol != nil && !s.saasMayIngest(r.Context(), pol) {
		writeError(w, r, errEventsQuota())
		return
	}
	resp, err := s.d.Ingest.IngestEvents(r.Context(), req)
	if err != nil {
		switch status.Code(err) {
		case codes.ResourceExhausted:
			w.Header().Set("Retry-After", "5") // ingest backlog: back off a little
		case codes.Unavailable, codes.DeadlineExceeded:
			w.Header().Set("Retry-After", "1") // nothing was accepted: retrying with the same keys is safe
		}
		writeError(w, r, err)
		return
	}
	if pol != nil {
		s.saasMeter(r.Context(), pol, principalFrom(r.Context()).Tenant.ID.String(), resp.GetResults())
	}
	out := struct {
		Results []eventResultJSON `json:"results"`
	}{Results: make([]eventResultJSON, len(resp.GetResults()))}
	for i, res := range resp.GetResults() {
		out.Results[i] = eventResultJSON{Status: resultStatus[res.GetStatus()], EventID: res.GetEventId(),
			ErrorCode: res.GetErrorCode(), ErrorMessage: res.GetErrorMessage()}
	}
	writeJSONValue(w, http.StatusAccepted, out)
}
