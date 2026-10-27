package domain

// MaxQuantity is the most seats a subscription can have (the database holds the same bound).
const MaxQuantity = 100000

// QuantityOf is the quantity a request asks for: 0 keeps current, anything else must be between 1 and MaxQuantity.
func QuantityOf(requested, current int32) (int32, error) {
	switch {
	case requested == 0:
		return current, nil
	case requested < 0 || requested > MaxQuantity:
		return 0, Invalid("quantity", "must be between 1 and 100000")
	}
	return requested, nil
}
