package paddle

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/0furkancolak/quotify/pkg/payments"
)

// The calls below are written from Paddle's API reference (https://developer.paddle.com/api-reference). Where the reference does not
// settle a path, a field or a response shape, the code says UNVERIFIED and reads the response defensively; no sandbox has been run.

var (
	_ payments.Catalogue       = (*Client)(nil)
	_ payments.CustomerAPI     = (*Client)(nil)
	_ payments.SubscriptionAPI = (*Client)(nil)
	_ payments.DiscountAPI     = (*Client)(nil)
	_ payments.DocumentLinker  = (*Client)(nil)
)

type reply[T any] struct {
	Data T `json:"data"`
}

// SyncCatalogue creates the plan's product when it has none, and a recurring price for each requested key. A price in a currency Paddle
// cannot bill is skipped, with the reason. Prices are immutable at Paddle, which suits plan versions: a new version is new prices.
func (c *Client) SyncCatalogue(ctx context.Context, r payments.CatalogueRequest) (payments.CatalogueResult, error) {
	out := payments.CatalogueResult{ProductRef: r.ProductRef, Prices: map[string]string{}, Skipped: map[string]string{}}
	caps := Caps()
	if out.ProductRef == "" {
		tax := r.TaxCategory
		if tax == "" {
			tax = "saas"
		}
		body := map[string]any{"name": r.ProductName, "tax_category": tax, "custom_data": map[string]string{"quotify_plan_code": r.QuotifyPlanCode}}
		if r.Description != "" {
			body["description"] = r.Description
		}
		var p reply[struct {
			ID string `json:"id"`
		}]
		if err := c.do(ctx, http.MethodPost, "/products", body, &p); err != nil {
			return out, err
		}
		if !strings.HasPrefix(p.Data.ID, "pro_") {
			return out, errors.New("paddle: a product without an id")
		}
		out.ProductRef = p.Data.ID
	}
	for _, spec := range r.Prices {
		if !caps.CanCharge(spec.Currency) {
			out.Skipped[spec.Key] = "Paddle cannot bill in " + spec.Currency
			continue
		}
		if spec.Interval != "month" && spec.Interval != "year" {
			out.Skipped[spec.Key] = "interval must be month or year"
			continue
		}
		body := map[string]any{
			"product_id":    out.ProductRef,
			"description":   "Payrudder " + spec.Key,
			"unit_price":    money{Amount: strconv.FormatInt(spec.AmountMinor, 10), CurrencyCode: spec.Currency},
			"billing_cycle": map[string]any{"interval": spec.Interval, "frequency": 1},
			"quantity":      map[string]int{"minimum": 1, "maximum": 1},
			"tax_mode":      c.s.TaxMode,
			"custom_data":   map[string]string{"quotify_key": spec.Key},
		}
		if spec.Name != "" {
			body["name"] = spec.Name
		}
		var p reply[struct {
			ID string `json:"id"`
		}]
		if err := c.do(ctx, http.MethodPost, "/prices", body, &p); err != nil {
			return out, fmt.Errorf("price %s: %w", spec.Key, err)
		}
		if !strings.HasPrefix(p.Data.ID, "pri_") {
			return out, errors.New("paddle: a price without an id")
		}
		out.Prices[spec.Key] = p.Data.ID
	}
	return out, nil
}

// EnsureCustomer finds the customer by e-mail (GET /customers?email=) or creates it, then an address for the country and a business for the tax id.
// UNVERIFIED: the address and business bodies (country_code, postal_code, city, first_line; name, tax_identifier) and that a list of
// addresses can be matched by country. A customer that exists already is reused, and so are its address and business.
func (c *Client) EnsureCustomer(ctx context.Context, s payments.CustomerSpec) (payments.CustomerRefs, error) {
	var refs payments.CustomerRefs
	if s.Email == "" {
		return refs, errors.New("paddle: a customer needs an e-mail address")
	}
	var found reply[[]struct {
		ID string `json:"id"`
	}]
	if err := c.do(ctx, http.MethodGet, "/customers?email="+url.QueryEscape(s.Email), nil, &found); err != nil {
		return refs, err
	}
	if len(found.Data) > 0 {
		refs.CustomerRef = found.Data[0].ID
	} else {
		body := map[string]any{"email": s.Email, "custom_data": map[string]string{"quotify_customer_id": s.QuotifyCustomerID}}
		if s.Name != "" {
			body["name"] = s.Name
		}
		var created reply[struct {
			ID string `json:"id"`
		}]
		if err := c.do(ctx, http.MethodPost, "/customers", body, &created); err != nil {
			return refs, err
		}
		refs.CustomerRef = created.Data.ID
	}
	if !strings.HasPrefix(refs.CustomerRef, "ctm_") {
		return refs, errors.New("paddle: a customer without an id")
	}
	base := "/customers/" + url.PathEscape(refs.CustomerRef)
	if s.Country != "" {
		var list reply[[]struct {
			ID      string `json:"id"`
			Country string `json:"country_code"`
		}]
		if err := c.do(ctx, http.MethodGet, base+"/addresses", nil, &list); err != nil {
			return refs, err
		}
		for _, a := range list.Data {
			if a.Country == s.Country {
				refs.AddressRef = a.ID
			}
		}
		if refs.AddressRef == "" {
			body := map[string]any{"country_code": s.Country}
			for k, v := range map[string]string{"postal_code": s.PostalCode, "city": s.City, "first_line": s.Address} {
				if v != "" {
					body[k] = v
				}
			}
			var created reply[struct {
				ID string `json:"id"`
			}]
			if err := c.do(ctx, http.MethodPost, base+"/addresses", body, &created); err != nil {
				return refs, err
			}
			refs.AddressRef = created.Data.ID
		}
	}
	if s.TaxID != "" {
		var list reply[[]struct {
			ID  string `json:"id"`
			Tax string `json:"tax_identifier"`
		}]
		if err := c.do(ctx, http.MethodGet, base+"/businesses", nil, &list); err != nil {
			return refs, err
		}
		for _, b := range list.Data {
			if b.Tax == s.TaxID {
				refs.BusinessRef = b.ID
			}
		}
		if refs.BusinessRef == "" {
			name := s.Business
			if name == "" {
				name = s.Name
			}
			var created reply[struct {
				ID string `json:"id"`
			}]
			if err := c.do(ctx, http.MethodPost, base+"/businesses", map[string]any{"name": name, "tax_identifier": s.TaxID}, &created); err != nil {
				return refs, err
			}
			refs.BusinessRef = created.Data.ID
		}
	}
	return refs, nil
}

// PortalLink creates a customer portal session. UNVERIFIED: the path (POST /customers/{id}/portal-sessions), the subscription_ids
// field and the location of the link in the response (data.urls.general.overview).
func (c *Client) PortalLink(ctx context.Context, customerRef string, subscriptionRefs []string) (string, error) {
	if !strings.HasPrefix(customerRef, "ctm_") {
		return "", errors.New(`paddle: a customer id ("ctm_...") is needed`)
	}
	body := map[string]any{}
	if len(subscriptionRefs) > 0 {
		body["subscription_ids"] = subscriptionRefs
	}
	var out reply[struct {
		URLs struct {
			General struct {
				Overview string `json:"overview"`
			} `json:"general"`
		} `json:"urls"`
	}]
	if err := c.do(ctx, http.MethodPost, "/customers/"+url.PathEscape(customerRef)+"/portal-sessions", body, &out); err != nil {
		return "", err
	}
	if out.Data.URLs.General.Overview == "" {
		return "", errors.New("paddle: the portal session has no link")
	}
	return out.Data.URLs.General.Overview, nil
}

// prorationMode maps Quotify's timing and proration onto Paddle's proration_billing_mode. Paddle changes the items at once; it can bill
// the difference now or at the next renewal, but cannot hold an item change back until the end of the period. A change at the period end is
// refused rather than approximated: the customer would get the new plan at once while the caller believed they would not.
func prorationMode(timing, proration string) (string, error) {
	switch {
	case timing == "period_end":
		return "", fmt.Errorf("%w: Paddle applies an item change at once and cannot schedule it for the end of the period; change now with proration none (billed from the next renewal)", payments.ErrUnsupported)
	case proration == "" || proration == "prorate":
		return "prorated_immediately", nil
	case proration == "full":
		return "full_immediately", nil
	case proration == "none":
		return "full_next_billing_period", nil
	}
	return "", fmt.Errorf("paddle: unknown proration %q", proration)
}

func changeBody(ch payments.ProviderChange) (map[string]any, error) {
	mode, err := prorationMode(ch.Timing, ch.Proration)
	if err != nil {
		return nil, err
	}
	if len(ch.Items) == 0 {
		return nil, errors.New("paddle: a change needs at least one item")
	}
	items := make([]map[string]any, 0, len(ch.Items))
	for _, it := range ch.Items {
		if !strings.HasPrefix(it.PriceRef, "pri_") {
			return nil, errors.New(`paddle: price must be a Paddle price id ("pri_...")`)
		}
		q := it.Quantity
		if q <= 0 {
			q = 1
		}
		items = append(items, map[string]any{"price_id": it.PriceRef, "quantity": q})
	}
	return map[string]any{"items": items, "proration_billing_mode": mode, "on_payment_failure": "prevent_change"}, nil
}

func subPath(ref string) (string, error) {
	if !strings.HasPrefix(ref, "sub_") {
		return "", errors.New(`paddle: a subscription id ("sub_...") is needed`)
	}
	return "/subscriptions/" + url.PathEscape(ref), nil
}

func minorOf(m money) int64 { return minor(m.Amount) }

// PreviewChange asks Paddle what a change would bill (PATCH /subscriptions/{id}/preview). UNVERIFIED: the inner shape of update_summary
// ({credit, charge, result: {action, amount}}) and of recurring_transaction_details; fields that are absent read as zero.
func (c *Client) PreviewChange(ctx context.Context, ref string, ch payments.ProviderChange) (payments.ChangePreview, error) {
	path, err := subPath(ref)
	if err != nil {
		return payments.ChangePreview{}, err
	}
	body, err := changeBody(ch)
	if err != nil {
		return payments.ChangePreview{}, err
	}
	var out reply[struct {
		CurrencyCode  string  `json:"currency_code"`
		NextBilledAt  *string `json:"next_billed_at"`
		UpdateSummary *struct {
			Credit money `json:"credit"`
			Charge money `json:"charge"`
			Result struct {
				Action string `json:"action"`
				Amount string `json:"amount"`
			} `json:"result"`
		} `json:"update_summary"`
		Recurring *struct {
			Totals struct {
				Total string `json:"total"`
			} `json:"totals"`
		} `json:"recurring_transaction_details"`
	}]
	if err := c.do(ctx, http.MethodPatch, path+"/preview", body, &out); err != nil {
		return payments.ChangePreview{}, err
	}
	p := payments.ChangePreview{Currency: out.Data.CurrencyCode}
	if s := out.Data.UpdateSummary; s != nil {
		p.CreditMinor, p.ChargeMinor = minorOf(s.Credit), minorOf(s.Charge)
		p.ResultMinor = minor(s.Result.Amount)
		if s.Result.Action == "credit" {
			p.ResultMinor = -p.ResultMinor
		}
		if p.Currency == "" {
			p.Currency = s.Charge.CurrencyCode
		}
	}
	if out.Data.Recurring != nil {
		p.NextAmountMinor = minor(out.Data.Recurring.Totals.Total)
	}
	if out.Data.NextBilledAt != nil {
		if t, err := time.Parse(time.RFC3339, *out.Data.NextBilledAt); err == nil {
			p.NextBilledAt = &t
		}
	}
	return p, nil
}

// ApplyChange changes the items of a subscription (PATCH /subscriptions/{id}). A payment that fails prevents the change (on_payment_failure).
func (c *Client) ApplyChange(ctx context.Context, ref string, ch payments.ProviderChange) error {
	path, err := subPath(ref)
	if err != nil {
		return err
	}
	body, err := changeBody(ch)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPatch, path, body, nil)
}

// Pause pauses a subscription at once or at the end of the billing period.
func (c *Client) Pause(ctx context.Context, ref string, atPeriodEnd bool, resumeAt *time.Time) error {
	path, err := subPath(ref)
	if err != nil {
		return err
	}
	body := map[string]any{"effective_from": "immediately"}
	if atPeriodEnd {
		body["effective_from"] = "next_billing_period"
	}
	if resumeAt != nil {
		body["resume_at"] = resumeAt.UTC().Format(time.RFC3339)
	}
	return c.do(ctx, http.MethodPost, path+"/pause", body, nil)
}

// Resume resumes a paused subscription now, in a new billing period (Paddle's default), which charges it at once.
func (c *Client) Resume(ctx context.Context, ref string) error {
	path, err := subPath(ref)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, path+"/resume", map[string]string{"effective_from": "immediately"}, nil)
}

// PaymentMethodLink returns the checkout where the customer updates the subscription's payment method
// (GET /subscriptions/{id}/update-payment-method-transaction). UNVERIFIED: the response carries the page at data.checkout.url.
func (c *Client) PaymentMethodLink(ctx context.Context, ref string) (string, error) {
	path, err := subPath(ref)
	if err != nil {
		return "", err
	}
	var out reply[struct {
		Checkout *struct {
			URL string `json:"url"`
		} `json:"checkout"`
	}]
	if err := c.do(ctx, http.MethodGet, path+"/update-payment-method-transaction", nil, &out); err != nil {
		return "", err
	}
	if out.Data.Checkout == nil || out.Data.Checkout.URL == "" {
		return "", errors.New("paddle: the payment method transaction has no checkout url")
	}
	return out.Data.Checkout.URL, nil
}

// CreateDiscount creates a coupon (POST /discounts). A percentage amount is "10" for 10 %; a flat amount is minor units of the currency.
func (c *Client) CreateDiscount(ctx context.Context, d payments.DiscountSpec) (payments.DiscountRefs, error) {
	switch d.Type {
	case "percentage":
	case "flat":
		if d.Currency == "" {
			return payments.DiscountRefs{}, errors.New("paddle: a flat discount needs a currency")
		}
	default:
		return payments.DiscountRefs{}, errors.New(`paddle: discount type must be "percentage" or "flat"`)
	}
	body := map[string]any{"description": d.Description, "type": d.Type, "amount": d.Amount, "enabled_for_checkout": true, "recur": d.Recurring}
	if d.Type == "flat" {
		body["currency_code"] = d.Currency
	}
	if d.Code != "" {
		body["code"] = d.Code
	}
	if d.Recurring && d.MaxIntervals > 0 {
		body["maximum_recurring_intervals"] = d.MaxIntervals
	}
	if d.UsageLimit > 0 {
		body["usage_limit"] = d.UsageLimit
	}
	if d.ExpiresAt != nil {
		body["expires_at"] = d.ExpiresAt.UTC().Format(time.RFC3339)
	}
	var out reply[struct {
		ID   string `json:"id"`
		Code string `json:"code"`
	}]
	if err := c.do(ctx, http.MethodPost, "/discounts", body, &out); err != nil {
		return payments.DiscountRefs{}, err
	}
	if !strings.HasPrefix(out.Data.ID, "dsc_") {
		return payments.DiscountRefs{}, errors.New("paddle: a discount without an id")
	}
	return payments.DiscountRefs{DiscountRef: out.Data.ID, Code: out.Data.Code}, nil
}

// InvoiceURL returns the PDF of the invoice Paddle issued for a transaction (GET /transactions/{id}/invoice). UNVERIFIED: the link is at data.url.
func (c *Client) InvoiceURL(ctx context.Context, transactionRef string) (string, error) {
	if !strings.HasPrefix(transactionRef, "txn_") {
		return "", errors.New(`paddle: a transaction id ("txn_...") is needed`)
	}
	var out reply[struct {
		URL string `json:"url"`
	}]
	if err := c.do(ctx, http.MethodGet, "/transactions/"+url.PathEscape(transactionRef)+"/invoice", nil, &out); err != nil {
		return "", err
	}
	if out.Data.URL == "" {
		return "", errors.New("paddle: the transaction has no invoice link")
	}
	return out.Data.URL, nil
}
