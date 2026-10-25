package main

import (
	"fmt"
	"hash/fnv"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ---- the data -------------------------------------------------------------------------------------------------

type seedCustomer struct {
	Ext, Name, Email string
	Scenario         string // sandbox payment scenario; "" = no payment method on file
	Plan             string
	Weight           int // percent of the baseline usage
	Advance          bool
}

type seedPlan struct {
	Code, Name string
	Base       string
	Prices     []any
	Limits     bool // entitlements with alerts
}

type seedExpense struct {
	Vendor, Category, Currency, Note string
	Amount                           int64
	DaysAgo                          int
	Recurring                        string
}

type seedPack struct {
	Name         string
	Credits      int64
	Price        int64
	ValidityDays int
}

type seedData struct {
	Meters    []map[string]any
	Plans     []seedPlan
	Customers []seedCustomer
	Expenses  []seedExpense
	Pack      *seedPack
	AdhocMin  int64 // the ad-hoc payment link's amount in minor units
	Usage     map[string]int64
	Webhook   string
}

// dataFor builds the sample data of an app profile. Money is in minor units of the app's currency; the prices are in the
// scale a shop of that currency would use, so the dashboards show plausible numbers.
func dataFor(profile, currency string) seedData {
	switch profile {
	case "store":
		return seedData{
			Meters: stdMeters(),
			Plans: []seedPlan{
				{Code: "starter", Name: "Starter", Base: "29900"},
				{Code: "pro", Name: "Pro", Base: "99900", Limits: true, Prices: []any{
					tiered("api_calls", []any{tier("10000", "0"), tier("", "0.5")}), perUnit("storage_gb", "250")}},
				{Code: "scale", Name: "Scale (usage based)", Base: "0", Limits: true, Prices: []any{
					tiered("api_calls", []any{tier("50000", "0.8"), tier("", "0.45")}), perUnit("storage_gb", "180")}},
			},
			Customers: []seedCustomer{
				{"dev_cus_01", "Anadolu Kahve", "billing@anadolukahve.example.com", "success", "pro", 100, false},
				{"dev_cus_02", "Bosphorus Books", "ap@bosphorusbooks.example.com", "success", "scale", 160, false},
				{"dev_cus_03", "Ege Ceramics", "hello@egeceramics.example.com", "success", "starter", 20, true},
				{"dev_cus_04", "Kapadokya Tours", "finance@kapadokyatours.example.com", "declined", "pro", 70, false},
				{"dev_cus_05", "Marmara Textiles", "ops@marmaratextiles.example.com", "insufficient_funds_once", "scale", 120, false},
				{"dev_cus_06", "Taksim Sneakers", "pay@taksimsneakers.example.com", "success", "pro", 90, false},
				{"dev_cus_07", "Izmir Gadgets", "team@izmirgadgets.example.com", "", "starter", 15, false},
			},
			Expenses: []seedExpense{
				{"Hetzner Cloud", "infrastructure", "TRY", "Production cluster", 214000, 80, "monthly"},
				{"Google Ads", "marketing", "TRY", "Spring campaign", 200000, 40, "none"},
				{"Figma", "software", "TRY", "Design seats", 45000, 33, "monthly"},
				{"Mali Musavir Ofisi", "other", "TRY", "Bookkeeping", 150000, 21, "none"},
				{"Kira ve Aidat", "other", "TRY", "Co-working desks", 240000, 14, "monthly"},
				{"Iyzico", "payment_fees", "TRY", "Card processing fees", 38650, 9, "none"},
				{"Vercel", "software", "USD", "Pro plan", 2000, 28, "monthly"},
				{"Anthropic API", "infrastructure", "USD", "Support bot usage", 14800, 12, "none"},
				{"Linear", "software", "USD", "Issue tracker", 1600, 5, "none"},
			},
			Pack:     &seedPack{"1,000 credits", 1000, 49900, 365},
			AdhocMin: 150000,
			Usage:    map[string]int64{"api_calls": 4200, "storage_gb": 12},
			Webhook:  "https://hooks.example.com/payrudder/storefront",
		}
	case "game":
		return seedData{
			Meters: stdMeters(),
			Plans: []seedPlan{
				{Code: "indie", Name: "Indie", Base: "900"},
				{Code: "studio", Name: "Studio", Base: "4900", Limits: true, Prices: []any{
					tiered("api_calls", []any{tier("100000", "0"), tier("", "0.004")}), perUnit("storage_gb", "9")}},
				{Code: "live_ops", Name: "Live Ops (usage based)", Base: "0", Limits: true, Prices: []any{
					tiered("api_calls", []any{tier("200000", "0.006"), tier("", "0.0035")}), perUnit("storage_gb", "6")}},
			},
			Customers: []seedCustomer{
				{"dev_cus_01", "Pixel Pirates", "billing@pixelpirates.example.com", "success", "studio", 100, false},
				{"dev_cus_02", "Nova Arcade", "finance@novaarcade.example.com", "success", "live_ops", 180, false},
				{"dev_cus_03", "Lumen Games", "pay@lumengames.example.com", "success", "indie", 25, false},
				{"dev_cus_04", "Tiny Tanks", "ap@tinytanks.example.com", "declined", "studio", 60, false},
				{"dev_cus_05", "Rogue Waves", "hello@roguewaves.example.com", "error_then_success", "live_ops", 130, false},
				{"dev_cus_06", "Drift Kings", "team@driftkings.example.com", "success", "studio", 85, false},
			},
			Expenses: []seedExpense{
				{"AWS", "infrastructure", "USD", "Game servers", 28000, 70, "monthly"},
				{"Unity", "software", "USD", "Pro seats", 6500, 55, "monthly"},
				{"Apple Developer", "other", "USD", "Annual program", 9900, 50, "yearly"},
				{"Meta Ads", "marketing", "USD", "Launch campaign", 90000, 26, "none"},
				{"Sentry", "software", "USD", "Team plan", 8900, 18, "monthly"},
				{"Contractors", "payroll", "USD", "Level design sprint", 150000, 10, "none"},
				{"Turkcell Kurumsal", "other", "TRY", "Office lines", 129000, 20, "monthly"},
				{"Stripe", "payment_fees", "EUR", "EU card fees", 41200, 8, "none"},
			},
			Pack:     &seedPack{"500 gems", 500, 499, 0},
			AdhocMin: 4900,
			Usage:    map[string]int64{"api_calls": 90000, "storage_gb": 40},
			Webhook:  "https://hooks.example.com/payrudder/mobile-game",
		}
	default: // "mini": a tiny second tenant to look at isolation
		return seedData{
			Meters: stdMeters(),
			Plans:  []seedPlan{{Code: "basic", Name: "Basic", Base: "900", Prices: []any{perUnit("api_calls", "0.01")}}},
			Customers: []seedCustomer{
				{"dev_cus_01", "Hafen Cafe", "hello@hafencafe.example.com", "success", "basic", 100, false},
				{"dev_cus_02", "Spree Studio", "pay@spreestudio.example.com", "declined", "basic", 50, false},
			},
			Expenses: []seedExpense{{"Hetzner Cloud", "infrastructure", "EUR", "Sync servers", 3900, 30, "monthly"}},
			AdhocMin: 2500,
			Usage:    map[string]int64{"api_calls": 800, "storage_gb": 1},
		}
	}
}

func stdMeters() []map[string]any {
	return []map[string]any{
		{"code": "api_calls", "aggregation": "AGGREGATION_SUM", "value_property": "calls", "quantity_scale": 0, "unit_label": "calls"},
		{"code": "storage_gb", "aggregation": "AGGREGATION_SUM", "value_property": "gb", "quantity_scale": 3, "unit_label": "GB"},
	}
}

func tier(upTo, unit string) map[string]any {
	t := map[string]any{"unit_price": unit}
	if upTo != "" {
		t["up_to"] = upTo
	}
	return t
}
func tiered(meter string, tiers []any) map[string]any {
	return map[string]any{"meter_code": meter, "tiered": map[string]any{"tiers": tiers}}
}
func perUnit(meter, price string) map[string]any {
	return map[string]any{"meter_code": meter, "per_unit": map[string]any{"unit_price": price}}
}

// ---- seeding one sandbox tenant -------------------------------------------------------------------------------------

type tenantSeed struct {
	s       *devSeeder
	label   string
	tenant  string
	session string
	app     devApp
	appID   string
	data    seedData
}

func (t *tenantSeed) logf(format string, a ...any) {
	t.s.logf("%s: "+format, append([]any{t.label}, a...)...)
}

// call acts on the app's sandbox tenant with the person's session.
func (t *tenantSeed) call(method, path string, in, out any, extra ...string) error {
	h := map[string]string{"Authorization": "Bearer " + t.session, "X-Payrudder-Tenant": t.tenant}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}
	return t.s.do(method, t.s.base, path, h, in, out)
}

// list reads every page of a list route.
func (t *tenantSeed) list(path, key string) ([]map[string]any, error) {
	var all []map[string]any
	token := ""
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	for range 50 {
		p := path + sep + "page_size=200"
		if token != "" {
			p += "&page_token=" + url.QueryEscape(token)
		}
		var page map[string]any
		if err := t.call("GET", p, nil, &page); err != nil {
			return nil, err
		}
		if items, ok := page[key].([]any); ok {
			for _, it := range items {
				if m, ok := it.(map[string]any); ok {
					all = append(all, m)
				}
			}
		}
		token, _ = page["next_page_token"].(string)
		if token == "" {
			break
		}
	}
	return all, nil
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func (t *tenantSeed) seed() error {
	t.data = dataFor(t.app.Profile, t.app.Currency)
	steps := []struct {
		name string
		fn   func() error
	}{
		{"meters", t.meters}, {"packages", t.plans}, {"customers", t.customers}, {"subscriptions", t.subscriptions},
		{"usage", t.usage}, {"invoices", t.invoices}, {"credit notes", t.creditNotes}, {"credits", t.credits},
		{"payment links", t.links}, {"expenses", t.expenses}, {"webhook", t.webhook},
	}
	for _, st := range steps {
		if err := st.fn(); err != nil {
			return fmt.Errorf("%s: %w", st.name, err)
		}
	}
	return nil
}

func (t *tenantSeed) meters() error {
	have, err := t.list("/v1/meters", "meters")
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, m := range have {
		exists[str(m, "code")] = true
	}
	for _, m := range t.data.Meters {
		if !exists[m["code"].(string)] {
			if err := t.call("POST", "/v1/meters", m, nil); err != nil && statusOf(err) != 409 {
				return err
			}
			t.logf("meter %s created", m["code"])
		}
	}
	return nil
}

func (t *tenantSeed) plans() error {
	have, err := t.list("/v1/plans", "plans")
	if err != nil {
		return err
	}
	plans := map[string]map[string]any{}
	for _, p := range have {
		plans[str(p, "code")] = p
	}
	for _, p := range t.data.Plans {
		if plans[p.Code] == nil {
			if err := t.call("POST", "/v1/plans", map[string]any{"code": p.Code, "name": p.Name}, nil); err != nil && statusOf(err) != 409 {
				return err
			}
			plans[p.Code] = map[string]any{}
		}
		if v, _ := plans[p.Code]["latest_version"].(float64); v >= 1 {
			continue
		}
		body := map[string]any{"base_fee_minor": p.Base, "prices": p.Prices}
		if p.Prices == nil {
			body["prices"] = []any{}
		}
		if p.Limits {
			body["entitlements"] = []any{map[string]any{"meter_code": "api_calls", "limit": fmt.Sprint(t.data.Usage["api_calls"] * 30), "alert_thresholds_pct": []int{80, 100}}}
		}
		if err := t.call("POST", "/v1/plans/"+p.Code+"/versions", body, nil); err != nil {
			return fmt.Errorf("plan %s: %w", p.Code, err)
		}
		t.logf("package %s created", p.Code)
	}
	return nil
}

func (t *tenantSeed) customers() error {
	have, err := t.list("/v1/customers", "customers")
	if err != nil {
		return err
	}
	exists := map[string]bool{}
	for _, c := range have {
		exists[str(c, "external_id")] = true
	}
	for _, c := range t.data.Customers {
		if exists[c.Ext] {
			continue
		}
		body := map[string]any{"external_id": c.Ext, "name": c.Name, "email": c.Email}
		if c.Scenario != "" {
			body["payment_method"] = "pm_sandbox_" + c.Scenario
		}
		if err := t.call("POST", "/v1/customers", body, nil); err != nil && statusOf(err) != 409 {
			return err
		}
		t.logf("customer %s created", c.Name)
	}
	return nil
}

func (t *tenantSeed) subscriptions() error {
	have, err := t.list("/v1/subscriptions", "subscriptions")
	if err != nil {
		return err
	}
	subscribed := map[string]bool{}
	for _, s := range have {
		subscribed[str(s, "customer_external_id")] = true
	}
	for _, c := range t.data.Customers {
		if subscribed[c.Ext] {
			continue
		}
		timing := "FEE_TIMING_ARREARS"
		if c.Advance {
			timing = "FEE_TIMING_ADVANCE"
		}
		if err := t.call("POST", "/v1/subscriptions", map[string]any{"customer_external_id": c.Ext, "plan_code": c.Plan, "fee_timing": timing}, nil); err != nil && statusOf(err) != 409 {
			return err
		}
		t.logf("%s subscribed to %s", c.Name, c.Plan)
	}
	return nil
}

// jitter is a deterministic pseudo-random number in [0, n) from a key, so a re-run builds the same events.
func jitter(key string, n int64) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64() % uint64(n)) //nolint:gosec // n > 0 and small
}

// usageEvents builds one event per customer, meter and past day of the current month (UTC), at noon. Keys and payloads
// depend only on the day, the customer and the meter, so sending them again is a duplicate, never a second event.
func usageEvents(now time.Time, data seedData) []map[string]any {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	var evs []map[string]any
	for day := start; day.Before(now); day = day.AddDate(0, 0, 1) {
		at := day.Add(12 * time.Hour)
		if at.After(now.Add(-time.Hour)) {
			continue
		}
		for _, c := range data.Customers {
			for _, meter := range []string{"api_calls", "storage_gb"} {
				key := fmt.Sprintf("devseed:%s:%s:%s", day.Format("20060102"), c.Ext, meter)
				base := data.Usage[meter]
				q := base * int64(c.Weight) / 100
				q = q*(60+jitter(key, 80))/100 + 1 // 60% to 140% of the baseline
				qty := fmt.Sprint(q)
				if meter == "storage_gb" {
					qty = fmt.Sprintf("%d.%03d", q, jitter(key+"f", 1000))
				}
				evs = append(evs, map[string]any{"idempotency_key": key, "customer_external_id": c.Ext, "meter_code": meter,
					"quantity": qty, "event_time": at.Format(time.RFC3339)})
			}
		}
	}
	return evs
}

func (t *tenantSeed) usage() error {
	evs := usageEvents(t.s.o.Now(), t.data)
	sent := 0
	for len(evs) > 0 {
		n := min(len(evs), 400)
		var res struct {
			Results []map[string]any `json:"results"`
		}
		if err := t.call("POST", "/v1/events", map[string]any{"events": evs[:n]}, &res); err != nil {
			return err
		}
		for _, r := range res.Results {
			if st := str(r, "status"); st != "" && !strings.EqualFold(st, "duplicate") && !strings.Contains(strings.ToLower(st), "reject") {
				sent++
			}
		}
		evs = evs[n:]
	}
	if sent > 0 {
		t.logf("%d usage events accepted", sent)
	}
	return nil
}

func (t *tenantSeed) invoicesAll() ([]map[string]any, error) {
	return t.list("/v1/invoices", "invoices")
}

// invoices closes the current period once (sandbox), so there are finalized invoices with every payment outcome. A tenant
// that already has a REGULAR invoice is left alone: closing again would bill nothing new.
func (t *tenantSeed) invoices() error {
	inv, err := t.invoicesAll()
	if err != nil {
		return err
	}
	for _, i := range inv {
		if str(i, "kind") == "REGULAR" {
			return nil
		}
	}
	t.logf("closing the current period (sandbox): invoices and payments run through the real workers")
	if err := t.call("POST", "/v1/sandbox/close-period", map[string]any{}, nil); err != nil {
		return err
	}
	want := 0 // a customer billed in advance gets a FEE invoice instead of a REGULAR one
	for _, c := range t.data.Customers {
		if !c.Advance {
			want++
		}
	}
	deadline := t.s.o.Now().Add(t.s.o.Timeout)
	for {
		inv, err = t.invoicesAll()
		if err != nil {
			return err
		}
		regular, unsettled := 0, 0
		for _, i := range inv {
			if str(i, "kind") == "REGULAR" {
				regular++
			}
			if !invoiceSettled(i) {
				unsettled++
			}
		}
		if regular >= want && unsettled == 0 {
			return nil
		}
		if t.s.o.Now().After(deadline) {
			return fmt.Errorf("the invoices did not settle in time (%d regular of %d, %d still moving)", regular, want, unsettled)
		}
		time.Sleep(t.s.o.PollWait)
	}
}

// invoiceSettled is true once the saga has nothing more to do without outside help: paid, or declined and waiting for dunning.
func invoiceSettled(inv map[string]any) bool {
	switch str(inv, "saga_state") {
	case "COMPLETED", "VOIDED", "PAYMENT_DECLINED", "AWAITING_PAYMENT":
		return true
	}
	switch str(inv, "status") {
	case "paid", "void", "uncollectible":
		return true
	}
	return false
}

func (t *tenantSeed) creditNotes() error {
	if t.app.Profile == "mini" {
		return nil
	}
	notes, err := t.list("/v1/credit-notes", "credit_notes")
	if err != nil {
		return err
	}
	if len(notes) > 0 {
		return nil
	}
	inv, err := t.invoicesAll()
	if err != nil {
		return err
	}
	var paid []map[string]any
	for _, i := range inv {
		if str(i, "status") == "paid" && str(i, "kind") == "REGULAR" {
			paid = append(paid, i)
		}
	}
	sort.Slice(paid, func(a, b int) bool { return str(paid[a], "number") < str(paid[b], "number") })
	for n, refund := range []bool{true, false} {
		if n >= len(paid) {
			break
		}
		total := parseMinor(str(paid[n], "total_minor"))
		body := map[string]any{"tenant_id": t.tenant, "amount_minor": fmt.Sprint(max(total/4, 1)), "refund_to_card": refund,
			"reason": map[bool]string{true: "Partial refund: duplicate seat billed", false: "Goodwill credit after an outage"}[refund]}
		if err := t.s.operator("POST", "/admin/v1/invoices/"+str(paid[n], "id")+"/credit-notes", body, nil); err != nil {
			return err
		}
		t.logf("credit note on %s (%s)", str(paid[n], "number"), map[bool]string{true: "refund to card", false: "to credit balance"}[refund])
	}
	return nil
}

func parseMinor(s string) int64 {
	var n int64
	_, _ = fmt.Sscan(s, &n)
	return n
}

func (t *tenantSeed) credits() error {
	p := t.data.Pack
	if p == nil {
		return nil
	}
	packs, err := t.list("/v1/credit-packs", "packs")
	if err != nil {
		return err
	}
	have := false
	for _, k := range packs {
		if str(k, "name") == p.Name {
			have = true
		}
	}
	if !have {
		body := map[string]any{"name": p.Name, "credits": fmt.Sprint(p.Credits), "price_minor": fmt.Sprint(p.Price), "currency": t.app.Currency}
		if p.ValidityDays > 0 {
			body["validity_days"] = p.ValidityDays
		}
		if err := t.call("POST", "/v1/credit-packs", body, nil); err != nil {
			return err
		}
		t.logf("credit pack %q created", p.Name)
	}
	// Grants are idempotent by key. A consume that leaves a customer under the threshold announces credits.low.
	if err := t.call("PUT", "/v1/credits/settings", map[string]any{"low_threshold": 300}, nil); err != nil {
		return err
	}
	grants := []struct {
		cus, key, source string
		credits          int
		consume          int
	}{
		{t.data.Customers[0].Ext, "devseed-grant-1", "promo", 2500, 600},
		{t.data.Customers[2].Ext, "devseed-grant-2", "manual", 400, 150},
	}
	for _, g := range grants {
		var res map[string]any
		if err := t.call("POST", "/v1/customers/"+g.cus+"/credits/grant", map[string]any{"credits": fmt.Sprint(g.credits), "source": g.source,
			"reason": "Dev seed starter credits", "idempotency_key": g.key}, &res); err != nil {
			return err
		}
		if r, _ := res["replayed"].(bool); !r {
			t.logf("%d credits granted to %s", g.credits, g.cus)
		}
		if err := t.call("POST", "/v1/customers/"+g.cus+"/credits/consume", map[string]any{"credits": fmt.Sprint(g.consume),
			"reason": "Dev seed usage", "idempotency_key": g.key + "-use"}, nil); err != nil && statusOf(err) != 409 {
			return err
		}
	}
	return nil
}

func (t *tenantSeed) links() error {
	links, err := t.list("/v1/payment-links", "links")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, l := range links {
		have[str(l, "idempotency_key")] = true
	}
	create := func(key string, body map[string]any) error {
		if have[key] {
			return nil
		}
		body["idempotency_key"] = key
		if err := t.call("POST", "/v1/payment-links", body, nil, "Idempotency-Key", key); err != nil && statusOf(err) != 409 {
			return err
		}
		t.logf("payment link %s created", key)
		return nil
	}
	if err := create("devseed-link-adhoc", map[string]any{"kind": "adhoc", "amount_minor": fmt.Sprint(t.data.AdhocMin), "currency": t.app.Currency,
		"description": "Onboarding workshop", "customer_external_id": t.data.Customers[0].Ext}); err != nil {
		return err
	}
	if t.data.Pack != nil {
		packs, err := t.list("/v1/credit-packs", "packs")
		if err != nil {
			return err
		}
		for _, k := range packs {
			if str(k, "name") == t.data.Pack.Name {
				if err := create("devseed-link-pack", map[string]any{"kind": "credit_pack", "pack_id": str(k, "id"), "quantity": 2,
					"customer_external_id": t.data.Customers[1].Ext}); err != nil {
					return err
				}
			}
		}
	}
	inv, err := t.invoicesAll()
	if err != nil {
		return err
	}
	var open []map[string]any
	for _, i := range inv {
		if str(i, "status") == "open" {
			open = append(open, i)
		}
	}
	sort.Slice(open, func(a, b int) bool { return str(open[a], "number") < str(open[b], "number") })
	if len(open) > 0 && !have["devseed-link-invoice"] {
		anyInvoiceLink := false
		for _, l := range links {
			if str(l, "kind") == "invoice" {
				anyInvoiceLink = true
			}
		}
		if !anyInvoiceLink {
			return create("devseed-link-invoice", map[string]any{"kind": "invoice", "invoice_id": str(open[0], "id")})
		}
	}
	return nil
}

func (t *tenantSeed) expenses() error {
	have, err := t.list("/v1/expenses", "expenses")
	if err != nil {
		return err
	}
	type k struct{ vendor, cur string } // by vendor and currency: an amount edited by hand is not "missing"
	exists := map[k]bool{}
	for _, e := range have {
		exists[k{str(e, "vendor"), str(e, "currency")}] = true
	}
	today := t.s.o.Now().UTC()
	for _, e := range t.data.Expenses {
		if exists[k{e.Vendor, e.Currency}] {
			continue
		}
		body := map[string]any{"vendor": e.Vendor, "category": e.Category, "amount_minor": fmt.Sprint(e.Amount), "currency": e.Currency,
			"incurred_on": today.AddDate(0, 0, -e.DaysAgo).Format("2006-01-02"), "note": e.Note, "recurring": e.Recurring}
		if err := t.call("POST", "/v1/expenses", body, nil); err != nil {
			return fmt.Errorf("expense %s: %w", e.Vendor, err)
		}
		t.logf("expense %s created", e.Vendor)
	}
	return nil
}

func (t *tenantSeed) webhook() error {
	if t.data.Webhook == "" {
		return nil
	}
	have, err := t.list("/v1/webhook-endpoints", "endpoints")
	if err != nil {
		return err
	}
	for _, e := range have {
		if str(e, "url") == t.data.Webhook {
			return nil
		}
	}
	if err := t.call("POST", "/v1/webhook-endpoints", map[string]any{"url": t.data.Webhook, "description": "Dev seed endpoint (does not exist)",
		"event_types": []string{"invoice.paid", "invoice.finalized", "payment_link.paid", "credits.low"}}, nil); err != nil {
		return err
	}
	t.logf("webhook endpoint created")
	return nil
}
