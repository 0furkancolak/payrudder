package payrudder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Int64 is a 64-bit integer that travels as a JSON string, as every *_minor amount of the API does (the protobuf
// JSON mapping of int64). It reads a string or a number, so a value is never squeezed through a float64.
type Int64 int64

// MarshalJSON writes the value as a string.
func (i Int64) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(i), 10) + `"`), nil
}

// UnmarshalJSON reads "123" or 123. null leaves the value alone.
func (i *Int64) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	s := string(bytes.Trim(b, `"`))
	if s == "" {
		*i = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("payrudder: %q is not an int64: %w", s, err)
	}
	*i = Int64(n)
	return nil
}

// String is the decimal text of the value.
func (i Int64) String() string { return strconv.FormatInt(int64(i), 10) }

// Decimal is an exact decimal number such as a usage quantity. It is sent as a JSON string and read from a string or
// a number, keeping the text as written: it never becomes a float.
type Decimal string

// MarshalJSON writes the text as a JSON string.
func (d Decimal) MarshalJSON() ([]byte, error) { return json.Marshal(string(d)) }

// UnmarshalJSON reads "1.25" or 1.25.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*d = Decimal(s)
		return nil
	}
	*d = Decimal(b)
	return nil
}

// String is the text of the number.
func (d Decimal) String() string { return string(d) }

// Ptr returns a pointer to v, for the optional fields of request structs.
func Ptr[T any](v T) *T { return &v }

// CancelSubscriptionResult is the answer of Subscriptions.CancelSubscription. Without credit_unused the API returns
// the subscription itself; with it, {subscription, credited_minor}. Both read into this type.
type CancelSubscriptionResult struct {
	Subscription
	// CreditedMinor is the unused fee credited to the customer, in minor units. Zero when nothing was credited.
	CreditedMinor Int64 `json:"credited_minor,omitempty"`
	// Credited is true when the answer carried credited_minor.
	Credited bool `json:"-"`
}

// UnmarshalJSON reads either of the two shapes.
func (r *CancelSubscriptionResult) UnmarshalJSON(b []byte) error {
	var probe struct {
		Subscription  *Subscription `json:"subscription"`
		CreditedMinor *Int64        `json:"credited_minor"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	*r = CancelSubscriptionResult{}
	if probe.Subscription != nil {
		r.Subscription = *probe.Subscription
		if probe.CreditedMinor != nil {
			r.CreditedMinor = *probe.CreditedMinor
			r.Credited = true
		}
		return nil
	}
	return json.Unmarshal(b, &r.Subscription)
}
