package command

import (
	"time"

	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

func feeRule(environment, provider, currency string, bps int32) store.FeeRule {
	return store.FeeRule{Environment: environment, Provider: provider, Currency: currency, RateBps: bps}
}

func farFuture() time.Time { return time.Now().Add(48 * time.Hour) }
func zeroTime() time.Time  { return time.Time{} }
