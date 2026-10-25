// Package idgen generates the identifiers used across Payrudder:
// UUIDv7 for rows (time-ordered, index friendly), ULIDs for event IDs, and UUIDv5 for deterministic IDs
// (command IDs, limit-event IDs) that must be identical on every retry.
package idgen

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// Namespace is the UUIDv5 namespace for all Payrudder deterministic IDs.
var Namespace = uuid.MustParse("6f1b2a3c-0d4e-5f60-8a7b-9c0d1e2f3a4b")

// NewUUIDv7 returns a new time-ordered UUID.
func NewUUIDv7() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

var (
	ulidMu      sync.Mutex
	ulidEntropy = ulid.Monotonic(rand.Reader, 0)
)

// NewULID returns a new ULID string. IDs generated in the same millisecond by this process are monotonic.
func NewULID() string {
	return NewULIDAt(time.Now())
}

// NewULIDAt returns a ULID for the given time.
func NewULIDAt(t time.Time) string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(t), ulidEntropy).String()
}

// Deterministic returns uuidv5(Namespace, parts joined by "|"). The same parts always give the same ID,
// e.g. Deterministic(sagaID, "charge", "2") for a command ID. Parts must not contain "|".
func Deterministic(parts ...string) uuid.UUID {
	return uuid.NewSHA1(Namespace, []byte(strings.Join(parts, "|")))
}
