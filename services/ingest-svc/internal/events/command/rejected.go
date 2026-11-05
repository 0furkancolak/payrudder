package command

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/pkg/errtrack"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

const (
	// rejectedWriters bounds the log writes in flight; a rejection storm drops the surplus rather than queueing or slowing ingest (the metric still counts them).
	rejectedWriters = 4
	// rejectedWriteTimeout bounds one log write.
	rejectedWriteTimeout = 3 * time.Second
	// rejectedTrimEvery is how often a tenant's log is trimmed by the writer; the age sweep of the maintenance covers the tenants that stop being rejected.
	rejectedTrimEvery = time.Minute
	// maxRejectedField cuts what the developer sent (a meter code, a customer id, a key, the API's message) before it is kept.
	maxRejectedField = 200
)

// recordRejected writes the rejected events of a batch to the tenant's log (plan 18), after the answer is known and without making the request wait: best effort, so a
// slow or failing database loses log rows, never an accepted event or the response. It keeps what identifies an event (meter code, customer id, time, key) and the API's
// answer, never the properties or the quantity.
func (c *Commands) recordRejected(ctx context.Context, b batch, events []*ingestv1.IncomingEvent, results []*ingestv1.EventResult) {
	var q sqlc.InsertRejectedEventsParams
	q.TenantID = b.ti.ID
	for i, r := range results {
		if r.GetStatus() != ingestv1.EventResult_STATUS_REJECTED {
			continue
		}
		ev := events[i]
		var at time.Time
		if ev.GetEventTime() != nil {
			at = ev.GetEventTime().AsTime()
		}
		q.ErrorCodes = append(q.ErrorCodes, cut(r.GetErrorCode()))
		q.ErrorMessages = append(q.ErrorMessages, cut(r.GetErrorMessage()))
		q.MeterCodes = append(q.MeterCodes, cut(ev.GetMeterCode()))
		q.CustomerIds = append(q.CustomerIds, cut(ev.GetCustomerExternalId()))
		q.EventTimes = append(q.EventTimes, at)
		q.IdempotencyKeys = append(q.IdempotencyKeys, cut(ev.GetIdempotencyKey()))
	}
	if len(q.ErrorCodes) == 0 {
		return
	}
	select {
	case c.rejectedSem <- struct{}{}:
	default:
		return
	}
	c.rejectedWG.Add(1)
	go func() {
		defer errtrack.Recover(ctx)
		defer c.rejectedWG.Done()
		defer func() { <-c.rejectedSem }()
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rejectedWriteTimeout)
		defer cancel()
		trim := c.dueForTrim(b.ti.ID.String(), c.o.Now())
		err := pgxx.InTenantTx(wctx, c.o.App, b.ti.ID, func(tx pgx.Tx) error {
			qs := sqlc.New(tx)
			if err := qs.InsertRejectedEvents(wctx, q); err != nil {
				return err
			}
			if !trim {
				return nil
			}
			_, err := qs.TrimRejectedEvents(wctx, sqlc.TrimRejectedEventsParams{TenantID: b.ti.ID, MaxDays: domain.RejectedKeepDays, MaxRows: domain.RejectedMaxRows})
			return err
		})
		if err != nil {
			c.log.WarnContext(wctx, "ingest: rejected events were not logged", "error", err, "count", len(q.ErrorCodes))
		}
	}()
}

// WaitRejected waits for the log writes in flight (tests; the server never needs to).
func (c *Commands) WaitRejected() { c.rejectedWG.Wait() }

// dueForTrim says whether tenant's log is to be trimmed by this write: at most once a minute, so a stream of rejections does not run the trim each time. A log can
// exceed its row limit by what one minute brings (the sweep and the next trim bring it back).
func (c *Commands) dueForTrim(tenantID string, now time.Time) bool {
	c.trimMu.Lock()
	defer c.trimMu.Unlock()
	if last, ok := c.trimmed[tenantID]; ok && now.Sub(last) < rejectedTrimEvery {
		return false
	}
	c.trimmed[tenantID] = now
	return true
}

// cut shortens s to maxRejectedField characters (never inside one), so that a value the developer sent cannot make a log row large.
func cut(s string) string {
	s = strings.ReplaceAll(s, "\x00", "") // a NUL is valid JSON and not valid in a Postgres text
	if utf8.RuneCountInString(s) <= maxRejectedField {
		return s
	}
	return string([]rune(s)[:maxRejectedField])
}
