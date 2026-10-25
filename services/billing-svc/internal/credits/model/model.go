// Package model is what the credits module speaks: the credit pack a tenant sells.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Pack is a credit pack as stored.
type Pack struct {
	ID         uuid.UUID
	Name       string
	Credits    int64
	PriceMinor int64
	Currency   string
	Active     bool
	CreatedAt  time.Time
	// ValidityDays is how long the credits bought from the pack last; nil means they never expire.
	ValidityDays *int32
}
