package blocklist

import (
	"context"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Handler serves the blocklist methods of PlansService (the app's list).
type Handler struct{ lists *Lists }

// NewHandler returns the app's blocklist handlers.
func NewHandler(l *Lists) *Handler { return &Handler{lists: l} }

func (h *Handler) ListBlocklist(ctx context.Context, _ *plansv1.ListBlocklistRequest) (*plansv1.ListBlocklistResponse, error) {
	return h.lists.List(ctx)
}

func (h *Handler) AddBlocklistEntry(ctx context.Context, req *plansv1.AddBlocklistEntryRequest) (*plansv1.AddBlocklistEntryResponse, error) {
	e, err := h.lists.Add(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.AddBlocklistEntryResponse{Entry: e}, nil
}

func (h *Handler) RemoveBlocklistEntry(ctx context.Context, req *plansv1.RemoveBlocklistEntryRequest) (*plansv1.RemoveBlocklistEntryResponse, error) {
	return &plansv1.RemoveBlocklistEntryResponse{}, h.lists.Remove(ctx, req)
}

// AdminHandler serves the platform blocklist methods of AdminService.
type AdminHandler struct{ platform *Platform }

// NewAdminHandler returns the operator's blocklist handlers.
func NewAdminHandler(p *Platform) *AdminHandler { return &AdminHandler{platform: p} }

func (h *AdminHandler) ListPlatformBlocklist(ctx context.Context, _ *plansv1.ListPlatformBlocklistRequest) (*plansv1.ListPlatformBlocklistResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	return h.platform.List(ctx)
}

func (h *AdminHandler) AddPlatformBlocklistEntry(ctx context.Context, req *plansv1.AddPlatformBlocklistEntryRequest) (*plansv1.AddPlatformBlocklistEntryResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	e, err := h.platform.Add(ctx, req)
	if err != nil {
		return nil, err
	}
	return &plansv1.AddPlatformBlocklistEntryResponse{Entry: e}, nil
}

func (h *AdminHandler) RemovePlatformBlocklistEntry(ctx context.Context, req *plansv1.RemovePlatformBlocklistEntryRequest) (*plansv1.RemovePlatformBlocklistEntryResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	return &plansv1.RemovePlatformBlocklistEntryResponse{}, h.platform.Remove(ctx, req)
}
