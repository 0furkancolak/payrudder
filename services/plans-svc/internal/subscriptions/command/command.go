// Package command changes subscriptions: creates, cancels, pauses, resumes, schedules their end, moves them to another plan and syncs
// the ones a payment provider runs.
package command

import (
	"time"

	"github.com/0furkancolak/quotify/services/plans-svc/internal/store"
)

// Commands changes subscriptions.
type Commands struct {
	db  *store.DB
	now func() time.Time
}

// New returns the subscription commands.
func New(db *store.DB, now func() time.Time) *Commands { return &Commands{db: db, now: now} }
