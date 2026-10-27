package cells

import (
	"context"
	"io"
	"strings"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

func stringsReader(s string) io.Reader { return strings.NewReader(s) }

// Directory is the tenant directory as the operator tool uses it: it asks the owner (never a cache) and changes the placement through the steps of a move.
type Directory interface {
	// Placement is where the tenant lives now.
	Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error)
	// Placed lists the tenants placed in a cell ("" for every cell).
	Placed(ctx context.Context, cell string) ([]tenantdir.Placement, error)
	// BeginMove fences the tenant for a move to toCell ("" for the pooled database).
	BeginMove(ctx context.Context, id uuid.UUID, toCell string) error
	// FinishMove ends the move in cell/region.
	FinishMove(ctx context.Context, id uuid.UUID, cell, region string) error
	// AbortMove ends the move where it began.
	AbortMove(ctx context.Context, id uuid.UUID) error
}

// GRPC is a Directory over plans-svc's DirectoryService, called as the operator. Nothing is cached: an operator acts on what the owner says now.
type GRPC struct {
	client plansv1.DirectoryServiceClient
}

// NewGRPC returns the Directory over client.
func NewGRPC(client plansv1.DirectoryServiceClient) *GRPC { return &GRPC{client: client} }

func opCtx(ctx context.Context) context.Context {
	return tenant.OutgoingContext(tenant.WithOperator(ctx))
}

// Placement implements Directory.
func (g *GRPC) Placement(ctx context.Context, id uuid.UUID) (tenantdir.Placement, error) {
	resp, err := g.client.GetPlacement(opCtx(ctx), &plansv1.GetPlacementRequest{TenantId: id.String()})
	if err != nil {
		return tenantdir.Placement{}, err
	}
	return tenantdir.FromProto(resp.GetPlacement())
}

// Placed implements Directory.
func (g *GRPC) Placed(ctx context.Context, cell string) ([]tenantdir.Placement, error) {
	var out []tenantdir.Placement
	token := ""
	for {
		resp, err := g.client.ListPlacements(opCtx(ctx), &plansv1.ListPlacementsRequest{Cell: cell, PageSize: 500, PageToken: token})
		if err != nil {
			return nil, err
		}
		for _, p := range resp.GetPlacements() {
			pl, err := tenantdir.FromProto(p)
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

// BeginMove implements Directory.
func (g *GRPC) BeginMove(ctx context.Context, id uuid.UUID, toCell string) error {
	_, err := g.client.BeginMove(opCtx(ctx), &plansv1.BeginMoveRequest{TenantId: id.String(), ToCell: toCell})
	return err
}

// FinishMove implements Directory.
func (g *GRPC) FinishMove(ctx context.Context, id uuid.UUID, cell, region string) error {
	_, err := g.client.FinishMove(opCtx(ctx), &plansv1.FinishMoveRequest{TenantId: id.String(), Cell: cell, Region: region})
	return err
}

// AbortMove implements Directory.
func (g *GRPC) AbortMove(ctx context.Context, id uuid.UUID) error {
	_, err := g.client.AbortMove(opCtx(ctx), &plansv1.AbortMoveRequest{TenantId: id.String()})
	return err
}
