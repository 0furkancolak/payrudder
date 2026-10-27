// Package directory is the module behind DirectoryService: where each tenant's data lives (ADR 0018). It is operator-only and has no gateway route; the
// other services read it through pkg/tenantdir.
package directory

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
)

// Server serves DirectoryService.
type Server struct {
	plansv1.UnimplementedDirectoryServiceServer
	qry *query.Queries
	cmd *command.Commands
}

// New returns the directory server.
func New(qry *query.Queries, cmd *command.Commands) *Server { return &Server{qry: qry, cmd: cmd} }

func (s *Server) BeginMove(ctx context.Context, req *plansv1.BeginMoveRequest) (*plansv1.BeginMoveResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	p, err := s.cmd.BeginMove(ctx, req.GetTenantId(), req.GetToCell())
	if err != nil {
		return nil, err
	}
	return &plansv1.BeginMoveResponse{Placement: p}, nil
}

func (s *Server) FinishMove(ctx context.Context, req *plansv1.FinishMoveRequest) (*plansv1.FinishMoveResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	p, err := s.cmd.FinishMove(ctx, req.GetTenantId(), req.GetCell(), req.GetRegion())
	if err != nil {
		return nil, err
	}
	return &plansv1.FinishMoveResponse{Placement: p}, nil
}

func (s *Server) AbortMove(ctx context.Context, req *plansv1.AbortMoveRequest) (*plansv1.AbortMoveResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	p, err := s.cmd.AbortMove(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	return &plansv1.AbortMoveResponse{Placement: p}, nil
}

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
