package command

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/period"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// correction is what a correcting event carries beyond an event's own facts (ADR 0027). The event it corrects is found, and the difference worked out, in the write
// transaction (resolveCorrections), because both depend on what is stored.
type correction struct {
	// key is the idempotency key of the event it corrects.
	key    string
	reason string
	// replacement is the quantity the original should have had, in the meter's scaled units; counts says the meter is a COUNT one, where an event is one whatever its
	// quantity, so the replacement is only whether it counts at all.
	replacement int64
	counts      bool
	// originalEventID and delta are what resolveCorrections found.
	originalEventID string
	delta           int64
}

// prepareCorrection runs the checks of a correcting event that need no write. Its customer and meter must exist like an ordinary event's, and the meter must be one that takes
// corrections; the event it corrects, the time, the period and the difference are settled in the write transaction.
func (c *Commands) prepareCorrection(ctx context.Context, b batch, idx int, ev *ingestv1.IncomingEvent) (*pending, *domain.Reject, error) {
	pre, rej := domain.CheckCorrectionPrelim(ev, b.ti.Environment)
	if rej != nil {
		return nil, rej, nil
	}
	meter, cust, rej, err := c.resolve(ctx, b, ev)
	if err != nil || rej != nil {
		return nil, rej, err
	}
	if !meter.AcceptsCorrection {
		return nil, &domain.Reject{Code: domain.CodeCorrectionUnsupportedMeter, Message: fmt.Sprintf(
			"meter %q cannot take a correction: only meters that add (SUM, COUNT) and have no property filter can", meter.Code)}, nil
	}
	units, rej := domain.ParseQuantity(ev.GetQuantity(), meter.Scale)
	if rej != nil {
		return nil, rej, nil
	}
	reason := strings.TrimSpace(ev.GetReason())
	eventID := idgen.NewULID()
	e := &eventsv1.UsageEvent{
		EventId: eventID, TenantId: b.tid, CustomerId: cust.ID, CustomerExternalId: cust.ExternalID, MeterCode: meter.Code, MeterVersion: meter.Version,
		QuantityScale: uint32(meter.Scale), //nolint:gosec // 0..6, validated by plans-svc
		Correction:    &eventsv1.Correction{Reason: reason, CorrectedBy: b.actor},
	}
	if pre.Auto {
		e.IdempotencyKey = domain.AutoKeyPrefix + eventID
	} else {
		e.IdempotencyKey = pre.Key
	}
	return &pending{
		idx: idx, key: pre.Key, event: e,
		hash: domain.CorrectionHash(cust.ExternalID, meter.Code, ev.GetCorrects(), reason, units, meter.Scale),
		corr: &correction{key: ev.GetCorrects(), reason: reason, replacement: units, counts: meter.Counts},
	}, nil, nil
}

// resolveCorrections finishes the correcting events of todo against the events they correct, inside the write transaction, and returns todo without the ones it refused
// (their results are written). For each: the original must be known (its key is in the dedup window and was written with its facts), must not itself be a correction,
// and must be the same customer and meter. The difference is the quantity asked for minus what the original stands at now (its quantity plus the differences of
// every earlier correction of it), read under an advisory lock on the original, so that two corrections of one event, in one batch or in two requests, each see the other's.
// The correcting event takes the original's time and period, and the difference as its signed quantity.
func (c *Commands) resolveCorrections(ctx context.Context, q *sqlc.Queries, b batch, buckets []time.Time, todo []*pending, results []*ingestv1.EventResult) ([]*pending, error) {
	var corrs []*pending
	for _, p := range todo {
		if p.corr != nil {
			corrs = append(corrs, p)
		}
	}
	if len(corrs) == 0 {
		return todo, nil
	}
	tenantID := b.ti.ID
	keys := make([]string, len(corrs))
	for i, p := range corrs {
		keys[i] = p.corr.key
	}
	originals, err := findKeyRows(ctx, q, tenantID, buckets, keys)
	if err != nil {
		return nil, fmt.Errorf("find the events to correct: %w", err)
	}
	ids := make([]string, 0, len(originals))
	for _, r := range originals {
		ids = append(ids, r.EventID)
	}
	isCorrection := map[string]bool{}
	if len(ids) > 0 {
		rows, err := q.WhichAreCorrections(ctx, sqlc.WhichAreCorrectionsParams{TenantID: tenantID, EventIds: ids})
		if err != nil {
			return nil, fmt.Errorf("which events are corrections: %w", err)
		}
		for _, id := range rows {
			isCorrection[id] = true
		}
	}

	refused := map[*pending]bool{}
	var live []*pending
	for _, p := range corrs {
		r, ok := originals[p.corr.key]
		switch {
		case !ok || r.CustomerID == nil || r.MeterCode == nil || r.EventTime == nil || r.QuantityUnits == nil:
			// Never seen, forgotten with its month, or written before corrections existed.
			results[p.idx], refused[p] = rejected(&domain.Reject{Code: domain.CodeCorrectionUnknownEvent, Message: fmt.Sprintf(
				"no event with the idempotency key %q is known: it was never accepted, or it is older than the keys are kept", p.corr.key)}), true
			continue
		case isCorrection[r.EventID]:
			results[p.idx], refused[p] = rejected(&domain.Reject{Code: domain.CodeCorrectionOfCorrection, Message: "the event is itself a correction: correct the original again, with the quantity it should have"}), true
			continue
		case r.CustomerID.String() != p.event.CustomerId || *r.MeterCode != p.event.MeterCode:
			results[p.idx], refused[p] = rejected(&domain.Reject{Code: domain.CodeCorrectionMismatch, Message: "customer_external_id and meter_code must be those of the event being corrected"}), true
			continue
		}
		p.corr.originalEventID = r.EventID
		p.event.EventTime = timestamppb.New(*r.EventTime)
		p.event.PeriodId = period.PeriodOf(*r.EventTime, b.loc)
		p.event.Correction.CorrectsEventId = r.EventID
		live = append(live, p)
	}
	if len(live) == 0 {
		return withoutRefused(todo, refused), nil
	}
	if err := c.differences(ctx, q, tenantID, originals, live); err != nil {
		return nil, err
	}
	return withoutRefused(todo, refused), nil
}

// differences works out the signed difference of each correction in live (in batch order), under an advisory lock on every original, taken in id order so that two
// requests correcting the same events cannot wait for each other.
func (c *Commands) differences(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, originals map[string]sqlc.FindIdempotencyKeysRow, live []*pending) error {
	var ids []string
	seen := map[string]bool{}
	for _, p := range live {
		if id := p.corr.originalEventID; !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := q.LockOriginal(ctx, sqlc.LockOriginalParams{TenantID: tenantID.String(), EventID: id}); err != nil {
			return fmt.Errorf("lock the event to correct: %w", err)
		}
	}
	moved, err := q.CorrectedUnits(ctx, sqlc.CorrectedUnitsParams{TenantID: tenantID, EventIds: ids})
	if err != nil {
		return fmt.Errorf("earlier corrections: %w", err)
	}
	standing := map[string]int64{} // what each original stands at now: its quantity (one, for COUNT) plus the differences so far
	for _, p := range live {
		id := p.corr.originalEventID
		if _, ok := standing[id]; !ok {
			base := *originals[p.corr.key].QuantityUnits
			if p.corr.counts {
				base = 1
			}
			standing[id] = base
			for _, m := range moved {
				if m.CorrectsEventID == id {
					standing[id] += m.DeltaUnits
				}
			}
		}
		want := p.corr.replacement
		if p.corr.counts && want > 0 {
			want = 1
		}
		p.corr.delta = want - standing[id]
		standing[id] = want
		p.event.QuantityUnits = p.corr.delta
	}
	return nil
}

// withoutRefused is todo minus the refused events, in order.
func withoutRefused(todo []*pending, refused map[*pending]bool) []*pending {
	if len(refused) == 0 {
		return todo
	}
	out := make([]*pending, 0, len(todo)-len(refused))
	for _, p := range todo {
		if !refused[p] {
			out = append(out, p)
		}
	}
	return out
}

// recordCorrections writes the record of the corrections among the accepted events (those that won their keys; a duplicate is not here).
func recordCorrections(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, accepted []*pending) error {
	var in sqlc.InsertCorrectionsParams
	for _, p := range accepted {
		if p.corr == nil {
			continue
		}
		cust, err := uuid.Parse(p.event.CustomerId)
		if err != nil {
			return fmt.Errorf("customer id %q: %w", p.event.CustomerId, err)
		}
		in.EventIds, in.CorrectionKeys = append(in.EventIds, p.event.EventId), append(in.CorrectionKeys, p.event.IdempotencyKey)
		in.CorrectsKeys, in.CorrectsEventIds = append(in.CorrectsKeys, p.corr.key), append(in.CorrectsEventIds, p.corr.originalEventID)
		in.CustomerIds, in.MeterCodes, in.Deltas = append(in.CustomerIds, cust), append(in.MeterCodes, p.event.MeterCode), append(in.Deltas, p.corr.delta)
		in.Reasons, in.CorrectedBy = append(in.Reasons, p.corr.reason), append(in.CorrectedBy, p.event.Correction.CorrectedBy)
	}
	if len(in.EventIds) == 0 {
		return nil
	}
	in.TenantID = tenantID
	if err := q.InsertCorrections(ctx, in); err != nil {
		return fmt.Errorf("record corrections: %w", err)
	}
	return nil
}
