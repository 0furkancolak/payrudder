package httpapi

import (
	"net/http"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// A payment link that names its own agreements asks the payer for those instead of the seller's setting (docs/plans/21-contracts.md): the page and the pay request both put
// the link's list in the question to plans-svc, and a link that names none asks the way it always did.
func TestAPaymentLinkAsksForItsOwnAgreements(t *testing.T) {
	ts, c, _ := contractsServer(t)
	c.enforce(false)

	get(t, ts, "/i/"+tokOpenSandbox)
	if got := c.asked[len(c.asked)-1].GetRequiredKinds(); len(got) != 0 {
		t.Errorf("a link that names none leaves the setting alone: %v", got)
	}

	get(t, ts, "/i/"+tokLinkAgreements)
	if got := c.asked[len(c.asked)-1].GetRequiredKinds(); len(got) != 1 || got[0] != "privacy" {
		t.Errorf("the page of a link with its own list asked for %v", got)
	}

	// The pay request is gated by the same question, so the list cannot be dodged by posting without the page.
	before := len(c.asked)
	resetPayment()
	send(t, noRedirect(), http.MethodPost, ts.URL+"/i/"+tokLinkAgreements+"/pay", agreeForm(), nil)
	if len(c.asked) == before {
		t.Fatal("the pay request did not ask plans-svc what the link requires")
	}
	for _, a := range c.asked[before:] {
		if got := a.GetRequiredKinds(); len(got) != 1 || got[0] != "privacy" {
			t.Errorf("the pay request asked for %v, not the link's list", got)
		}
	}
	if paid() != nil {
		t.Error("a payment started without the link's agreement being accepted")
	}
}

// The acceptance log is read a page at a time with filters, and the query string reaches plans-svc as typed.
func TestTheAcceptanceLogTakesFiltersAndPages(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts
	c.accepted = nil
	c.nextPage = "next-1"

	r, m := do(t, ts, http.MethodGet, "/v1/customers/cus_1/contract-acceptances?kind=tos&source=hosted&version_id=ver-1&from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z&page_size=25&page_token=tok", "billing-key", ``)
	if r.StatusCode != http.StatusOK || m["next_page_token"] != "next-1" {
		t.Fatalf("list: %d %v", r.StatusCode, m)
	}
	got := c.listed
	if got.GetCustomerExternalId() != "cus_1" || got.GetKind() != "tos" || got.GetSource() != "hosted" || got.GetVersionId() != "ver-1" || got.GetPageSize() != 25 || got.GetPageToken() != "tok" ||
		!got.GetFromTime().AsTime().Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) || !got.GetToTime().AsTime().Equal(timestamppb.New(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)).AsTime()) {
		t.Errorf("plans-svc was asked %+v", got)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/customers/cus_1/contract-acceptances?from=yesterday", "billing-key", ``); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a time that is not RFC 3339: %d", r.StatusCode)
	}
	if r, _ := do(t, ts, http.MethodGet, "/v1/customers/cus_1/contract-acceptances?page_size=x", "billing-key", ``); r.StatusCode != http.StatusBadRequest {
		t.Errorf("a page size that is not a number: %d", r.StatusCode)
	}
}

// A link may name agreements only once their text is published, as the setting may, and the list reaches billing as sent.
func TestAPaymentLinkNamesAgreementsOnlyOncePublished(t *testing.T) {
	ts, _ := newTestServer(t)
	c := lastContracts
	c.mu.Lock()
	c.settings = &plansv1.ContractSettings{PublishedKinds: []string{"tos"}}
	c.mu.Unlock()

	r, m := do(t, ts, http.MethodPost, "/v1/payment-links", "live-key", `{"kind":"adhoc","amount_minor":"2500","description":"Setup","required_contracts":["tos"]}`)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("a published kind: %d %v", r.StatusCode, m)
	}
	billingMu.Lock()
	got := lastCreate.GetRequiredContracts()
	billingMu.Unlock()
	if len(got) != 1 || got[0] != "tos" {
		t.Errorf("billing was asked for %v", got)
	}

	billingMu.Lock()
	lastCreate = nil
	billingMu.Unlock()
	r, m = do(t, ts, http.MethodPost, "/v1/payment-links", "live-key", `{"kind":"adhoc","amount_minor":"2500","description":"Setup","required_contracts":["tos","privacy"]}`)
	billingMu.Lock()
	made := lastCreate
	billingMu.Unlock()
	if r.StatusCode < 400 || r.StatusCode >= 500 || errKey(m) != "plans.contract.kind_unpublished" || made != nil {
		t.Errorf("an unpublished kind: %d %v, reached billing: %v", r.StatusCode, m, made)
	}
}
