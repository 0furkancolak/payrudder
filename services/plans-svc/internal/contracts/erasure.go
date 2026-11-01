package contracts

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Eraser blanks the IP address and the user agent of acceptances older than their tenant's retention (12 to 120 months, 36 unless the seller chose). It is the one
// thing that ever changes an acceptance, and the one reason plans_system holds UPDATE (ip, user_agent) on the table; the rest of the record stays. It runs on the system
// role over every tenant of the pooled database in a statement or two and, with cells on (ADR 0018), over each tenant's own schema in a transaction of its own.
type Eraser struct {
	places *placement.Places
	every  time.Duration
	now    func() time.Time
	log    *slog.Logger
}

// NewEraser returns the job; every is how often it looks (the retention is months long, so a day is plenty).
func NewEraser(places *placement.Places, every time.Duration, log *slog.Logger) *Eraser {
	if every <= 0 {
		every = 24 * time.Hour
	}
	return &Eraser{places: places, every: every, now: time.Now, log: log}
}

// WithClock sets the clock the job reads (tests).
func (e *Eraser) WithClock(now func() time.Time) *Eraser {
	e.now = now
	return e
}

func monthsBefore(now time.Time, months int32) time.Time { return now.AddDate(0, -int(months), 0) }

// erase blanks what is past retention in the schema db is looking at, and says how many rows it blanked.
func erase(ctx context.Context, db sqlc.DBTX, now time.Time) (int64, error) {
	q := sqlc.New(db)
	configured, err := q.ListContractRetention(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	ids := make([]uuid.UUID, 0, len(configured))
	for _, c := range configured {
		ids = append(ids, c.TenantID)
		n, err := q.EraseContractPersonalDataOf(ctx, sqlc.EraseContractPersonalDataOfParams{TenantID: c.TenantID, Cutoff: monthsBefore(now, c.RetentionMonths)})
		if err != nil {
			return total, err
		}
		total += n
	}
	n, err := q.EraseContractPersonalDataOthers(ctx, sqlc.EraseContractPersonalDataOthersParams{Configured: ids, Cutoff: monthsBefore(now, domain.DefaultRetention)})
	return total + n, err
}

// Once blanks what is due and says how many acceptances it changed.
func (e *Eraser) Once(ctx context.Context) (int64, error) {
	now := e.now()
	total, err := erase(ctx, e.places.Pool(), now)
	if err != nil {
		return total, err
	}
	err = e.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		return e.places.CellTx(ctx, t, func(tx pgx.Tx) error {
			n, err := erase(ctx, tx, now)
			total += n
			return err
		})
	})
	return total, err
}

// Run looks now and then every interval until ctx ends.
func (e *Eraser) Run(ctx context.Context) error {
	t := time.NewTicker(e.every)
	defer t.Stop()
	for {
		if n, err := e.Once(ctx); err != nil {
			e.log.ErrorContext(ctx, "contract erasure failed", "error", err)
		} else if n > 0 {
			e.log.InfoContext(ctx, "contract acceptances past retention: IP address and user agent blanked", "count", n)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
