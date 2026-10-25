package query

import (
	"google.golang.org/grpc/codes"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/affiliates/model"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Unavailable is the answer for a seller without a program that is open to applications, whatever the reason: no program, one in draft or paused, a live environment
// that is not active, or an id that is no tenant at all.
func Unavailable() error {
	return errkey.New(codes.NotFound, "affiliate.portal.program_unavailable", "this seller has no partner program open to applications")
}

// Open reports whether a program takes applications now: it is active, and so is the tenant (a live environment that has not gone live does not).
func Open(r sqlc.PortalProgramRow) bool {
	return r.Status == model.ProgramActive && r.TenantStatus == domain.StatusActive
}

// ProgramOf maps the program a person is about to apply to.
func ProgramOf(r sqlc.PortalProgramRow) *plansv1.MyProgram {
	p := &plansv1.MyProgram{Status: r.Status, CommissionKind: r.CommissionKind, CommissionValue: r.CommissionValue, CommissionTerm: r.CommissionTerm,
		AttributionWindowDays: r.AttributionWindowDays, HoldDays: r.HoldDays, MinPayoutMinor: r.MinPayoutMinor}
	if r.Currency != nil {
		p.Currency = *r.Currency
	}
	if r.CommissionTermValue != nil {
		p.CommissionTermValue = *r.CommissionTermValue
	}
	return p
}
