package iyzico

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Card storage and instalments (docs.iyzico.com/en/advanced/card-storage and installment-and-bin-service, read 2026-11-02).
//
// What is not here, on purpose: a call that puts a card into storage. POST /cardstorage/card and the registerCard field of a payment both take the card number, expiry
// and CVC, and the checkout form stores no card for the merchant ("cards saved on the payment form are kept in the customer's iyzico wallet; cardUserKey and cardToken
// are not returned to you", docs.iyzico.com/sss/abonelik-ve-kart-saklama). Payrudder holds no card numbers, so the cardUserKey and cardToken of a customer come from
// whoever does (the seller's own PCI-compliant page) through the payment method of the customer, and Payrudder lists and deletes the stored cards, charges them and
// authenticates a payment with them (StartThreeDS).

const (
	cardsPath       = "/cardstorage/cards"
	cardPath        = "/cardstorage/card"
	installmentPath = "/payment/iyzipos/installment"
)

type cardList struct {
	response
	Cards []struct {
		Token       string `json:"cardToken"`
		Alias       string `json:"cardAlias"`
		BIN         string `json:"binNumber"`
		Last4       string `json:"lastFourDigits"`
		Type        string `json:"cardType"`
		Association string `json:"cardAssociation"`
		Family      string `json:"cardFamily"`
		Bank        string `json:"cardBankName"`
		ExpireMonth string `json:"expireMonth"`
		ExpireYear  string `json:"expireYear"`
	} `json:"cardDetails"`
}

// CardUserKey implements payments.CardStore: the user key of a payment method of the form "cardUserKey:cardToken".
func (c *Client) CardUserKey(pm string) (string, bool) {
	userKey, _, err := SplitPaymentMethod(pm)
	return userKey, err == nil
}

// StoredCardInstallments implements payments.InstallmentOffer: the instalment options of the account's enabled counts (Settings.EnabledInstallments, more than one
// payment) that iyzico quotes for the customer's stored card and the amount. The card's BIN is read from the card storage list (the token of the payment method
// picks the card) and handed to the instalment query. Nothing is offered, with no error, when the account enables no instalments, the card is not in the list or its
// BIN is not the 8 digits the query takes (UNVERIFIED how long the BIN of a stored card is: the page of the list does not say); an error of iyzico is returned so the
// caller can log it, and the payer then pays once.
func (c *Client) StoredCardInstallments(ctx context.Context, pm string, amountMinor int64, currency string) ([]payments.InstallmentOption, error) {
	var enabled []int
	for _, n := range c.s.EnabledInstallments {
		if n > 1 {
			enabled = append(enabled, n)
		}
	}
	if len(enabled) == 0 {
		return nil, nil
	}
	userKey, token, err := SplitPaymentMethod(pm)
	if err != nil {
		return nil, err
	}
	cards, err := c.ListCards(ctx, userKey)
	if err != nil {
		return nil, err
	}
	bin := ""
	for _, k := range cards {
		if k.Token == token {
			bin = k.BIN
		}
	}
	if len(bin) != 8 || strings.Trim(bin, "0123456789") != "" {
		return nil, nil
	}
	quoted, err := c.Installments(ctx, amountMinor, currency, bin)
	if err != nil {
		return nil, err
	}
	// The query named the card's BIN, so the answer is that card's: its first (only) entry.
	var out []payments.InstallmentOption
	if len(quoted) > 0 {
		for _, o := range quoted[0].Options {
			if slices.Contains(enabled, o.Count) && o.TotalMinor >= amountMinor {
				out = append(out, o)
			}
		}
	}
	return out, nil
}

// ListCards implements payments.CardStore: the cards iyzico holds under a cardUserKey. A cardUserKey iyzico does not know (3005) is an error of the request, not an
// empty list. UNVERIFIED: that the account has the card storage add-on (3007 says so when it has not).
func (c *Client) ListCards(ctx context.Context, userKey string) ([]payments.StoredCard, error) {
	if userKey == "" {
		return nil, errors.New("iyzico: listing cards needs a cardUserKey")
	}
	var out cardList
	if err := c.doInto(ctx, cardsPath, map[string]any{"locale": "en", "cardUserKey": userKey}, &out); err != nil {
		return nil, err
	}
	if !out.ok() {
		return nil, c.cardError(out.response)
	}
	cards := make([]payments.StoredCard, 0, len(out.Cards))
	for _, k := range out.Cards {
		cards = append(cards, payments.StoredCard{Token: k.Token, Alias: k.Alias, BIN: k.BIN, Last4: k.Last4, Type: k.Type, Association: k.Association, Family: k.Family,
			Bank: k.Bank, ExpireMonth: k.ExpireMonth, ExpireYear: k.ExpireYear})
	}
	return cards, nil
}

// DeleteCard implements payments.CardStore: DELETE /cardstorage/card with the user key and the token in the body (the page documents a body on a DELETE). UNVERIFIED:
// that the signature covers that body like any other (randomKey + path + body); a delete is repeatable, a second one is answered 3006 ("cardToken is not found").
func (c *Client) DeleteCard(ctx context.Context, userKey, token string) error {
	if userKey == "" || token == "" {
		return errors.New("iyzico: deleting a card needs a cardUserKey and a cardToken")
	}
	var out response
	if err := c.doMethod(ctx, http.MethodDelete, cardPath, map[string]any{"locale": "en", "cardUserKey": userKey, "cardToken": token}, &out); err != nil {
		return err
	}
	if !out.ok() {
		return c.cardError(out)
	}
	return nil
}

// cardError is a refused card storage call: the card storage codes (3000 to 3013) are the caller's mistakes or a missing add-on, none of them a decline.
func (c *Client) cardError(r response) error {
	err := c.classify(r)
	var d *payments.DeclinedError
	if errors.As(err, &d) {
		return &Error{Code: string(r.ErrorCode), Message: r.ErrorMessage}
	}
	return err
}

type installmentAnswer struct {
	response
	Details []struct {
		BIN         string `json:"binNumber"`
		Type        string `json:"cardType"`
		Association string `json:"cardAssociation"`
		Family      string `json:"cardFamilyName"`
		Bank        string `json:"bankName"`
		Force3DS    int    `json:"force3ds"`
		Prices      []struct {
			Installment text `json:"installmentPrice"`
			Total       text `json:"totalPrice"`
			Number      int  `json:"installmentNumber"`
		} `json:"installmentPrices"`
	} `json:"installmentDetails"`
}

// Installments implements payments.InstallmentQuoter: POST /payment/iyzipos/installment with the price and, to narrow the answer to one card, the first 8 digits of
// its number. The rates are the account's own ("Installment and Commission Management" in the panel). A card with force3ds 1 must be authenticated with the payer.
func (c *Client) Installments(ctx context.Context, amountMinor int64, currency, bin string) ([]payments.InstallmentCard, error) {
	amount, err := price(amountMinor, currency)
	if err != nil {
		return nil, err
	}
	if bin != "" && (len(bin) != 8 || strings.Trim(bin, "0123456789") != "") {
		return nil, errors.New("iyzico: a BIN is the first 8 digits of a card")
	}
	req := map[string]any{"locale": "en", "price": amount}
	if bin != "" {
		req["binNumber"] = bin
	}
	var out installmentAnswer
	if err := c.doInto(ctx, installmentPath, req, &out); err != nil {
		return nil, err
	}
	if !out.ok() {
		return nil, c.classify(out.response)
	}
	cur, _ := money.LookupCurrency(currency)
	var cards []payments.InstallmentCard
	for _, d := range out.Details {
		card := payments.InstallmentCard{BIN: d.BIN, Type: d.Type, Association: d.Association, Family: d.Family, Bank: d.Bank, Force3DS: d.Force3DS == 1}
		for _, p := range d.Prices {
			each, ok1 := parseMinor(string(p.Installment), cur.Exponent)
			total, ok2 := parseMinor(string(p.Total), cur.Exponent)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("iyzico: instalment prices %q and %q cannot be read in %s", p.Installment, p.Total, currency)
			}
			card.Options = append(card.Options, payments.InstallmentOption{Count: p.Number, InstallmentMinor: each, TotalMinor: total})
		}
		cards = append(cards, card)
	}
	return cards, nil
}
