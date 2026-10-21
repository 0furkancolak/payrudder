package httpapi

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

func (fakeBilling) CreateCreditPack(_ context.Context, req *billingv1.CreateCreditPackRequest) (*billingv1.CreateCreditPackResponse, error) {
	if req.GetCredits() < 1 {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.bad_pack", "a pack needs credits and a price above zero")
	}
	return &billingv1.CreateCreditPackResponse{Pack: &billingv1.CreditPack{Id: "pack-1", Name: req.GetName(), Credits: req.GetCredits(), PriceMinor: req.GetPriceMinor(), Currency: req.GetCurrency(), Active: true}}, nil
}

func (fakeBilling) ListCreditPacks(_ context.Context, req *billingv1.ListCreditPacksRequest) (*billingv1.ListCreditPacksResponse, error) {
	packs := []*billingv1.CreditPack{{Id: "pack-1", Name: "Starter", Credits: 1000, Active: true}}
	if !req.GetActiveOnly() {
		packs = append(packs, &billingv1.CreditPack{Id: "pack-2", Name: "Old", Credits: 10})
	}
	return &billingv1.ListCreditPacksResponse{Packs: packs}, nil
}

func (fakeBilling) GetCreditBalance(_ context.Context, req *billingv1.GetCreditBalanceRequest) (*billingv1.GetCreditBalanceResponse, error) {
	if req.GetCustomerId() != "cust-uuid" {
		return nil, errkey.New(codes.Internal, "common.internal", "the gateway must pass the internal customer id")
	}
	return &billingv1.GetCreditBalanceResponse{Balance: 120}, nil
}

func (fakeBilling) ConsumeCredits(_ context.Context, req *billingv1.ConsumeCreditsRequest) (*billingv1.ConsumeCreditsResponse, error) {
	if req.GetCustomerId() != "cust-uuid" {
		return nil, errkey.New(codes.Internal, "common.internal", "the gateway must pass the internal customer id")
	}
	if req.GetIdempotencyKey() == "" {
		return nil, errkey.New(codes.InvalidArgument, "billing.credits.key_required", "an idempotency key is required to consume credits")
	}
	if req.GetCredits() > 120 {
		return nil, errkey.New(codes.FailedPrecondition, "billing.credits.insufficient", "the customer has too few credits")
	}
	return &billingv1.ConsumeCreditsResponse{Balance: 120 - req.GetCredits(), Replayed: req.GetIdempotencyKey() == "again"}, nil
}

func (fakeBilling) GrantCredits(_ context.Context, req *billingv1.GrantCreditsRequest) (*billingv1.GrantCreditsResponse, error) {
	return &billingv1.GrantCreditsResponse{Balance: 120 + req.GetCredits()}, nil
}

func (fakeBilling) ListCreditLedger(_ context.Context, req *billingv1.ListCreditLedgerRequest) (*billingv1.ListCreditLedgerResponse, error) {
	return &billingv1.ListCreditLedgerResponse{Entries: []*billingv1.CreditLedgerEntry{{Id: "e1", Kind: "consume", Amount: -5}}, NextPageToken: req.GetPageToken() + "x"}, nil
}

func TestCreditPacks(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "POST", "/v1/credit-packs", "live-key", `{"name":"Starter","credits":1000,"price_minor":500,"currency":"USD"}`)
	if resp.StatusCode != http.StatusCreated || m["id"] != "pack-1" || m["credits"] != "1000" {
		t.Fatalf("create: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/credit-packs", "live-key", `{"name":"x"}`); resp.StatusCode != 400 || errKey(m) != "billing.credits.bad_pack" {
		t.Fatalf("bad pack: %d %v", resp.StatusCode, m)
	}
	_, m = do(t, ts, "GET", "/v1/credit-packs?active=true", "live-key", "")
	if len(m["packs"].([]any)) != 1 {
		t.Fatalf("active packs: %v", m)
	}
	_, m = do(t, ts, "GET", "/v1/credit-packs", "live-key", "")
	if len(m["packs"].([]any)) != 2 {
		t.Fatalf("all packs: %v", m)
	}
}

func TestCustomerCredits(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, m := do(t, ts, "GET", "/v1/customers/cus_1/credits", "live-key", "")
	if resp.StatusCode != 200 || m["balance"] != "120" {
		t.Fatalf("balance: %d %v", resp.StatusCode, m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/customers/nobody/credits", "live-key", ""); resp.StatusCode != 404 {
		t.Fatalf("unknown customer: %d", resp.StatusCode)
	}
	resp, m = do(t, ts, "POST", "/v1/customers/cus_1/credits/consume", "live-key", `{"credits":5,"idempotency_key":"k1"}`)
	if resp.StatusCode != 200 || m["balance"] != "115" {
		t.Fatalf("consume: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/credits/consume", "live-key", `{"credits":5}`); resp.StatusCode != 400 || errKey(m) != "billing.credits.key_required" {
		t.Fatalf("no key: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/credits/consume", "live-key", `{"credits":500,"idempotency_key":"k2"}`); resp.StatusCode != 409 || errKey(m) != "billing.credits.insufficient" {
		t.Fatalf("too many: %d %v", resp.StatusCode, m)
	}
	if resp, m := do(t, ts, "POST", "/v1/customers/cus_1/credits/grant", "live-key", `{"credits":30,"source":"promo"}`); resp.StatusCode != 200 || m["balance"] != "150" {
		t.Fatalf("grant: %d %v", resp.StatusCode, m)
	}
	_, m = do(t, ts, "GET", "/v1/customers/cus_1/credits/ledger?page_size=5", "live-key", "")
	if len(m["entries"].([]any)) != 1 || m["next_page_token"] != "x" {
		t.Fatalf("ledger: %v", m)
	}
	if resp, _ := do(t, ts, "GET", "/v1/customers/cus_1/credits/ledger?page_size=500", "live-key", ""); resp.StatusCode != 400 {
		t.Fatalf("page size: %d", resp.StatusCode)
	}
}

func errKey(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	k, _ := e["key"].(string)
	return k
}
