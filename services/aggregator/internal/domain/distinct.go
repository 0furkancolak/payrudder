package domain

import (
	"crypto/sha256"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Digest identifies a distinct value (usage.Digest of the property value).
type Digest = [sha256.Size]byte

// SeenKey identifies one distinct value of a UNIQUE meter in one billing period: a row of usage_distinct_values.
type SeenKey struct {
	Tenant, Customer uuid.UUID
	Meter, Period    string
	Generation       int32
	Value            Digest
}

// HourSeenKey identifies one distinct value in one wall-clock hour: a row of usage_distinct_hours.
type HourSeenKey struct {
	Tenant, Customer uuid.UUID
	Meter, Hour      string
	Generation       int32
	Value            Digest
}

// MergeSeen combines what two parts of the log say about the same value in the same period. A part says "" when an on-time event carried the value,
// and otherwise the billing period of the late event that carried it. The value is on time if any part says so, else it is billed in the earliest
// period any part names:
//
//	MergeSeen(a, b) = min(a, b)   with "" smaller than every period id
//
// which is associative, commutative and idempotent, so the final state of a value does not depend on the order in which partitions, redeliveries or a
// replay present its events, and the row can be kept with an upsert. Period ids ("2026-11") order chronologically as strings.
func MergeSeen(a, b string) string {
	if b < a {
		return b
	}
	return a
}

// SortedSeen sorts seen keys in a fixed order, so concurrent transactions lock the rows of their sets in the same order (a deadlock between two
// batches that share values would otherwise be possible).
func SortedSeen(m map[SeenKey]string) []SeenKey {
	keys := make([]SeenKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessSeen(keys[i], keys[j]) })
	return keys
}

func lessSeen(a, b SeenKey) bool {
	switch {
	case a.Tenant != b.Tenant:
		return a.Tenant.String() < b.Tenant.String()
	case a.Period != b.Period:
		return a.Period < b.Period
	case a.Customer != b.Customer:
		return a.Customer.String() < b.Customer.String()
	case a.Meter != b.Meter:
		return a.Meter < b.Meter
	case a.Generation != b.Generation:
		return a.Generation < b.Generation
	}
	return string(a.Value[:]) < string(b.Value[:])
}

// SortedHourSeen is SortedSeen for hour sets.
func SortedHourSeen(m map[HourSeenKey]struct{}) []HourSeenKey {
	keys := make([]HourSeenKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		switch {
		case a.Tenant != b.Tenant:
			return a.Tenant.String() < b.Tenant.String()
		case a.Customer != b.Customer:
			return a.Customer.String() < b.Customer.String()
		case a.Meter != b.Meter:
			return a.Meter < b.Meter
		case a.Hour != b.Hour:
			return a.Hour < b.Hour
		case a.Generation != b.Generation:
			return a.Generation < b.Generation
		}
		return strings.Compare(string(a.Value[:]), string(b.Value[:])) < 0
	})
	return keys
}

// AddSeen records that an event carried the value of k, on time (billed "") or late and billed in that period.
func (d *Delta) AddSeen(k SeenKey, billed string) {
	if cur, ok := d.Seen[k]; ok {
		billed = MergeSeen(cur, billed)
	}
	d.Seen[k] = billed
}

// AddHourSeen records that an on-time event carried the value of k in the hour.
func (d *Delta) AddHourSeen(k HourSeenKey) { d.HourSeen[k] = struct{}{} }
