//go:build integration

package itest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/payments"
	"github.com/0furkancolak/payrudder/pkg/payments/iyzico"
	"github.com/0furkancolak/payrudder/pkg/secretbox"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/handler"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/store"
	"github.com/0furkancolak/payrudder/services/payments-worker/internal/webhook"
)

// hostedIyzico answers the calls of the hosted flows as docs.iyzico.com documents them and keeps what it was sent.
type hostedIyzico struct {
	mu     sync.Mutex
	bodies map[string][]map[string]any // by path
	paid   map[string]bool             // payment ids (and tokens) iyzico has taken money for
	// noInstalments makes the card list fail, so the instalment options cannot be read.
	noInstalments bool
}

func (f *hostedIyzico) sent(path string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.bodies[path]...)
}

func (f *hostedIyzico) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var in map[string]any
	_ = json.Unmarshal(raw, &in)
	f.mu.Lock()
	f.bodies[r.URL.Path] = append(f.bodies[r.URL.Path], in)
	f.mu.Unlock()
	key, _ := in["conversationId"].(string)
	invoice, _, _ := strings.Cut(strings.TrimPrefix(key, "link:"), ":")
	switch r.URL.Path {
	case "/payment/iyzipos/checkoutform/initialize/auth/ecom":
		_, _ = io.WriteString(w, `{"status":"success","token":"tok-`+invoice[:4]+`","paymentPageUrl":"https://cpp.example/?token=tok-`+invoice[:4]+`"}`)
	case "/payment/iyzipos/checkoutform/auth/ecom/detail":
		tok, _ := in["token"].(string)
		if !f.paid[tok] {
			_, _ = io.WriteString(w, `{"status":"failure","errorCode":"10051","errorGroup":"NOT_SUFFICIENT_FUNDS","errorMessage":"Not sufficient funds"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"success","paymentStatus":"SUCCESS","paymentId":"pay-cf","basketId":"`+invoice+`","token":"`+tok+`","price":123.45,"currency":"TRY","fraudStatus":1}`)
	case "/payment/3dsecure/initialize":
		page := base64.StdEncoding.EncodeToString([]byte(`<form action="https://bank.example"></form>`))
		_, _ = io.WriteString(w, `{"status":"success","paymentId":"pay-3ds","threeDSHtmlContent":"`+page+`"}`)
	case "/payment/v2/3dsecure/auth":
		_, _ = io.WriteString(w, `{"status":"success","paymentId":"pay-3ds","basketId":"`+invoice+`","price":123.45,"currency":"TRY","fraudStatus":1,"mdStatus":1}`)
	case "/cardstorage/cards":
		if f.noInstalments {
			_, _ = io.WriteString(w, `{"status":"failure","errorCode":"3005","errorMessage":"cardUserKey is not found"}`)
			return
		}
		if in["cardUserKey"] == "other" {
			_, _ = io.WriteString(w, `{"status":"success","cardUserKey":"other","cardDetails":[{"cardToken":"ct9","binNumber":"41595400","lastFourDigits":"1111","cardBankName":"Another Bank"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"success","cardUserKey":"ck","cardDetails":[{"cardToken":"ct","cardAlias":"work","binNumber":"55287900","lastFourDigits":"0008","cardType":"CREDIT_CARD",`+
			`"cardAssociation":"MASTER_CARD","cardFamily":"World","cardBankName":"Halkbank","expireMonth":"12","expireYear":"2030"},{"cardToken":"ct2","binNumber":"41595400","lastFourDigits":"4242"}]}`)
	case "/cardstorage/card":
		_, _ = io.WriteString(w, `{"status":"success"}`)
	case "/payment/iyzipos/installment":
		// The bank's quote for 123.45: 12 instalments are not among the account's enabled counts, 2 is not either.
		_, _ = io.WriteString(w, `{"status":"success","installmentDetails":[{"binNumber":"55287900","force3ds":1,"installmentPrices":[{"installmentPrice":123.45,"totalPrice":123.45,"installmentNumber":1},
			{"installmentPrice":63.0,"totalPrice":126.0,"installmentNumber":2},{"installmentPrice":42.33,"totalPrice":126.99,"installmentNumber":3},{"installmentPrice":21.8,"totalPrice":130.8,"installmentNumber":6},
			{"installmentPrice":11.5,"totalPrice":138.0,"installmentNumber":12}]}]}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func TestIyzicoHostedPaymentsThroughTheWorker(t *testing.T) {
	pg := testkit.StartPostgres(t)
	cdb := openDB(t, pg)
	pool := cdb.DB
	ctx := context.Background()

	iyz := &hostedIyzico{bodies: map[string][]map[string]any{}, paid: map[string]bool{}}
	psp := httptest.NewServer(iyz)
	defer psp.Close()

	box, _ := secretbox.New(bytes.Repeat([]byte{9}, 32))
	registry := payments.NewRegistry()
	iyzico.Register(registry)
	accounts := store.Accounts{DB: pool, Box: box}
	resolver := &handler.Resolver{Registry: registry, Accounts: accounts}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	billing := &recBilling{answer: true}
	ledger := store.Ledger{DB: pool}
	tenant := uuid.New()
	plans := &recPlans{customers: map[string]*plansv1.Customer{
		"ext-1": {Id: "cus-1", ExternalId: "ext-1", Name: "Ada Lovelace", Email: "ada@example.com", Phone: "+905321234567", TaxId: "11111111110", PaymentMethod: "ck:ct",
			Address: &plansv1.Address{Line1: "Nisantasi 1", City: "Istanbul", PostalCode: "34000", Country: "TR"}},
		"ext-2": {Id: "cus-2", ExternalId: "ext-2", Name: "Grace Hopper", Email: "grace@example.com", PaymentMethod: "ck:ct"}, // no phone, no tax id, no address
		"ext-3": {Id: "cus-3", ExternalId: "ext-3", Name: "Linus", Email: "linus@example.com", PaymentMethod: "other:ct9"},
		"ext-4": {Id: "cus-4", ExternalId: "ext-4", Name: "No Card", Email: "nocard@example.com"},
	}}
	api := httptest.NewServer((&webhook.Server{Registry: registry, Resolver: resolver, Accounts: accounts, Events: store.Events{DB: pool}, Attempts: store.Attempts{DB: pool},
		Ledger: ledger, Subscriptions: store.Subscriptions{DB: pool}, Log: log, Token: "internal", Plans: plans, Billing: billing}).Handler())
	defer api.Close()
	if err := accounts.Put(ctx, tenant, "iyzico", "live", []byte(`{"api_key":"api","secret_key":"sec","base_url":"`+psp.URL+`"}`), true); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(method, api.URL+path, strings.NewReader(body))
		req.Header.Set("X-Internal-Token", "internal")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	base := "/admin/tenants/" + tenant.String()
	pay := func(invoice uuid.UUID, ext, method string) string {
		return `{"invoice_id":"` + invoice.String() + `","saga_id":"` + uuid.NewString() + `","number":"INV-1","amount_minor":12345,"currency":"TRY","method":"` + method + `","country":"TR",` +
			`"customer_external_id":"` + ext + `","payer_ip":"85.34.78.112","customer_email":"ada@example.com","customer_name":"Ada Lovelace",` +
			`"return_url":"https://q.example/i/tok","callback_url":"https://q.example/i/tok/return/{key}?lang=tr","environment":"live"}`
	}
	ret := func(key, form string) (int, map[string]any) {
		t.Helper()
		return call("POST", "/admin/invoice-payments/return", `{"key":"`+key+`","form":`+form+`}`)
	}

	t.Run("a checkout is opened for the customer, not the account's default buyer, and the callback names the attempt", func(t *testing.T) {
		invoice := uuid.New()
		code, out := call("POST", base+"/invoice-payments", pay(invoice, "ext-1", "checkout"))
		if code != 201 || out["kind"] != "redirect" || !strings.HasPrefix(out["url"].(string), "https://cpp.example/") {
			t.Fatalf("checkout = %d %v", code, out)
		}
		sent := iyz.sent("/payment/iyzipos/checkoutform/initialize/auth/ecom")
		in := sent[len(sent)-1]
		b := in["buyer"].(map[string]any)
		key := "link:" + invoice.String() + ":1"
		if b["name"] != "Ada" || b["surname"] != "Lovelace" || b["identityNumber"] != "11111111110" || b["gsmNumber"] != "+905321234567" || b["country"] != "Turkey" || b["ip"] != "85.34.78.112" || b["id"] != "cus-1" {
			t.Errorf("buyer = %v", b)
		}
		if in["callbackUrl"] != "https://q.example/i/tok/return/link:"+invoice.String()+":1?lang=tr" || in["conversationId"] != key {
			t.Errorf("callbackUrl %v, conversationId %v", in["callbackUrl"], in["conversationId"])
		}

		// The payer comes back with the token. Nothing is believed until iyzico says so: a token iyzico has taken no money for is a decline.
		code, res := ret(key, `{"token":["tok-`+invoice.String()[:4]+`"]}`)
		if code != 200 || res["state"] != "declined" || res["decline_code"] != "insufficient_funds" || billing.count() != 0 {
			t.Fatalf("an unpaid token = %d %v, settled %d", code, res, billing.count())
		}
		// A token that is not the attempt's is refused before iyzico is asked.
		asked := len(iyz.sent("/payment/iyzipos/checkoutform/auth/ecom/detail"))
		other := uuid.New()
		call("POST", base+"/invoice-payments", pay(other, "ext-1", "checkout"))
		if code, _ := ret("link:"+other.String()+":1", `{"token":["tok-of-somebody-else"]}`); code != 401 || len(iyz.sent("/payment/iyzipos/checkoutform/auth/ecom/detail")) != asked {
			t.Errorf("a foreign token = %d, iyzico asked %d times", code, len(iyz.sent("/payment/iyzipos/checkoutform/auth/ecom/detail"))-asked)
		}
	})

	t.Run("a paid checkout is recorded once, settles the invoice, and the notification that follows changes nothing", func(t *testing.T) {
		invoice := uuid.New()
		before := billing.count()
		call("POST", base+"/invoice-payments", pay(invoice, "ext-1", "checkout"))
		key := "link:" + invoice.String() + ":1"
		tok := "tok-" + invoice.String()[:4]
		iyz.mu.Lock()
		iyz.paid[tok] = true
		iyz.mu.Unlock()
		code, res := ret(key, `{"token":["`+tok+`"]}`)
		if code != 200 || res["state"] != "paid" || billing.count() != before+1 {
			t.Fatalf("return = %d %v, settled %d", code, res, billing.count()-before)
		}
		s := billing.settled[len(billing.settled)-1]
		if s.GetInvoiceId() != invoice.String() || s.GetAmountMinor() != 12345 || s.GetCurrency() != "TRY" || s.GetProvider() != "iyzico" || s.GetProviderRef() != "pay-cf" {
			t.Errorf("settle = %v", s)
		}
		// Posting the same form again is answered from the ledger: iyzico is not asked again.
		asked := len(iyz.sent("/payment/iyzipos/checkoutform/auth/ecom/detail"))
		if code, res := ret(key, `{"token":["`+tok+`"]}`); code != 200 || res["state"] != "paid" || len(iyz.sent("/payment/iyzipos/checkoutform/auth/ecom/detail")) != asked {
			t.Errorf("repeat = %d %v", code, res)
		}
		rows, _ := ledger.LookupInvoices(ctx, tenant, []uuid.UUID{invoice})
		var succeeded int
		for _, r := range rows {
			if r.Status == "succeeded" {
				succeeded++
			}
		}
		if succeeded != 1 {
			t.Errorf("ledger = %+v, want one succeeded charge", rows)
		}
	})

	t.Run("a customer the provider cannot take is refused with the fields, before iyzico is asked", func(t *testing.T) {
		n := len(iyz.sent("/payment/iyzipos/checkoutform/initialize/auth/ecom"))
		code, out := call("POST", base+"/invoice-payments", pay(uuid.New(), "ext-2", "checkout"))
		e, _ := out["error"].(map[string]any)
		if code != 422 || e["key"] != "payments.invoice.buyer_incomplete" || !strings.Contains(e["message"].(string), "tax_id") || !strings.Contains(e["message"].(string), "phone") {
			t.Fatalf("= %d %v", code, out)
		}
		if len(iyz.sent("/payment/iyzipos/checkoutform/initialize/auth/ecom")) != n {
			t.Error("iyzico was asked for a form it would have refused")
		}
	})

	t.Run("the saved card is offered to a customer who has one, and authenticates at the bank", func(t *testing.T) {
		code, out := call("GET", base+"/invoice-methods?environment=live&currency=TRY&country=TR&customer=ext-1", "")
		var ids []string
		for _, m := range out["methods"].([]any) {
			ids = append(ids, m.(map[string]any)["id"].(string))
		}
		if code != 200 || strings.Join(ids, ",") != "checkout,stored_card" {
			t.Fatalf("methods = %d %v", code, ids)
		}
		if _, out = call("GET", base+"/invoice-methods?environment=live&currency=TRY&country=TR", ""); len(out["methods"].([]any)) != 1 {
			t.Errorf("a guest has no saved card: %v", out)
		}

		invoice := uuid.New()
		code, out = call("POST", base+"/invoice-payments", pay(invoice, "ext-1", "stored_card"))
		if code != 201 || out["kind"] != "html" || out["html"] != `<form action="https://bank.example"></form>` {
			t.Fatalf("start = %d %v", code, out)
		}
		init := iyz.sent("/payment/3dsecure/initialize")
		card := init[len(init)-1]["paymentCard"].(map[string]any)
		if card["cardUserKey"] != "ck" || card["cardToken"] != "ct" || len(card) != 2 {
			t.Errorf("paymentCard = %v; a stored card, never a number", card)
		}
		rows, _ := ledger.LookupInvoices(ctx, tenant, []uuid.UUID{invoice})
		if len(rows) != 1 || rows[0].Status != "pending" {
			t.Fatalf("ledger = %+v, want one pending row while the payer is at the bank", rows)
		}

		key := "link:" + invoice.String() + ":1"
		before := billing.count()
		code, res := ret(key, `{"status":["success"],"paymentId":["pay-3ds"],"mdStatus":["1"],"conversationId":["`+key+`"]}`)
		if code != 200 || res["state"] != "paid" || billing.count() != before+1 {
			t.Fatalf("return = %d %v, settled %d", code, res, billing.count()-before)
		}
		auth := iyz.sent("/payment/v2/3dsecure/auth")
		if len(auth) != 1 || auth[0]["paymentId"] != "pay-3ds" || auth[0]["paidPrice"] != "123.45" || auth[0]["basketId"] != invoice.String() {
			t.Errorf("auth = %v", auth)
		}
		// The page reloaded: the ledger answers, and the auth call is not made a second time.
		if code, res := ret(key, `{"status":["success"],"paymentId":["pay-3ds"],"mdStatus":["1"]}`); code != 200 || res["state"] != "paid" || len(iyz.sent("/payment/v2/3dsecure/auth")) != 1 {
			t.Errorf("repeat = %d %v, auth calls %d", code, res, len(iyz.sent("/payment/v2/3dsecure/auth")))
		}
	})

	t.Run("an authentication the bank refused is a decline and no auth call is made", func(t *testing.T) {
		invoice := uuid.New()
		call("POST", base+"/invoice-payments", pay(invoice, "ext-1", "stored_card"))
		key := "link:" + invoice.String() + ":1"
		auths := len(iyz.sent("/payment/v2/3dsecure/auth"))
		code, res := ret(key, `{"status":["failure"],"paymentId":["pay-3ds"],"mdStatus":["0"]}`)
		if code != 200 || res["state"] != "declined" || res["decline_code"] != "authentication_failed" || len(iyz.sent("/payment/v2/3dsecure/auth")) != auths {
			t.Errorf("= %d %v", code, res)
		}
	})

	t.Run("a return for an attempt that does not exist, or one that is not hosted", func(t *testing.T) {
		if code, _ := ret("link:"+uuid.NewString()+":1", `{"token":["x"]}`); code != 404 {
			t.Errorf("unknown = %d", code)
		}
		if code, _ := ret("inv-1:charge:1", `{"token":["x"]}`); code != 404 {
			t.Errorf("a charge of the saga = %d", code)
		}
	})

	t.Run("a stored card is offered in the instalments the account enables and the bank quotes, and the quote is what is charged", func(t *testing.T) {
		split := uuid.New()
		if err := accounts.Put(ctx, split, "iyzico", "live", []byte(`{"api_key":"api","secret_key":"sec","base_url":"`+psp.URL+`","enabled_installments":[1,3,6]}`), true); err != nil {
			t.Fatal(err)
		}
		b := "/admin/tenants/" + split.String()
		stored := func(q string) map[string]any {
			t.Helper()
			code, out := call("GET", b+"/invoice-methods?environment=live&currency=TRY&country=TR&customer=ext-1"+q, "")
			if code != 200 {
				t.Fatalf("methods = %d %v", code, out)
			}
			for _, m := range out["methods"].([]any) {
				if m.(map[string]any)["kind"] == "stored_card" {
					return m.(map[string]any)
				}
			}
			t.Fatalf("no stored card in %v", out)
			return nil
		}
		opts, _ := stored("&amount_minor=12345")["installments"].([]any)
		if len(opts) != 2 {
			t.Fatalf("instalments = %v, want 3 and 6 (enabled, and more than one payment)", opts)
		}
		if o := opts[0].(map[string]any); o["count"] != float64(3) || o["each_minor"] != float64(4233) || o["total_minor"] != float64(12699) {
			t.Errorf("3 instalments = %v: 3 of 42.33 are 126.99", o)
		}
		if _, has := stored("")["installments"]; has {
			t.Error("instalments were quoted without an amount")
		}
		iyz.mu.Lock()
		iyz.noInstalments = true
		iyz.mu.Unlock()
		if _, has := stored("&amount_minor=12345")["installments"]; has {
			t.Error("instalments were offered while iyzico cannot be asked: the payer pays once")
		}
		iyz.mu.Lock()
		iyz.noInstalments = false
		iyz.mu.Unlock()

		with := func(invoice uuid.UUID, n int) string {
			return strings.Replace(pay(invoice, "ext-1", "stored_card"), `"environment":"live"`, `"environment":"live","installments":`+strconv.Itoa(n), 1)
		}
		// A count the bank does not quote (or the account does not enable) starts nothing, and iyzico is not asked to start a payment.
		inits := len(iyz.sent("/payment/3dsecure/initialize"))
		for _, n := range []int{2, 12, 4} {
			if code, out := call("POST", b+"/invoice-payments", with(uuid.New(), n)); code != 409 || out["error"].(map[string]any)["key"] != "payments.invoice.installment_unavailable" {
				t.Errorf("%d instalments = %d %v", n, code, out)
			}
		}
		if len(iyz.sent("/payment/3dsecure/initialize")) != inits {
			t.Fatal("a payment was started for an instalment count that is not offered")
		}

		invoice := uuid.New()
		if code, out := call("POST", b+"/invoice-payments", with(invoice, 3)); code != 201 || out["kind"] != "html" {
			t.Fatalf("start = %d %v", code, out)
		}
		init := iyz.sent("/payment/3dsecure/initialize")
		last := init[len(init)-1]
		if last["installment"] != float64(3) || last["price"] != "123.45" || last["paidPrice"] != "126.99" {
			t.Errorf("initialise = %v: 3 instalments, the price of the invoice, the bank's total as the paid price", last)
		}
		key := "link:" + invoice.String() + ":1"
		before := billing.count()
		if code, res := ret(key, `{"status":["success"],"paymentId":["pay-3ds"],"mdStatus":["1"],"conversationId":["`+key+`"]}`); code != 200 || res["state"] != "paid" || billing.count() != before+1 {
			t.Fatalf("return = %d %v", code, res)
		}
		auth := iyz.sent("/payment/v2/3dsecure/auth")
		if got := auth[len(auth)-1]; got["paidPrice"] != "126.99" {
			t.Errorf("auth = %v: it names what the initialise call did", got)
		}
		rows, _ := ledger.LookupInvoices(ctx, split, []uuid.UUID{invoice})
		for _, r := range rows {
			if r.AmountMinor != 12345 {
				t.Errorf("ledger = %+v: the invoice is paid in full at its own price, the bank's charge for the instalments is not the seller's", rows)
			}
		}
		if len(rows) == 0 || rows[0].Status != "succeeded" {
			t.Errorf("ledger = %+v, want the payment succeeded", rows)
		}
		// One payment stays one payment.
		single := uuid.New()
		call("POST", b+"/invoice-payments", pay(single, "ext-1", "stored_card"))
		init = iyz.sent("/payment/3dsecure/initialize")
		if last := init[len(init)-1]; last["installment"] != float64(1) || last["paidPrice"] != "123.45" {
			t.Errorf("a single payment = %v", last)
		}
	})

	t.Run("the cards iyzico holds for a customer are listed and deleted, only the customer's own", func(t *testing.T) {
		cards := base + "/customers/ext-1/cards"
		code, out := call("GET", cards, "")
		list, _ := out["cards"].([]any)
		if code != 200 || out["provider"] != "iyzico" || len(list) != 2 {
			t.Fatalf("list = %d %v", code, out)
		}
		first := list[0].(map[string]any)
		if first["token"] != "ct" || first["last4"] != "0008" || first["bin"] != "55287900" || first["bank"] != "Halkbank" || first["expire_year"] != "2030" || first["in_use"] != true || list[1].(map[string]any)["in_use"] != false {
			t.Errorf("cards = %v: the one the payment method names is in use", list)
		}
		if raw, _ := json.Marshal(out); strings.Contains(string(raw), "cardUserKey") || strings.Contains(string(raw), `"ck"`) {
			t.Errorf("the provider's user key is in the answer: %s", raw)
		}
		listed := iyz.sent("/cardstorage/cards")
		if got := listed[len(listed)-1]["cardUserKey"]; got != "ck" {
			t.Errorf("the user key asked for is %v: it comes from the customer's payment method", got)
		}
		// A customer with no stored card has an empty list; one that does not exist is a 404; a customer of another user key has only their own.
		if code, out := call("GET", base+"/customers/ext-4/cards", ""); code != 200 || len(out["cards"].([]any)) != 0 || out["provider"] != "" {
			t.Errorf("no payment method = %d %v", code, out)
		}
		if code, _ := call("GET", base+"/customers/nobody/cards", ""); code != 404 {
			t.Errorf("unknown customer = %d", code)
		}
		// Delete: a card of another customer is a 404 and iyzico is asked to delete nothing.
		deletes := len(iyz.sent("/cardstorage/card"))
		if code, out := call("DELETE", cards+"/ct9", ""); code != 404 || out["error"].(map[string]any)["key"] != "payments.card.not_found" || len(iyz.sent("/cardstorage/card")) != deletes {
			t.Errorf("another customer's card = %d %v, deletes %d", code, out, len(iyz.sent("/cardstorage/card"))-deletes)
		}
		if code, _ := call("DELETE", base+"/customers/ext-4/cards/ct", ""); code != 404 || len(iyz.sent("/cardstorage/card")) != deletes {
			t.Errorf("a customer with no card: %d", code)
		}
		code, out = call("DELETE", cards+"/ct2", "")
		if code != 200 || out["deleted"] != true || out["was_in_use"] != false {
			t.Fatalf("delete = %d %v", code, out)
		}
		sent := iyz.sent("/cardstorage/card")
		if len(sent) != deletes+1 || sent[len(sent)-1]["cardUserKey"] != "ck" || sent[len(sent)-1]["cardToken"] != "ct2" {
			t.Errorf("iyzico was asked %v", sent)
		}
		// The card the customer's invoices are charged with says so, and the customer's payment method is left to the seller.
		if code, out := call("DELETE", cards+"/ct", ""); code != 200 || out["was_in_use"] != true {
			t.Errorf("delete the card in use = %d %v", code, out)
		}
	})
}
