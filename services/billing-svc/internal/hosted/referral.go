package hosted

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted/query"
)

// The referral cookie of a visitor (phase 47) reaches billing-svc with the request that starts a payment: the code and when the link was followed. Billing does not
// judge it. It tells the affiliate program, which knows the program's window, the affiliate's status and the self-referral rules, and the answer is not waited for
// by the payment: a payment never fails because of a referral.

// refer passes the referral of a customer who is paying on to the affiliate program.
func (h *Handler) refer(ctx context.Context, tenantID, customerID uuid.UUID, code string, touched *timestamppb.Timestamp) {
	if code == "" || customerID == uuid.Nil || h.d.Dir == nil {
		return
	}
	var at time.Time
	if touched != nil {
		at = touched.AsTime()
	}
	h.d.Dir.Attribute(ctx, tenantID, customerID, code, at)
}

// linkCustomer finds the customer a payment link is made out to, when the request carries a referral to pass on. A link made for nobody (a guest payment) has no
// customer, and there is nothing to attribute.
func (h *Handler) linkCustomer(ctx context.Context, token, code string) (tenantID, customerID uuid.UUID) {
	if code == "" || h.d.Dir == nil {
		return uuid.Nil, uuid.Nil
	}
	link, err := query.ResolveLink(ctx, h.q.System, token)
	if err != nil || link.Row.CustomerExternalID == nil || *link.Row.CustomerExternalID == "" {
		return uuid.Nil, uuid.Nil
	}
	cu, found, err := h.d.Dir.Customer(ctx, link.Row.TenantID, *link.Row.CustomerExternalID)
	if err != nil || !found {
		return uuid.Nil, uuid.Nil
	}
	id, err := uuid.Parse(cu.GetId())
	if err != nil {
		return uuid.Nil, uuid.Nil
	}
	return link.Row.TenantID, id
}
