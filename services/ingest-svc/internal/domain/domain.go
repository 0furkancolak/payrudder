// Package domain holds ingest-svc's pure validation rules: no I/O, so every rule is table-testable.
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"regexp"
	"sort"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	ingestv1 "github.com/0furkancolak/quotify/gen/go/quotify/ingest/v1"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/tenant"
)

// Limits (docs/03-services/ingest-svc.md#validation-rules).
const (
	MaxBatch         = 500
	MaxLateness      = 35 * 24 * time.Hour
	MaxFuture        = 5 * time.Minute
	MaxProperties    = 32
	MaxPropertyBytes = 4096
)

// Error codes reported per event.
const (
	CodeUnknownMeter      = "UNKNOWN_METER"
	CodeUnknownCustomer   = "UNKNOWN_CUSTOMER"
	CodeEventTooOld       = "EVENT_TOO_OLD"
	CodeEventInFuture     = "EVENT_IN_FUTURE"
	CodeInvalidEventTime  = "INVALID_EVENT_TIME"
	CodeInvalidQuantity   = "INVALID_QUANTITY"
	CodeInvalidPrecision  = "INVALID_QUANTITY_PRECISION"
	CodeKeyRequired       = "IDEMPOTENCY_KEY_REQUIRED"
	CodeKeyInvalid        = "IDEMPOTENCY_KEY_INVALID"
	CodeKeyReused         = "IDEMPOTENCY_KEY_REUSED"
	CodeInvalidProperties = "INVALID_PROPERTIES"
	AutoKeyPrefix         = "auto:"
)

var keyRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// Reject is a per-event validation failure.
type Reject struct {
	Code    string
	Message string
}

func (r *Reject) Error() string { return r.Code + ": " + r.Message }

// Prelim is the outcome of the checks that need no lookups.
type Prelim struct {
	// Key is the idempotency key to dedup on. Empty when Auto.
	Key string
	// Auto is true for sandbox events without a key: no dedup, the key is auto:{event_id}.
	Auto      bool
	EventTime time.Time
}

// CheckPrelim validates the idempotency key, event time and properties.
func CheckPrelim(ev *ingestv1.IncomingEvent, now time.Time, environment string) (Prelim, *Reject) {
	var p Prelim
	switch key := ev.GetIdempotencyKey(); {
	case key == "" && environment == tenant.EnvSandbox:
		p.Auto = true
	case key == "":
		return p, &Reject{CodeKeyRequired, "idempotency_key is required for live API keys"}
	case !keyRe.MatchString(key):
		return p, &Reject{CodeKeyInvalid, "idempotency_key must be 1-128 characters of [A-Za-z0-9_.:-]"}
	default:
		p.Key = key
	}

	ts := ev.GetEventTime()
	if ts == nil || ts.CheckValid() != nil {
		return p, &Reject{CodeInvalidEventTime, "event_time is required and must be a valid timestamp"}
	}
	p.EventTime = ts.AsTime()
	if p.EventTime.Before(now.Add(-MaxLateness)) {
		return p, &Reject{CodeEventTooOld, "event_time is more than 35 days in the past"}
	}
	if p.EventTime.After(now.Add(MaxFuture)) {
		return p, &Reject{CodeEventInFuture, "event_time is more than 5 minutes in the future"}
	}

	if err := checkProperties(ev.GetProperties()); err != nil {
		return p, &Reject{CodeInvalidProperties, err.Error()}
	}
	return p, nil
}

func checkProperties(props map[string]string) error {
	if len(props) > MaxProperties {
		return errors.New("at most 32 properties are allowed")
	}
	size := 0
	for k, v := range props {
		if k == "" {
			return errors.New("property names must not be empty")
		}
		size += len(k) + len(v)
	}
	if size > MaxPropertyBytes {
		return errors.New("properties are larger than 4 KiB in total")
	}
	return nil
}

// ParseQuantity converts the decimal string to scaled units for a meter scale.
func ParseQuantity(q string, scale int) (int64, *Reject) {
	units, err := quantity.ParseUnits(q, scale)
	switch {
	case err == nil:
		return units, nil
	case errors.Is(err, quantity.ErrPrecision):
		return 0, &Reject{CodeInvalidPrecision, "quantity has more decimal places than the meter allows"}
	default:
		return 0, &Reject{CodeInvalidQuantity, "quantity must be a non-negative decimal that fits the meter scale"}
	}
}

// PayloadHash is the canonical digest of an event's content, used to tell a retry from a reused key. Properties are
// sorted, strings are length-prefixed and the quantity is the normalized scaled value, so "1.0" and "1" hash equally.
func PayloadHash(customerExternalID, meterCode string, units int64, scale int, eventTime time.Time, props map[string]string) []byte {
	h := sha256.New()
	var buf []byte
	str := func(s string) {
		buf = binary.AppendUvarint(buf[:0], uint64(len(s)))
		h.Write(buf)
		h.Write([]byte(s))
	}
	num := func(n int64) {
		buf = binary.BigEndian.AppendUint64(buf[:0], uint64(n)) //nolint:gosec // two's complement bit pattern is the intent
		h.Write(buf)
	}
	str(customerExternalID)
	str(meterCode)
	num(units)
	num(int64(scale))
	num(eventTime.UnixNano())
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	num(int64(len(keys)))
	for _, k := range keys {
		str(k)
		str(props[k])
	}
	return h.Sum(nil)
}

// Timestamp is a helper for building UsageEvent timestamps.
func Timestamp(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t.UTC()) }

// BucketOf returns the start of t's UTC month: the created_at bucket of idempotency_keys.
func BucketOf(t time.Time) time.Time {
	y, m, _ := t.UTC().Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}
