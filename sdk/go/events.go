package payrudder

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
)

const (
	// MaxEventsPerRequest is the largest batch POST /v1/events accepts.
	MaxEventsPerRequest = 500
	// MaxRequestBytes is the largest body the gateway accepts (1 MiB).
	MaxRequestBytes = 1 << 20

	// batchOverhead is the size of {"events":[]}.
	batchOverhead = 13
)

// Result statuses of EventResult.Status.
const (
	EventAccepted  = "accepted"
	EventDuplicate = "duplicate"
	EventRejected  = "rejected"
)

// ErrCodeEventTooLarge is the error_code the SDK gives an event that alone exceeds MaxRequestBytes, which the API
// would answer with 413 for the whole batch.
const ErrCodeEventTooLarge = "EVENT_TOO_LARGE"

// IngestEvents sends one batch (1 to 500 events, at most 1 MiB) as POST /v1/events. It does not split or complete the
// batch: see Ingest for that. A retry after a 500 happens only when every event has an idempotency key.
func (s *EventsService) IngestEvents(ctx context.Context, req *IngestEventsRequest, opts ...RequestOption) (*IngestEventsResponse, error) {
	if req == nil {
		req = &IngestEventsRequest{}
	}
	keyed := len(req.Events) > 0
	for i := range req.Events {
		if req.Events[i].IdempotencyKey == "" {
			keyed = false
			break
		}
	}
	var out IngestEventsResponse
	rs := requestSpec{method: http.MethodPost, path: "/v1/events", body: req, idempotent: keyed}
	if err := s.c.do(ctx, rs, &out, opts); err != nil {
		return nil, err
	}
	return &out, nil
}

// Ingest sends any number of events. It
//
//   - gives every event without an idempotency_key a generated one (live keys require it), so the automatic retries of
//     a call, and a repeated call with the same returned keys, never count an event twice;
//   - splits the input into valid batches (at most 500 events and 1 MiB each);
//   - returns one EventResult per input event, in input order. An event the API rejects comes back with Status
//     "rejected" and an ErrorCode; that is not an error of the call.
//
// The caller's slice is not modified. When a whole batch fails (authentication, rate limit after the retries, ...)
// Ingest returns the results of the batches before it together with the error, so the caller knows what was accepted.
func (s *EventsService) Ingest(ctx context.Context, events []IncomingEvent, opts ...RequestOption) ([]EventResult, error) {
	if len(events) == 0 {
		return nil, nil
	}
	prepared := make([]IncomingEvent, len(events))
	copy(prepared, events)
	for i := range prepared {
		if prepared[i].IdempotencyKey == "" {
			k, err := newIdempotencyKey()
			if err != nil {
				return nil, err
			}
			prepared[i].IdempotencyKey = k
		}
	}

	results := make([]EventResult, 0, len(prepared))
	var batch []IncomingEvent
	size := batchOverhead
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		resp, err := s.IngestEvents(ctx, &IngestEventsRequest{Events: batch}, opts...)
		if err != nil {
			return err
		}
		if len(resp.Results) != len(batch) {
			return fmt.Errorf("payrudder: ingest answered %d results for %d events", len(resp.Results), len(batch))
		}
		results = append(results, resp.Results...)
		batch, size = nil, batchOverhead
		return nil
	}
	for _, ev := range prepared {
		b, err := json.Marshal(ev)
		if err != nil {
			return results, fmt.Errorf("payrudder: encode event: %w", err)
		}
		n := len(b) + 1 // the comma
		if batchOverhead+n > MaxRequestBytes {
			// Flush first so the results keep the input order.
			if err := flush(); err != nil {
				return results, err
			}
			results = append(results, EventResult{
				Status:       EventRejected,
				ErrorCode:    ErrCodeEventTooLarge,
				ErrorMessage: fmt.Sprintf("the event is %d bytes, more than the %d byte request limit", len(b), MaxRequestBytes),
			})
			continue
		}
		if len(batch) == MaxEventsPerRequest || size+n > MaxRequestBytes {
			if err := flush(); err != nil {
				return results, err
			}
		}
		batch = append(batch, ev)
		size += n
	}
	if err := flush(); err != nil {
		return results, err
	}
	return results, nil
}

// newIdempotencyKey is "sdk_" and 32 hex digits, which fits the API's pattern ^[A-Za-z0-9_.:-]{1,128}$.
func newIdempotencyKey() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("payrudder: generate idempotency key: %w", err)
	}
	return "sdk_" + hex.EncodeToString(b[:]), nil
}
