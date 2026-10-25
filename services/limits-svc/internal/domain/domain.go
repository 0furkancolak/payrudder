// Package domain holds limits-svc's pure rules: threshold arithmetic over the counters. Decoding usage events and
// matching meters is pkg/usage, shared with the aggregator and analytics-svc.
package domain

import (
	"fmt"
	"math/big"

	"github.com/google/uuid"
)

// MaxCounter is the largest value a counter may reach: Lua compares numbers as doubles, exact up to 2^53.
const MaxCounter = int64(1) << 53

// Crossed reports whether before < pct% of limit <= after, in exact integer arithmetic.
func Crossed(before, after, limit int64, pct int32) bool {
	if limit <= 0 || pct <= 0 {
		return false
	}
	t := new(big.Int).Mul(big.NewInt(limit), big.NewInt(int64(pct)))
	return times100(before).Cmp(t) < 0 && t.Cmp(times100(after)) <= 0
}

// Reached reports whether used has reached pct% of limit.
func Reached(used, limit int64, pct int32) bool {
	if limit <= 0 || pct <= 0 {
		return false
	}
	t := new(big.Int).Mul(big.NewInt(limit), big.NewInt(int64(pct)))
	return times100(used).Cmp(t) >= 0
}

func times100(v int64) *big.Int { return new(big.Int).Mul(big.NewInt(v), big.NewInt(100)) }

// eventNamespace scopes the uuidv5 ids of LimitExceeded events.
var eventNamespace = uuid.MustParse("5b0e6c52-8f0f-5a43-a3a1-3f1d6c6e0a11")

// LimitEventID is the deterministic id of the LimitExceeded event of one threshold in one period.
func LimitEventID(tenant, customer uuid.UUID, meter, period string, pct int32) string {
	return uuid.NewSHA1(eventNamespace, fmt.Appendf(nil, "%s|%s|%s|%s|%d", tenant, customer, meter, period, pct)).String()
}
