// Package quota is the storage quota of a tenant: the limit of its tier (Limits), what it uses (query) and the
// reservation made when a file becomes READY (command).
package quota

import (
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
)

// Defaults are the per-tier storage quotas used when none are configured.
var Defaults = map[string]int64{"free": 100 * policy.MiB, "standard": 10 * policy.GiB, "enterprise": 1024 * policy.GiB}

// Limits are the storage limits in bytes by tenant tier ("free", "standard", "enterprise").
type Limits struct {
	byTier map[string]int64
	// fallback applies to an unknown tier.
	fallback int64
}

// NewLimits returns the limits for the tiers; nil tiers mean Defaults, and a fallback <= 0 means the "free" limit.
func NewLimits(byTier map[string]int64, fallback int64) Limits {
	if byTier == nil {
		byTier = Defaults
	}
	if fallback <= 0 {
		fallback = byTier["free"]
	}
	return Limits{byTier: byTier, fallback: fallback}
}

// For is the storage limit of the tenant's tier.
func (l Limits) For(ti tenant.Info) int64 {
	if q, ok := l.byTier[ti.Tier]; ok {
		return q
	}
	return l.fallback
}
