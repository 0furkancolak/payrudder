package iyzico

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/0furkancolak/payrudder/pkg/payments"
)

var ada = &payments.Buyer{ID: "cus-1", Name: "Ada Byron Lovelace", Email: "ada@example.com", Phone: "+905321234567", TaxID: "11111111110",
	Line1: "Nisantasi 1", Line2: "Kat 3", City: "Istanbul", PostalCode: "34000", Country: "TR", IP: "85.34.78.112"}

// noDefaults is a client whose seller configured no buyer: whatever the customer lacks is missing.
func noDefaults(t *testing.T) (*fake, *Client) {
	t.Helper()
	return newFake(t, func(s *Settings) { s.Buyer = Buyer{} })
}

func TestTheBuyerIsTheCustomer(t *testing.T) {
	f, c := newFake(t, nil) // the default buyer of the account is another person
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"9","fraudStatus":1}`
	}
	if _, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 1116, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: ada}); err != nil {
		t.Fatal(err)
	}
	b := f.last["buyer"].(map[string]any)
	want := map[string]string{"id": "cus-1", "name": "Ada Byron", "surname": "Lovelace", "identityNumber": "11111111110", "email": "ada@example.com", "gsmNumber": "+905321234567",
		"registrationAddress": "Nisantasi 1, Kat 3", "city": "Istanbul", "country": "Turkey", "zipCode": "34000", "ip": "85.34.78.112"}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("buyer.%s = %v, want %q", k, b[k], v)
		}
	}
	for _, name := range []string{"billingAddress", "shippingAddress"} {
		a := f.last[name].(map[string]any)
		if a["contactName"] != "Ada Byron Lovelace" || a["address"] != "Nisantasi 1, Kat 3" || a["city"] != "Istanbul" || a["country"] != "Turkey" || a["zipCode"] != "34000" {
			t.Errorf("%s = %v", name, a)
		}
	}
}

func TestAMissingBuyerFieldIsNamedAndNothingIsSent(t *testing.T) {
	f, c := noDefaults(t)
	f.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"success","paymentId":"9"}` }
	partial := *ada
	partial.Phone, partial.TaxID, partial.Line1, partial.Line2 = "", "", "", ""
	_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: &partial})
	var inc *payments.BuyerIncompleteError
	if !errors.As(err, &inc) || !slices.Equal(inc.Missing, []string{"tax_id", "phone", "address.line1"}) {
		t.Fatalf("err = %v, want the missing fields tax_id, phone and address.line1", err)
	}
	if len(f.paths) != 0 {
		t.Errorf("iyzico was called %v for a buyer it would refuse", f.paths)
	}
	// A single word is a name without a surname: not made up either.
	oneWord := *ada
	oneWord.Name = "Acme"
	_, err = c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: &oneWord})
	if !errors.As(err, &inc) || !slices.Equal(inc.Missing, []string{"surname"}) {
		t.Errorf("err = %v, want surname missing", err)
	}
	// No customer and no defaults: everything is missing, and the order id is not a buyer id the seller never gave.
	_, err = c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct"})
	if !errors.As(err, &inc) || len(inc.Missing) != 8 {
		t.Errorf("err = %v", err)
	}
}

func TestTheSellersDefaultsFillOnlyWhatTheCustomerLacks(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"success","paymentId":"9"}` }
	lacking := *ada
	lacking.TaxID, lacking.IP = "", ""
	if _, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: &lacking}); err != nil {
		t.Fatal(err)
	}
	b := f.last["buyer"].(map[string]any)
	if b["identityNumber"] != "11111111111" || b["ip"] != "85.34.78.112" || b["email"] != "ada@example.com" || b["name"] != "Ada Byron" {
		t.Errorf("buyer = %v: the default fills the identity number, the customer keeps the rest", b)
	}
}

func TestAnOptionalBuyerFieldIsLeftOutWhenThereIsNone(t *testing.T) {
	f, c := noDefaults(t)
	f.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"success","paymentId":"9"}` }
	b := *ada
	b.PostalCode, b.IP = "", ""
	if _, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "inv-1:charge:1", InvoiceID: "inv-1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: &b}); err != nil {
		t.Fatal(err)
	}
	buyer := f.last["buyer"].(map[string]any)
	if _, ok := buyer["ip"]; ok {
		t.Errorf("buyer.ip = %v; a renewal has no payer address and none is invented", buyer["ip"])
	}
	if _, ok := buyer["zipCode"]; ok {
		t.Error("buyer.zipCode is optional and was not given")
	}
}

func TestACountryThatIsNotACodeIsPassedOn(t *testing.T) {
	for in, want := range map[string]string{"TR": "Turkey", "de": "Germany", "Turkey": "Turkey", "": ""} {
		if got := countryName(in); got != want {
			t.Errorf("countryName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnAnswerAboutTheBuyerNamesTheFieldOfTheCustomer(t *testing.T) {
	for code, field := range map[string]string{"3": "email", "5": "email", "8": "tax_id", "10": "tax_id", "25": "phone", "27": "phone"} {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) {
			return 200, `{"status":"failure","errorCode":"` + code + `","errorMessage":"x","retryable":true}`
		}
		_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: ada})
		var inc *payments.BuyerIncompleteError
		if !errors.As(err, &inc) || !slices.Equal(inc.Missing, []string{field}) {
			t.Errorf("code %s: err = %v, want the buyer's %s", code, err, field)
		}
	}
}

func TestCheckoutFormHasACallbackTheBuyerAndTheInstalmentsOfTheAccount(t *testing.T) {
	f, c := newFake(t, func(s *Settings) { s.EnabledInstallments = []int{1, 3, 6} })
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","token":"tok-1","paymentPageUrl":"https://cpp.example/?token=tok-1"}`
	}
	_, err := c.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", Number: "N-1", AmountMinor: 1116,
		Currency: "TRY", ReturnURL: "https://q.example/i/tok", CallbackURL: "https://q.example/i/tok/return/link%3Ainv-1%3A1", Buyer: ada})
	if err != nil {
		t.Fatal(err)
	}
	if f.last["callbackUrl"] != "https://q.example/i/tok/return/link%3Ainv-1%3A1" {
		t.Errorf("callbackUrl = %v; the result is posted to the confirming route, not to the page", f.last["callbackUrl"])
	}
	if got := f.last["enabledInstallments"].([]any); len(got) != 3 || got[1] != float64(3) {
		t.Errorf("enabledInstallments = %v", got)
	}
	if b := f.last["buyer"].(map[string]any); b["id"] != "cus-1" || b["identityNumber"] != "11111111110" {
		t.Errorf("buyer = %v", b)
	}
	if _, err := New(Settings{APIKey: "a", SecretKey: "s", EnabledInstallments: []int{1, 5}}, 0); err == nil {
		t.Error("5 is not an instalment count of the payment pages")
	}
	// A guest has an email and a name, and the account has no defaults: the checkout is refused with the fields, not sent.
	f2, c2 := noDefaults(t)
	_, err = c2.CreateInvoiceCheckout(context.Background(), payments.CheckoutInvoiceRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", AmountMinor: 100, Currency: "TRY",
		ReturnURL: "https://q.example/i/tok", CustomerEmail: "g@example.com", CustomerName: "Grace Hopper"})
	var inc *payments.BuyerIncompleteError
	if !errors.As(err, &inc) || len(f2.paths) != 0 || slices.Contains(inc.Missing, "email") || slices.Contains(inc.Missing, "name") {
		t.Errorf("err = %v, calls %v", err, f2.paths)
	}
}

func returnRequest(form url.Values) payments.ReturnRequest {
	return payments.ReturnRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", AmountMinor: 1116, Currency: "TRY", Form: form}
}

func TestTheCheckoutFormReturnIsConfirmedWithIyzico(t *testing.T) {
	cf := func(extra string) string {
		return `{"status":"success","paymentStatus":"SUCCESS","paymentId":"77","basketId":"inv-1","token":"tok-1","price":11.16000000,"currency":"TRY","fraudStatus":1` + extra + `}`
	}
	run := func(t *testing.T, form url.Values, ref, answer string) (payments.StatusResult, *fake, error) {
		f, c := newFake(t, nil)
		f.reply = func(string, map[string]any) (int, string) { return 200, answer }
		r := returnRequest(form)
		r.ProviderRef = ref
		st, err := c.ConfirmReturn(context.Background(), r)
		return st, f, err
	}
	t.Run("a paid payment of the attempt", func(t *testing.T) {
		st, f, err := run(t, url.Values{"token": {"tok-1"}}, "tok-1", cf(""))
		if err != nil || st.State != payments.StateSucceeded || st.ChargeID != "77" {
			t.Fatalf("= %+v, %v", st, err)
		}
		if f.paths[0] != retrieveCheckoutFormPath || f.last["token"] != "tok-1" || f.last["conversationId"] != "link:inv-1:1" {
			t.Errorf("asked %v %v", f.paths, f.last)
		}
	})
	t.Run("a token that is not the attempt's is not asked about", func(t *testing.T) {
		_, f, err := run(t, url.Values{"token": {"other"}}, "tok-1", cf(""))
		if !errors.Is(err, payments.ErrUnverified) || len(f.paths) != 0 {
			t.Errorf("err = %v, calls %v", err, f.paths)
		}
	})
	t.Run("a payment of another invoice, amount or currency", func(t *testing.T) {
		for _, answer := range []string{
			strings.Replace(cf(""), `"basketId":"inv-1"`, `"basketId":"inv-2"`, 1),
			strings.Replace(cf(""), `11.16000000`, `1.16000000`, 1),
			strings.Replace(cf(""), `"TRY"`, `"EUR"`, 1),
			strings.Replace(cf(""), `"tok-1"`, `"tok-2"`, 1),
		} {
			if _, _, err := run(t, url.Values{"token": {"tok-1"}}, "tok-1", answer); !errors.Is(err, payments.ErrUnverified) {
				t.Errorf("%s: err = %v, want ErrUnverified", answer, err)
			}
		}
	})
	t.Run("a payment under fraud review is pending, one the filters rejected a decline", func(t *testing.T) {
		_, _, err := run(t, url.Values{"token": {"tok-1"}}, "tok-1", strings.Replace(cf(""), `"fraudStatus":1`, `"fraudStatus":0`, 1))
		var pending *payments.PendingError
		if !errors.As(err, &pending) {
			t.Errorf("err = %v, want pending", err)
		}
		st, _, err := run(t, url.Values{"token": {"tok-1"}}, "tok-1", strings.Replace(cf(""), `"fraudStatus":1`, `"fraudStatus":-1`, 1))
		if err != nil || st.State != payments.StateDeclined || st.DeclineCode != "fraudulent" {
			t.Errorf("= %+v, %v", st, err)
		}
	})
	t.Run("a bank's refusal is the payer's decline, a system failure is not", func(t *testing.T) {
		st, _, err := run(t, url.Values{"token": {"tok-1"}}, "tok-1", `{"status":"failure","errorCode":"10051","errorGroup":"NOT_SUFFICIENT_FUNDS"}`)
		if err != nil || st.State != payments.StateDeclined || st.DeclineCode != "insufficient_funds" {
			t.Errorf("= %+v, %v", st, err)
		}
		_, _, err = run(t, url.Values{"token": {"tok-1"}}, "tok-1", `{"status":"failure","errorCode":"10214","errorGroup":"COMMUNICATION_OR_SYSTEM_ERROR"}`)
		var transient *payments.TransientError
		if !errors.As(err, &transient) {
			t.Errorf("err = %v, want transient", err)
		}
	})
	t.Run("a return with neither a token nor a payment id", func(t *testing.T) {
		if _, _, err := run(t, url.Values{}, "", cf("")); !errors.Is(err, payments.ErrUnverified) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestThreeDSStartsWithTheStoredCardAndReturnsTheBanksPage(t *testing.T) {
	page := `<form id="f" action="https://bank.example/3ds" method="post"><script>document.getElementById("f").submit()</script></form>`
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","paymentId":"88","threeDSHtmlContent":"` + base64.StdEncoding.EncodeToString([]byte(page)) + `"}`
	}
	var _ payments.ThreeDSProvider = c
	st, err := c.StartThreeDS(context.Background(), payments.ThreeDSRequest{IdempotencyKey: "link:inv-1:1", InvoiceID: "inv-1", Number: "N-1", AmountMinor: 1116, Currency: "TRY",
		PaymentMethod: "ck:ct", CallbackURL: "https://q.example/i/tok/return/key", Buyer: ada})
	if err != nil || st.Ref != "88" || st.HTML != page {
		t.Fatalf("= %+v, %v", st, err)
	}
	b := f.last
	card := b["paymentCard"].(map[string]any)
	if f.paths[0] != "/payment/3dsecure/initialize" || b["callbackUrl"] != "https://q.example/i/tok/return/key" || b["price"] != "11.16" || b["basketId"] != "inv-1" ||
		b["conversationId"] != "link:inv-1:1" || card["cardUserKey"] != "ck" || card["cardToken"] != "ct" || len(card) != 2 {
		t.Errorf("request = %v", b)
	}
	if buyer := b["buyer"].(map[string]any); buyer["email"] != "ada@example.com" || buyer["ip"] != "85.34.78.112" {
		t.Errorf("buyer = %v", buyer)
	}
	// A refusal, a missing page, a card that is not a stored one, a buyer that is incomplete.
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"failure","errorCode":"10217","errorGroup":"DEBIT_CARDS_REQUIRES_3DS"}`
	}
	var d *payments.DeclinedError
	if _, err := c.StartThreeDS(context.Background(), payments.ThreeDSRequest{IdempotencyKey: "k:charge:1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", CallbackURL: "https://q.example/x", Buyer: ada}); !errors.As(err, &d) {
		t.Errorf("err = %v, want a decline", err)
	}
	f.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"success","paymentId":"88"}` }
	if _, err := c.StartThreeDS(context.Background(), payments.ThreeDSRequest{IdempotencyKey: "k:charge:1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", CallbackURL: "https://q.example/x", Buyer: ada}); err == nil {
		t.Error("an initialisation without a page is no 3-D Secure payment")
	}
	if _, err := c.StartThreeDS(context.Background(), payments.ThreeDSRequest{IdempotencyKey: "k:charge:1", AmountMinor: 100, Currency: "TRY", PaymentMethod: "4111111111111111", CallbackURL: "https://q.example/x", Buyer: ada}); err == nil {
		t.Error("a card number is not a stored card and must not be sent anywhere")
	}
}

func TestThreeDSIsFinalisedOnceAfterTheBankApproved(t *testing.T) {
	auth := `{"status":"success","paymentId":"88","basketId":"inv-1","price":11.16000000,"paidPrice":11.16000000,"currency":"TRY","fraudStatus":1,"mdStatus":1}`
	run := func(form url.Values, reply func(path string) (int, string)) (payments.StatusResult, *fake, error) {
		f, c := newFake(t, nil)
		f.reply = func(path string, _ map[string]any) (int, string) { return reply(path) }
		r := returnRequest(form)
		r.ProviderRef = "88"
		st, err := c.ConfirmReturn(context.Background(), r)
		return st, f, err
	}
	ok := url.Values{"status": {"success"}, "paymentId": {"88"}, "mdStatus": {"1"}, "conversationData": {"cd"}, "conversationId": {"link:inv-1:1"}}
	t.Run("the auth call carries what the initialisation did", func(t *testing.T) {
		st, f, err := run(ok, func(string) (int, string) { return 200, auth })
		if err != nil || st.State != payments.StateSucceeded || st.ChargeID != "88" {
			t.Fatalf("= %+v, %v", st, err)
		}
		if len(f.paths) != 1 || f.paths[0] != "/payment/v2/3dsecure/auth" {
			t.Fatalf("calls = %v", f.paths)
		}
		b := f.last
		if b["paymentId"] != "88" || b["paidPrice"] != "11.16" || b["basketId"] != "inv-1" || b["currency"] != "TRY" || b["conversationId"] != "link:inv-1:1" {
			t.Errorf("auth request = %v", b)
		}
	})
	t.Run("an authentication the bank refused is a decline and no auth call is made", func(t *testing.T) {
		for _, md := range []string{"0", "2", "4", "7", "8"} {
			form := url.Values{"status": {"failure"}, "paymentId": {"88"}, "mdStatus": {md}}
			st, f, err := run(form, func(string) (int, string) { return 200, auth })
			if err != nil || st.State != payments.StateDeclined || st.DeclineCode != "authentication_failed" || len(f.paths) != 0 {
				t.Errorf("mdStatus %s: %+v, %v, calls %v", md, st, err, f.paths)
			}
		}
	})
	t.Run("a payment id that is not the attempt's", func(t *testing.T) {
		form := url.Values{"paymentId": {"99"}, "mdStatus": {"1"}}
		_, f, err := run(form, func(string) (int, string) { return 200, auth })
		if !errors.Is(err, payments.ErrUnverified) || len(f.paths) != 0 {
			t.Errorf("err = %v, calls %v", err, f.paths)
		}
	})
	t.Run("an answer for another basket, payment or amount", func(t *testing.T) {
		for _, answer := range []string{
			strings.Replace(auth, `"basketId":"inv-1"`, `"basketId":"inv-9"`, 1),
			strings.Replace(auth, `"paymentId":"88"`, `"paymentId":"89"`, 1),
			strings.Replace(auth, `11.16000000`, `0.16000000`, 1),
		} {
			if _, _, err := run(ok, func(string) (int, string) { return 200, answer }); !errors.Is(err, payments.ErrUnverified) {
				t.Errorf("%s: err = %v", answer, err)
			}
		}
	})
	t.Run("a refusal of the auth call is the bank's decline", func(t *testing.T) {
		st, f, err := run(ok, func(path string) (int, string) {
			if path == "/payment/detail" {
				return 200, `{"status":"success","paymentStatus":"FAILURE","paymentId":"88","basketId":"inv-1"}`
			}
			return 200, `{"status":"failure","errorCode":"10051","errorGroup":"NOT_SUFFICIENT_FUNDS"}`
		})
		if err != nil || st.State != payments.StateDeclined || st.DeclineCode != "insufficient_funds" {
			t.Errorf("= %+v, %v (calls %v)", st, err, f.paths)
		}
	})
	t.Run("a repeated return finds the payment already done", func(t *testing.T) {
		st, _, err := run(ok, func(path string) (int, string) {
			if path == "/payment/detail" {
				return 200, `{"status":"success","paymentStatus":"SUCCESS","paymentId":"88","basketId":"inv-1","fraudStatus":1}`
			}
			return 200, `{"status":"failure","errorCode":"5087","errorMessage":"Payment is not for this merchant"}`
		})
		if err != nil || st.State != payments.StateSucceeded || st.ChargeID != "88" {
			t.Errorf("= %+v, %v; a second auth call that iyzico refuses must not turn a paid payment into a decline", st, err)
		}
	})
	t.Run("no answer to the auth call is looked up, never repeated", func(t *testing.T) {
		st, f, err := run(ok, func(path string) (int, string) {
			if path == "/payment/detail" {
				return 200, `{"status":"success","paymentStatus":"SUCCESS","paymentId":"88","basketId":"inv-1","fraudStatus":1}`
			}
			return 500, ``
		})
		if err != nil || st.State != payments.StateSucceeded {
			t.Errorf("= %+v, %v", st, err)
		}
		if n := strings.Count(strings.Join(f.paths, " "), "3dsecure/auth"); n != 1 {
			t.Errorf("the auth call was made %d times: %v", n, f.paths)
		}
		_, f, err = run(ok, func(path string) (int, string) {
			if path == "/payment/detail" {
				return 200, `{"status":"success","paymentStatus":"CALLBACK_THREEDS","paymentId":"88","basketId":"inv-1"}`
			}
			return 500, ``
		})
		var transient *payments.TransientError
		if !errors.As(err, &transient) {
			t.Errorf("err = %v, want the failure of the auth call, the outcome unknown (calls %v)", err, f.paths)
		}
	})
	t.Run("a payment under fraud review is pending", func(t *testing.T) {
		_, _, err := run(ok, func(string) (int, string) { return 200, strings.Replace(auth, `"fraudStatus":1`, `"fraudStatus":0`, 1) })
		var pending *payments.PendingError
		if !errors.As(err, &pending) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestStoredCardsAreListedAndDeleted(t *testing.T) {
	f, c := newFake(t, nil)
	var _ payments.CardStore = c
	f.reply = func(path string, _ map[string]any) (int, string) {
		return 200, `{"status":"success","cardUserKey":"ck","cardDetails":[{"cardToken":"ct1","cardAlias":"work","binNumber":"55287900","lastFourDigits":"0008","cardType":"CREDIT_CARD","cardAssociation":"MASTER_CARD","cardFamily":"World","cardBankName":"Halkbank","expireMonth":"12","expireYear":"2030"}]}`
	}
	cards, err := c.ListCards(context.Background(), "ck")
	if err != nil || len(cards) != 1 || cards[0].Token != "ct1" || cards[0].Last4 != "0008" || cards[0].BIN != "55287900" || cards[0].ExpireYear != "2030" {
		t.Fatalf("= %+v, %v", cards, err)
	}
	if f.paths[0] != "/cardstorage/cards" || f.methods[0] != http.MethodPost || f.last["cardUserKey"] != "ck" {
		t.Errorf("list: %v %v %v", f.methods, f.paths, f.last)
	}
	f.reply = func(string, map[string]any) (int, string) { return 200, `{"status":"success"}` }
	if err := c.DeleteCard(context.Background(), "ck", "ct1"); err != nil {
		t.Fatal(err)
	}
	if f.paths[1] != "/cardstorage/card" || f.methods[1] != http.MethodDelete || f.last["cardUserKey"] != "ck" || f.last["cardToken"] != "ct1" {
		t.Errorf("delete: %v %v %v", f.methods, f.paths, f.last)
	}
	// A card the account does not know is a mistake of the request, not a decline of a payment.
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"failure","errorCode":"3006","errorMessage":"cardToken is not found"}`
	}
	var e *Error
	if err := c.DeleteCard(context.Background(), "ck", "gone"); !errors.As(err, &e) || e.Code != "3006" {
		t.Errorf("err = %v", err)
	}
	if _, err := c.ListCards(context.Background(), ""); err == nil {
		t.Error("a user key is required")
	}
}

func TestAStoredCardThatIsGoneIsADeclineOfTheCharge(t *testing.T) {
	f, c := newFake(t, nil)
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"failure","errorCode":"3006","errorMessage":"cardToken is not found","retryable":true}`
	}
	_, err := c.Charge(context.Background(), payments.ChargeRequest{IdempotencyKey: "k", InvoiceID: "i", AmountMinor: 100, Currency: "TRY", PaymentMethod: "ck:ct", Buyer: ada})
	var d *payments.DeclinedError
	if !errors.As(err, &d) || d.Code != "payment_method_not_found" {
		t.Errorf("err = %v; the payer must give another card, retrying cannot help", err)
	}
}

func TestInstallmentsAreAskedByAmountAndBIN(t *testing.T) {
	f, c := newFake(t, nil)
	var _ payments.InstallmentQuoter = c
	f.reply = func(string, map[string]any) (int, string) {
		return 200, `{"status":"success","installmentDetails":[{"binNumber":"55287900","price":100.0,"cardType":"CREDIT_CARD","cardAssociation":"MASTER_CARD","cardFamilyName":"World","bankName":"Halkbank","force3ds":1,
		"installmentPrices":[{"installmentPrice":100.00000000,"totalPrice":100.00000000,"installmentNumber":1},{"installmentPrice":35.5,"totalPrice":106.5,"installmentNumber":3}]}]}`
	}
	cards, err := c.Installments(context.Background(), 10000, "TRY", "55287900")
	if err != nil || len(cards) != 1 || !cards[0].Force3DS || len(cards[0].Options) != 2 {
		t.Fatalf("= %+v, %v", cards, err)
	}
	if o := cards[0].Options[1]; o.Count != 3 || o.InstallmentMinor != 3550 || o.TotalMinor != 10650 {
		t.Errorf("option = %+v: 3 instalments of 35.50 are 106.50 in all", o)
	}
	if f.paths[0] != "/payment/iyzipos/installment" || f.last["price"] != "100.00" || f.last["binNumber"] != "55287900" {
		t.Errorf("request %v %v", f.paths, f.last)
	}
	if _, err := c.Installments(context.Background(), 10000, "TRY", "5528"); err == nil {
		t.Error("a BIN is 8 digits")
	}
	if _, err := c.Installments(context.Background(), 10000, "TRY", ""); err != nil || f.last["binNumber"] != nil {
		t.Errorf("without a BIN none is sent: %v %v", err, f.last)
	}
}

func TestAPaymentIsCancelledTheDayItWasTakenAndRefundedAfter(t *testing.T) {
	// 2026-11-02 21:30 UTC is 00:30 on the 3rd in Turkey.
	now := time.Date(2026, 11, 2, 21, 30, 0, 0, time.UTC)
	req := func(chargedAt time.Time, charge, refund int64) payments.RefundRequest {
		return payments.RefundRequest{IdempotencyKey: "inv-1:refund", InvoiceID: "inv-1", ChargeID: "77", AmountMinor: refund, ChargeAmountMinor: charge, ChargedAt: chargedAt, Currency: "TRY"}
	}
	run := func(r payments.RefundRequest, reply func(path string) (int, string)) (payments.RefundResult, []string, error) {
		f, c := newFake(t, nil)
		c.now = func() time.Time { return now }
		f.reply = func(path string, _ map[string]any) (int, string) { return reply(path) }
		res, err := c.Refund(context.Background(), r)
		return res, f.paths, err
	}
	cancelled := func(path string) (int, string) {
		if path == "/payment/cancel" {
			return 200, `{"status":"success","paymentId":"77","price":11.16,"cancelHostReference":"cx-1"}`
		}
		return 200, `{"status":"success","paymentId":"77","refundHostReference":"rf-1"}`
	}
	today := time.Date(2026, 11, 2, 21, 5, 0, 0, time.UTC) // 00:05 on the 3rd in Turkey: the same Turkish day as now

	res, paths, err := run(req(today, 1116, 1116), cancelled)
	if err != nil || res.RefundID != "cx-1" || !slices.Equal(paths, []string{"/payment/cancel"}) {
		t.Errorf("whole and the same day: %+v, %v, %v", res, err, paths)
	}
	// Yesterday in Turkey, though the same day in UTC, is not the same day.
	res, paths, err = run(req(time.Date(2026, 11, 2, 20, 59, 0, 0, time.UTC), 1116, 1116), cancelled)
	if err != nil || res.RefundID != "rf-1" || !slices.Equal(paths, []string{"/v2/payment/refund"}) {
		t.Errorf("another day: %+v, %v, %v", res, err, paths)
	}
	// A part of the payment cannot be cancelled; and a payment of unknown age and size is refunded as before.
	if _, paths, _ = run(req(today, 1116, 400), cancelled); !slices.Equal(paths, []string{"/v2/payment/refund"}) {
		t.Errorf("a part: %v", paths)
	}
	if _, paths, _ = run(req(time.Time{}, 0, 1116), cancelled); !slices.Equal(paths, []string{"/v2/payment/refund"}) {
		t.Errorf("unknown payment: %v", paths)
	}
	// The bank refused the cancel (its day is over, or the test card that cannot be cancelled): nothing moved, the payment is refunded.
	res, paths, err = run(req(today, 1116, 1116), func(path string) (int, string) {
		if path == "/payment/cancel" {
			return 200, `{"status":"failure","errorCode":"10223","errorGroup":"REQUIRES_DAY_END","errorMessage":"End of day required"}`
		}
		return cancelled(path)
	})
	if err != nil || res.RefundID != "rf-1" || !slices.Equal(paths, []string{"/payment/cancel", "/v2/payment/refund"}) {
		t.Errorf("a cancel iyzico refused: %+v, %v, %v", res, err, paths)
	}
	// A cancel whose outcome is not known is not followed by a refund: it may have worked.
	for name, reply := range map[string]func(string) (int, string){
		"a server error": func(string) (int, string) { return 500, `` },
		"a system error": func(string) (int, string) {
			return 200, `{"status":"failure","errorCode":"2","errorMessage":"System error occurred"}`
		},
	} {
		_, paths, err = run(req(today, 1116, 1116), reply)
		var transient *payments.TransientError
		if !errors.As(err, &transient) || !slices.Equal(paths, []string{"/payment/cancel"}) {
			t.Errorf("%s: %v, %v", name, err, paths)
		}
	}
}

func TestEveryDocumentedBankGroupHasAWordOfItsOwn(t *testing.T) {
	// The words the gateway's hosted pages explain in six languages (services/gateway/internal/httpapi/hosted_decline_i18n.go): this list and that table are one
	// vocabulary, and a word added to one without the other fails there.
	want := map[string]string{
		"10005": "do_not_honor", "10034": "fraudulent", "10041": "pickup_card", "10043": "lost_card", "10051": "insufficient_funds", "10054": "expired_card",
		"10057": "restricted_card", "10084": "incorrect_cvc", "10093": "restricted_card", "10201": "restricted_card", "10207": "refer_to_issuer", "10209": "blocked_card",
		"10215": "incorrect_number", "10217": "authentication_required", "10220": "generic_decline", "10224": "card_velocity_exceeded", "10225": "restricted_card",
		"10229": "invalid_expiry", "10243": "generic_decline", "10244": "generic_decline", "10248": "amount_too_large", "3005": "payment_method_not_found",
		"3006": "payment_method_not_found", "6001": "fraudulent",
	}
	c := &Client{}
	for code, decline := range want {
		var d *payments.DeclinedError
		if err := c.classify(response{ErrorCode: text(code)}); !errors.As(err, &d) || d.Code != decline {
			t.Errorf("code %s = %v, want %s", code, err, decline)
		}
	}
	if got := len(DeclineWords()); got != 18 {
		t.Errorf("DeclineWords has %d words, the table of hosted pages has 18", got)
	}
}
