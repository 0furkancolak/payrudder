package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

type correctionsRequest struct {
	Corrections []correctionJSON `json:"corrections"`
}

// correctionJSON is the wire shape of one correction of a usage event (ADR 0027). quantity stays raw, like an event's, so that a JSON number is never converted to a float.
type correctionJSON struct {
	IdempotencyKey     string          `json:"idempotency_key"`
	Corrects           string          `json:"corrects"`
	Reason             string          `json:"reason"`
	CustomerExternalID string          `json:"customer_external_id"`
	MeterCode          string          `json:"meter_code"`
	Quantity           json.RawMessage `json:"quantity"`
}

func parseCorrections(r *http.Request) (*ingestv1.IngestEventsRequest, error) {
	body, err := readBody(r)
	if err != nil {
		return nil, err
	}
	var in correctionsRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, errkey.Newf(codes.InvalidArgument, "gateway.request.malformed_json", "malformed JSON: %v", err)
	}
	if dec.More() {
		return nil, errkey.New(codes.InvalidArgument, "gateway.request.malformed_json", "malformed JSON: trailing data")
	}
	switch n := len(in.Corrections); {
	case n == 0:
		return nil, invalid("corrections", "at least one correction is required")
	case n > MaxEventsPerRequest:
		return nil, errTooManyEvents(n)
	}
	out := &ingestv1.IngestEventsRequest{Events: make([]*ingestv1.IncomingEvent, len(in.Corrections))}
	for i, c := range in.Corrections {
		out.Events[i] = &ingestv1.IncomingEvent{
			IdempotencyKey: c.IdempotencyKey, Corrects: c.Corrects, Reason: c.Reason, CustomerExternalId: c.CustomerExternalID, MeterCode: c.MeterCode, Quantity: quantityText(c.Quantity),
		}
	}
	return out, nil
}

// correctEvents is POST /v1/events/corrections: 202 with a per-correction result, like POST /v1/events. A correction names the idempotency key of the event it corrects
// and the quantity that event should have had; who made it is recorded from the credential (the signed-in person, or the API key). It is not metered as usage.
func (s *Server) correctEvents(w http.ResponseWriter, r *http.Request) {
	req, err := parseCorrections(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !s.charge(w, r, principalFrom(r.Context()), ratelimit.ClassIngest, len(req.Events)) {
		return
	}
	req.Actor = s.tenantActorOf(r)
	resp, ok := s.ingest(w, r, req)
	if !ok {
		return
	}
	writeEventResults(w, resp)
}
