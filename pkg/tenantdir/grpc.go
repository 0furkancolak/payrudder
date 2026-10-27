package tenantdir

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// listPage is the page size of the listing calls.
const listPage = 500

// NewGRPC returns a Directory that asks plans-svc's DirectoryService as the operator.
func NewGRPC(client plansv1.DirectoryServiceClient, o Options) *Directory {
	load := func(ctx context.Context, id uuid.UUID) (Placement, error) {
		resp, err := client.GetPlacement(tenant.OutgoingContext(tenant.WithOperator(ctx)), &plansv1.GetPlacementRequest{TenantId: id.String()})
		if status.Code(err) == codes.NotFound {
			return Placement{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		if err != nil {
			return Placement{}, err
		}
		return FromProto(resp.GetPlacement())
	}
	list := func(ctx context.Context, cell string) ([]Placement, error) {
		var out []Placement
		token := ""
		for {
			resp, err := client.ListPlacements(tenant.OutgoingContext(tenant.WithOperator(ctx)),
				&plansv1.ListPlacementsRequest{Cell: cell, PageSize: listPage, PageToken: token})
			if err != nil {
				return nil, err
			}
			for _, p := range resp.GetPlacements() {
				pl, err := FromProto(p)
				if err != nil {
					return nil, err
				}
				out = append(out, pl)
			}
			if token = resp.GetNextPageToken(); token == "" {
				return out, nil
			}
		}
	}
	return New(load, list, o)
}

// FromProto converts a directory row; an unknown status is an error, never "ready".
func FromProto(p *plansv1.Placement) (Placement, error) {
	id, err := uuid.Parse(p.GetTenantId())
	if err != nil {
		return Placement{}, fmt.Errorf("tenantdir: placement with tenant id %q", p.GetTenantId())
	}
	var s Status
	switch p.GetStatus() {
	case plansv1.PlacementStatus_PLACEMENT_STATUS_READY:
		s = StatusReady
	case plansv1.PlacementStatus_PLACEMENT_STATUS_MOVING:
		s = StatusMoving
	default:
		return Placement{}, fmt.Errorf("tenantdir: tenant %s has placement status %v", id, p.GetStatus())
	}
	return Placement{TenantID: id, Cell: p.GetCell(), Region: p.GetRegion(), Status: s}, nil
}

// ToProto converts a placement for DirectoryService.
func ToProto(p Placement) *plansv1.Placement {
	s := plansv1.PlacementStatus_PLACEMENT_STATUS_UNSPECIFIED
	switch p.Status {
	case StatusReady:
		s = plansv1.PlacementStatus_PLACEMENT_STATUS_READY
	case StatusMoving:
		s = plansv1.PlacementStatus_PLACEMENT_STATUS_MOVING
	}
	return &plansv1.Placement{TenantId: p.TenantID.String(), Cell: p.Cell, Region: p.Region, Status: s}
}
