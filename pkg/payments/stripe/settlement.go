package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Settlement implements payments.SettlementReader from Stripe's balance transactions and payouts (docs.stripe.com/api/balance_transactions,
// docs.stripe.com/api/payouts, docs.stripe.com/reports/reporting-categories, read 2026-11-02):
//
//   - GET /v1/balance_transactions (created[gte]=since, expand[]=data.source): a balance transaction with reporting_category "charge" (type charge for a card,
//     payment for another method) is the money of a payment: amount (gross), fee, net, available_on, and status "available" or "pending". Its source is the
//     charge; expanded, the charge names its payment_intent, which is the reference the payment ledger holds for a Stripe payment. Refunds, disputes and fees are
//     other reporting categories and are not read yet.
//   - GET /v1/payouts?status=paid: a paid payout has arrival_date. Only when its reconciliation_status is "completed" can its balance transactions be listed
//     (GET /v1/balance_transactions?payout=po_...): those are the payments that went out in it, and they are marked settled on the arrival date, in that batch.
//     Stripe lists them for standard automatic payouts only; a manual or instant payout is read as a payout line and its payments stay unsettled here.
//
// UNVERIFIED against a real account: nothing here ran against Stripe, only against a fake server written from those pages. In particular whether expand[]=data.source
// is honoured on this list, and the fields of a charge that is not a PaymentIntent's (then the charge id is the reference).
func (c *Client) Settlement(ctx context.Context, since time.Time) ([]payments.SettlementLine, error) {
	lines := map[string]payments.SettlementLine{}
	var order []string
	put := func(l payments.SettlementLine) {
		if _, ok := lines[l.Ref]; !ok {
			order = append(order, l.Ref)
		}
		lines[l.Ref] = l
	}
	created := strconv.FormatInt(since.Unix(), 10)

	err := c.pages(ctx, "/v1/balance_transactions", url.Values{"created[gte]": {created}, "expand[]": {"data.source"}}, func(raw json.RawMessage) error {
		if l, ok, err := paymentLine(raw); err != nil || !ok {
			return err
		} else if _, seen := lines[l.Ref]; !seen {
			put(l)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var payouts []payout
	err = c.pages(ctx, "/v1/payouts", url.Values{"status": {"paid"}, "created[gte]": {created}}, func(raw json.RawMessage) error {
		var p payout
		if err := json.Unmarshal(raw, &p); err != nil {
			return fmt.Errorf("stripe answered garbage: %w", err)
		}
		payouts = append(payouts, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, p := range payouts {
		arrival := time.Unix(p.ArrivalDate, 0).UTC()
		put(payments.SettlementLine{Ref: p.ID, Kind: "payout", Currency: strings.ToUpper(p.Currency), GrossMinor: p.Amount, NetMinor: p.Amount,
			OccurredAt: time.Unix(p.Created, 0).UTC(), SettledAt: &arrival, Batch: p.ID})
		if p.ReconciliationStatus != "completed" {
			continue
		}
		err := c.pages(ctx, "/v1/balance_transactions", url.Values{"payout": {p.ID}, "expand[]": {"data.source"}}, func(raw json.RawMessage) error {
			l, ok, err := paymentLine(raw)
			if err != nil || !ok {
				return err
			}
			l.SettledAt, l.Batch = &arrival, p.ID
			put(l)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	out := make([]payments.SettlementLine, len(order))
	for i, ref := range order {
		out[i] = lines[ref]
	}
	return out, nil
}

type payout struct {
	ID                   string `json:"id"`
	Amount               int64  `json:"amount"`
	Currency             string `json:"currency"`
	Created              int64  `json:"created"`
	ArrivalDate          int64  `json:"arrival_date"`
	ReconciliationStatus string `json:"reconciliation_status"`
}

// paymentLine reads a balance transaction that is the money of a payment; ok is false for the others (fees, refunds, payouts, transfers, ...).
func paymentLine(raw json.RawMessage) (payments.SettlementLine, bool, error) {
	var bt struct {
		ID                string          `json:"id"`
		Amount            int64           `json:"amount"`
		Fee               int64           `json:"fee"`
		Net               int64           `json:"net"`
		Currency          string          `json:"currency"`
		Created           int64           `json:"created"`
		AvailableOn       int64           `json:"available_on"`
		ReportingCategory string          `json:"reporting_category"`
		Source            json.RawMessage `json:"source"`
	}
	if err := json.Unmarshal(raw, &bt); err != nil {
		return payments.SettlementLine{}, false, fmt.Errorf("stripe answered garbage: %w", err)
	}
	if bt.ReportingCategory != "charge" {
		return payments.SettlementLine{}, false, nil
	}
	ref := sourceReference(bt.Source)
	if bt.ID == "" || ref == "" {
		return payments.SettlementLine{}, false, nil
	}
	l := payments.SettlementLine{Ref: bt.ID, Kind: "payment", PaymentRef: ref, Currency: strings.ToUpper(bt.Currency), GrossMinor: bt.Amount, FeeMinor: bt.Fee, NetMinor: bt.Net,
		OccurredAt: time.Unix(bt.Created, 0).UTC()}
	if bt.AvailableOn > 0 {
		at := time.Unix(bt.AvailableOn, 0).UTC()
		l.AvailableOn = &at
	}
	return l, true, nil
}

// sourceReference is the reference the payment ledger holds for the payment a balance transaction is about: the PaymentIntent of the expanded charge, else the
// charge's own id (a charge made without a PaymentIntent), else a source that was not expanded (the id).
func sourceReference(source json.RawMessage) string {
	var id string
	if json.Unmarshal(source, &id) == nil {
		return id
	}
	var charge struct {
		ID            string `json:"id"`
		PaymentIntent string `json:"payment_intent"`
	}
	if json.Unmarshal(source, &charge) != nil {
		return ""
	}
	if charge.PaymentIntent != "" {
		return charge.PaymentIntent
	}
	return charge.ID
}

// maxSettlementPages bounds one listing: 100 objects a page, so 20,000 objects before the caller must read a shorter window.
const maxSettlementPages = 200

// pages reads a Stripe list (has_more, starting_after the last id) and hands each object to fn.
func (c *Client) pages(ctx context.Context, path string, params url.Values, fn func(json.RawMessage) error) error {
	q := url.Values{"limit": {"100"}}
	for k, v := range params {
		q[k] = v
	}
	for page := 0; page < maxSettlementPages; page++ {
		var out struct {
			HasMore bool              `json:"has_more"`
			Data    []json.RawMessage `json:"data"`
		}
		if err := c.do(ctx, http.MethodGet, path, q, "", &out); err != nil {
			return err
		}
		last := ""
		for _, raw := range out.Data {
			if err := fn(raw); err != nil {
				return err
			}
			var id struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &id) == nil {
				last = id.ID
			}
		}
		if !out.HasMore || last == "" {
			return nil
		}
		q.Set("starting_after", last)
	}
	return errors.New("stripe: more than 20,000 objects in one settlement read; read a shorter window")
}
