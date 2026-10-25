// Package model is what the billing-run module speaks: the run and its statuses.
package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Status of a billing run.
const (
	Scheduled        = "SCHEDULED"
	WaitingWatermark = "WAITING_WATERMARK"
	Rating           = "RATING"
	Invoicing        = "INVOICING"
	Completed        = "COMPLETED"
	Failed           = "FAILED"
)

// Run is a billing_runs row.
type Run struct {
	TenantID     uuid.UUID
	PeriodID     string
	Status       string
	CutoffAt     time.Time
	Generation   *int32
	InvoiceCount int
	LastError    string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	// Offsets are the end offsets of the usage topic the aggregator must have consumed, nil until they are snapshotted.
	Offsets map[int32]int64
}

// Of maps a row.
func Of(row sqlc.BillingRun) (*Run, error) {
	r := &Run{TenantID: row.TenantID, PeriodID: row.PeriodID, Status: row.Status, CutoffAt: row.CutoffAt, Generation: row.Generation, InvoiceCount: int(row.InvoiceCount),
		CreatedAt: row.CreatedAt, StartedAt: row.StartedAt, CompletedAt: row.CompletedAt}
	if row.LastError != nil {
		r.LastError = *row.LastError
	}
	if len(row.RequiredOffsets) > 0 && string(row.RequiredOffsets) != "null" {
		if err := json.Unmarshal(row.RequiredOffsets, &r.Offsets); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// ErrBadPeriod: the period id is not YYYY-MM.
var ErrBadPeriod = errors.New("billing run: bad period id")

// PeriodOpenError: a live period can only be closed after its cutoff.
type PeriodOpenError struct {
	Period string
	Cutoff time.Time
}

func (e *PeriodOpenError) Error() string {
	return fmt.Sprintf("billing run: period %s closes at %s", e.Period, e.Cutoff.UTC().Format(time.RFC3339))
}
