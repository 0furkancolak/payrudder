package subscription

// How the fixed fee of a subscription is invoiced (subscriptions.fee_timing): at the start of each period, or after it with the usage.
const (
	FeeInAdvance = "advance"
	FeeInArrears = "arrears"
)
