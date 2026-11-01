package domain

import (
	"time"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/paymentgate"
)

// Gate asks the one payment gate (pkg/paymentgate) whether op may start for a customer with this block, in a tenant with this stored status, and turns a refusal
// into the error of plans-svc. what says what is refused ("no new subscription").
func Gate(op paymentgate.Op, tenantStatus string, b paymentgate.Block, now time.Time, what string) error {
	switch paymentgate.Check(op, StatusFromDB(tenantStatus), b, now) {
	case paymentgate.TenantOnHold:
		return errkey.New(codes.FailedPrecondition, "plans.tenant.on_hold", "this tenant is on hold: "+what)
	case paymentgate.CustomerBlocked:
		return errkey.New(codes.FailedPrecondition, "plans.customer.blocked", "this customer is blocked: "+what)
	case paymentgate.Allowed:
	}
	return nil
}
