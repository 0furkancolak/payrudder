// Package model is what the re-rating module speaks: the job, its statuses and why a request is refused.
//
// Re-rating prices a closed period again under another plan version and bills the difference (docs/03-services/billing-svc.md#re-rating). Prices change, and
// so does the usage if a replay corrected it since the run: the job reads the usage at the tenant's active generation.
package model

import (
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
)

// Errors a caller can tell apart.
var (
	ErrNoRun           = errors.New("rerating: no billing run for that period")
	ErrRunNotCompleted = errors.New("rerating: the period's billing run is not COMPLETED")
	ErrUnknownPlanVer  = errors.New("rerating: unknown plan version")
	ErrNotFound        = errors.New("rerating: no such re-rating job")
)

// Job statuses.
const (
	Running   = "RUNNING"
	Completed = "COMPLETED"
	Failed    = "FAILED"
)

// Job is a rerating_jobs row.
type Job struct {
	ID, TenantID    uuid.UUID
	PeriodID        string
	PlanVersionID   uuid.UUID
	Generation      int32
	Status          string
	CustomersRated  int
	Corrections     int
	Credits         int
	CorrectionMinor int64
	CreditMinor     int64
	LastError       string
	CreatedAt       time.Time
	CompletedAt     *time.Time
}

// Of maps a row.
func Of(r sqlc.ReratingJob) *Job {
	return &Job{ID: r.ID, TenantID: r.TenantID, PeriodID: r.PeriodID, PlanVersionID: r.PlanVersionID, Generation: r.Generation, Status: r.Status,
		CustomersRated: int(r.CustomersRated), Corrections: int(r.Corrections), Credits: int(r.Credits), CorrectionMinor: r.CorrectionMinor, CreditMinor: r.CreditMinor,
		LastError: store.Deref(r.LastError), CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt}
}
