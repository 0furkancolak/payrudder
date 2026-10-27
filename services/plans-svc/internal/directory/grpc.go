// Package directory is the module behind DirectoryService: where each tenant's data lives (ADR 0018). It is operator-only and has no gateway route; the
// other services read it through pkg/tenantdir.
package directory

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// Server serves DirectoryService.
type Server struct {
	plansv1.UnimplementedDirectoryServiceServer
	qry *query.Queries
}

// New returns the directory server.
func New(qry *query.Queries) *Server { return &Server{qry: qry} }

func (s *Server) GetPlacement(ctx context.Context, req *plansv1.GetPlacementRequest) (*plansv1.GetPlacementResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	p, err := s.qry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &plansv1.GetPlacementResponse{Placement: p}, nil
}

func (s *Server) ListPlacements(ctx context.Context, req *plansv1.ListPlacementsRequest) (*plansv1.ListPlacementsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	ps, next, err := s.qry.List(ctx, req.GetCell(), req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	return &plansv1.ListPlacementsResponse{Placements: ps, NextPageToken: next}, nil
}
