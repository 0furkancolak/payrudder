// Package paymentgate is the one place that decides whether something that takes money or grants access may start for a customer of a tenant: a subscription, a
// plan change, a payment link, a credit pack, the payment of an invoice. Every such path asks it, so that "blocked" and "tenant on hold" mean the same thing
// everywhere (docs/plans/13-customer-ban.md has the table of the paths; each has a test). The callers turn a verdict into their own error key.
package paymentgate

import (
	"time"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// Op is what is being started.
type Op int

const (
	// OpPurchase is something new that is paid for or granted: a subscription, a plan change, a payment link, a credit pack, the first use of an ad-hoc link.
	OpPurchase Op = iota
	// OpPayInvoice is the payment of an invoice that exists already. What was invoiced before a block stays payable, unless the block says the customer is not to
	// pay at all (fraud, chargeback).
	OpPayInvoice
)

// Verdict is the gate's answer.
type Verdict int

const (
	Allowed Verdict = iota
	// CustomerBlocked: the seller has blocked this customer (plans.customer.blocked, billing.customer.blocked).
	CustomerBlocked
	// TenantOnHold: the operator has suspended, frozen or banned the tenant: nothing is charged and no payment is taken (plans.tenant.on_hold,
	// billing.tenant.on_hold).
	TenantOnHold
)

// Block is what the gate reads of a customer's block. The zero value is no block.
type Block struct {
	At       *time.Time
	Until    *time.Time
	Category string
}

// BlockOf reads the block of an API customer.
func BlockOf(c *plansv1.Customer) Block {
	b := Block{Category: c.GetBlockCategory()}
	if t := c.GetBlockTime(); t != nil {
		at := t.AsTime()
		b.At = &at
	}
	if t := c.GetBlockedUntil(); t != nil {
		until := t.AsTime()
		b.Until = &until
	}
	return b
}

// InForce reports whether the block holds at now: it is set, and has not ended by itself.
func (b Block) InForce(now time.Time) bool {
	return b.At != nil && (b.Until == nil || b.Until.After(now))
}

// OnHold reports whether the operator has taken the tenant out of service.
func OnHold(s plansv1.TenantStatus) bool {
	switch s {
	case plansv1.TenantStatus_TENANT_STATUS_SUSPENDED, plansv1.TenantStatus_TENANT_STATUS_FROZEN, plansv1.TenantStatus_TENANT_STATUS_BANNED:
		return true
	case plansv1.TenantStatus_TENANT_STATUS_UNSPECIFIED, plansv1.TenantStatus_TENANT_STATUS_INACTIVE, plansv1.TenantStatus_TENANT_STATUS_ACTIVE:
	}
	return false
}

// Check says whether op may start for a customer with this block, in a tenant with this status, at now. A tenant on hold refuses everything; a blocked customer
// refuses everything new, and refuses to pay an invoice only for a fraud or chargeback block.
func Check(op Op, tenant plansv1.TenantStatus, b Block, now time.Time) Verdict {
	switch {
	case OnHold(tenant):
		return TenantOnHold
	case !b.InForce(now):
		return Allowed
	case op == OpPayInvoice && b.Category != "fraud" && b.Category != "chargeback":
		return Allowed
	}
	return CustomerBlocked
}
