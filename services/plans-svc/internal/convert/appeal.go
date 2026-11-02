package convert

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// TenantAppeal converts an owner's appeal against a hold.
func TenantAppeal(a sqlc.TenantAppeal) *plansv1.TenantAppeal {
	out := &plansv1.TenantAppeal{Id: a.ID.String(), TenantId: a.LiveTenantID.String(), Status: a.Status, HeldStatus: a.HeldStatus, Statement: a.Statement, SubmittedBy: a.SubmittedBy,
		Answer: a.Answer, DecidedBy: a.DecidedBy, CreateTime: timestamppb.New(a.CreatedAt)}
	if a.DecidedAt != nil {
		out.DecideTime = timestamppb.New(*a.DecidedAt)
	}
	return out
}
