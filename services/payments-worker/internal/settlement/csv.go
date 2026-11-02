package settlement

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
)

// MaxCSVLines bounds one import. A longer report is imported in parts.
const MaxCSVLines = 10000

func formatInt(n int64) string { return strconv.FormatInt(n, 10) }

// LineError is a line of a CSV that cannot be imported.
type LineError struct {
	Line    int    `json:"line"`
	Message string `json:"error"`
}

// ParseCSV reads a settlement report (docs/reconciliation.md#settlement-csv): a header row, then one line per payment or payout. The columns, in any order and in
// any case: provider (an account key; optional when defaultProvider is given), reference (the report's id of the line: the same reference read again is the same
// line), payment_ref (the provider reference Payrudder's ledger holds for the payment; empty for a payout), kind (payment or payout; default payment), currency, gross,
// fee (default 0), net (default gross - fee), occurred_on, available_on, settled_on (RFC 3339 or YYYY-MM-DD), batch. Amounts are decimals in the currency's major unit
// ("10.50", "-3"), never more fraction digits than the currency has. The whole file is refused if any line is: a half-imported report is worse than none.
func ParseCSV(r io.Reader, defaultProvider string) ([]store.SettlementLine, []LineError) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	head, err := cr.Read()
	if err != nil {
		return nil, []LineError{{Line: 1, Message: "the first line must be the header: " + err.Error()}}
	}
	col := map[string]int{}
	for i, h := range head {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\xef\xbb\xbf")))] = i
	}
	for _, need := range []string{"reference", "currency", "gross", "occurred_on"} {
		if _, ok := col[need]; !ok {
			return nil, []LineError{{Line: 1, Message: "the header has no " + need + " column"}}
		}
	}
	if _, ok := col["provider"]; !ok && defaultProvider == "" {
		return nil, []LineError{{Line: 1, Message: "the header has no provider column and the request names no provider"}}
	}
	var out []store.SettlementLine
	var errs []LineError
	seen := map[[2]string]int{}
	for n := 2; ; n++ {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			errs = append(errs, LineError{Line: n, Message: err.Error()})
			break
		}
		if len(out) >= MaxCSVLines {
			errs = append(errs, LineError{Line: n, Message: fmt.Sprintf("more than %d lines: import the report in parts", MaxCSVLines)})
			break
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		l, err := parseLine(get, defaultProvider)
		if err != nil {
			errs = append(errs, LineError{Line: n, Message: err.Error()})
			continue
		}
		k := [2]string{l.Provider, l.LineRef}
		if first, dup := seen[k]; dup {
			errs = append(errs, LineError{Line: n, Message: "the reference " + l.LineRef + " is also on line " + strconv.Itoa(first)})
			continue
		}
		seen[k] = n
		out = append(out, l)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

func parseLine(get func(string) string, defaultProvider string) (store.SettlementLine, error) {
	l := store.SettlementLine{Provider: get("provider"), LineRef: get("reference"), PaymentRef: get("payment_ref"), Kind: strings.ToLower(get("kind")), Currency: strings.ToUpper(get("currency")),
		Batch: get("batch"), Source: "csv"}
	if l.Provider == "" {
		l.Provider = defaultProvider
	}
	if !payments.ValidAccountKey(l.Provider) {
		return l, errors.New("provider " + strconv.Quote(l.Provider) + " is not a provider or account key")
	}
	if l.LineRef == "" || !utf8.ValidString(l.LineRef) || len(l.LineRef) > 200 {
		return l, errors.New("reference is required (at most 200 characters)")
	}
	if l.Kind == "" {
		l.Kind = "payment"
	}
	if l.Kind != "payment" && l.Kind != "payout" {
		return l, errors.New("kind must be payment or payout")
	}
	if l.Kind == "payment" && l.PaymentRef == "" {
		return l, errors.New("payment_ref is required for a payment: it is how the line finds its payment in the ledger")
	}
	cur, ok := money.LookupCurrency(l.Currency)
	if !ok {
		return l, errors.New("currency " + strconv.Quote(l.Currency) + " is not a supported ISO 4217 code")
	}
	var err error
	if l.GrossMinor, err = parseMinor(get("gross"), int(cur.Exponent)); err != nil {
		return l, errors.New("gross: " + err.Error())
	}
	if f := get("fee"); f != "" {
		if l.FeeMinor, err = parseMinor(f, int(cur.Exponent)); err != nil {
			return l, errors.New("fee: " + err.Error())
		}
	}
	l.NetMinor = l.GrossMinor - l.FeeMinor
	if s := get("net"); s != "" {
		if l.NetMinor, err = parseMinor(s, int(cur.Exponent)); err != nil {
			return l, errors.New("net: " + err.Error())
		}
		if l.NetMinor != l.GrossMinor-l.FeeMinor {
			return l, errors.New("net is not gross - fee")
		}
	}
	if l.OccurredAt, err = parseTime(get("occurred_on")); err != nil || l.OccurredAt.IsZero() {
		return l, errors.New("occurred_on must be an RFC 3339 time or a date (2026-10-31)")
	}
	for name, dst := range map[string]**time.Time{"available_on": &l.AvailableOn, "settled_on": &l.SettledAt} {
		if s := get(name); s != "" {
			t, err := parseTime(s)
			if err != nil {
				return l, errors.New(name + " must be an RFC 3339 time or a date")
			}
			*dst = &t
		}
	}
	return l, nil
}

func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	return t.UTC(), err
}

// parseMinor reads a decimal amount of a currency with exp fraction digits into minor units: "10.5" is 1050 for exponent 2. More fraction digits than the currency
// has are refused rather than rounded.
func parseMinor(s string, exp int) (int64, error) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" || len(frac) > exp {
		return 0, errors.New("not an amount with at most " + strconv.Itoa(exp) + " decimals")
	}
	digits := whole + frac + strings.Repeat("0", exp-len(frac))
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 || n > math.MaxInt64/2 {
		return 0, errors.New("not an amount")
	}
	if neg {
		n = -n
	}
	return n, nil
}
