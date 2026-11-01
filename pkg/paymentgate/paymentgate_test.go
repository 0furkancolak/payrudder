package paymentgate

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

func TestCheck(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	active, held := plansv1.TenantStatus_TENANT_STATUS_ACTIVE, plansv1.TenantStatus_TENANT_STATUS_FROZEN
	for _, tc := range []struct {
		name   string
		op     Op
		tenant plansv1.TenantStatus
		block  Block
		want   Verdict
	}{
		{"no block, new purchase", OpPurchase, active, Block{}, Allowed},
		{"blocked, new purchase", OpPurchase, active, Block{At: &past}, CustomerBlocked},
		{"blocked until later, new purchase", OpPurchase, active, Block{At: &past, Until: &future}, CustomerBlocked},
		{"block ended by itself, new purchase", OpPurchase, active, Block{At: &past, Until: &past}, Allowed},
		{"blocked, pay an invoice that exists", OpPayInvoice, active, Block{At: &past, Category: "abuse"}, Allowed},
		{"blocked without a category, pay an invoice", OpPayInvoice, active, Block{At: &past}, Allowed},
		{"fraud, pay an invoice", OpPayInvoice, active, Block{At: &past, Category: "fraud"}, CustomerBlocked},
		{"chargeback, pay an invoice", OpPayInvoice, active, Block{At: &past, Category: "chargeback"}, CustomerBlocked},
		{"fraud that ended, pay an invoice", OpPayInvoice, active, Block{At: &past, Until: &past, Category: "fraud"}, Allowed},
		{"tenant on hold, new purchase", OpPurchase, held, Block{}, TenantOnHold},
		{"tenant on hold, pay an invoice", OpPayInvoice, held, Block{}, TenantOnHold},
		{"tenant on hold wins over a block", OpPurchase, plansv1.TenantStatus_TENANT_STATUS_BANNED, Block{At: &past}, TenantOnHold},
		{"suspended", OpPayInvoice, plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, Block{}, TenantOnHold},
		{"a status the sender did not set counts as active", OpPurchase, plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED, Block{}, Allowed},
	} {
		if got := Check(tc.op, tc.tenant, tc.block, now); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBlockOfReadsTheCustomer(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	c := &plansv1.Customer{BlockTime: timestamppb.New(now.Add(-time.Hour)), BlockedUntil: timestamppb.New(now.Add(time.Hour)), BlockCategory: "fraud"}
	if b := BlockOf(c); !b.InForce(now) || b.Category != "fraud" {
		t.Errorf("block = %+v", b)
	}
	if BlockOf(nil).InForce(now) || BlockOf(&plansv1.Customer{}).InForce(now) {
		t.Error("no customer, or one without a block, is not blocked")
	}
}
