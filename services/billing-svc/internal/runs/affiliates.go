package runs

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
)

// attributeTimeout bounds the wait for the affiliate program: a referral is never worth a slow payment page.
const attributeTimeout = 2 * time.Second

// Attribute tells the affiliate program that a customer who is paying arrived through a partner's link (the code in the visitor's referral cookie). The program
// decides: first touch wins, the window, the affiliate's status and the self-referral rules are its own. Any failure is logged and forgotten.
func (m *Manager) Attribute(ctx context.Context, tenantID, customerID uuid.UUID, code string, touched time.Time) {
	if m.cfg.Affiliates == nil || code == "" || customerID == uuid.Nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, attributeTimeout)
	defer cancel()
	req := &plansv1.AttributeCustomerRequest{CustomerId: customerID.String(), Code: code, Source: "link"}
	if !touched.IsZero() {
		req.TouchTime = timestamppb.New(touched)
	}
	resp, err := m.cfg.Affiliates.AttributeCustomer(inputs.TenantCtx(ctx, tenantID), req)
	if err != nil {
		m.log.WarnContext(ctx, "referral not passed on to the affiliate program", "tenant", tenantID, "customer", customerID, "error", err)
		return
	}
	if !resp.GetAttributed() {
		m.log.DebugContext(ctx, "referral not attributed", "tenant", tenantID, "customer", customerID, "reason", resp.GetReason())
	}
}
