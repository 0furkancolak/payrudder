//go:build integration

package itest

import (
	"context"
	"net"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/grpcx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/directory"
	directoryqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/query"
)

// testDirectory: phase 28 stage 1. Every tenant is pooled until an operator places it, the directory answers operators only, and pkg/tenantdir reads it
// through DirectoryService the way the other services do.
func testDirectory(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.account(t, "Directory A")
	b := e.account(t, "Directory B")
	live := a[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	sandbox := a[plansv1.Environment_ENVIRONMENT_SANDBOX].GetId()
	other := b[plansv1.Environment_ENVIRONMENT_LIVE].GetId()

	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: testkit.Logger(), Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor())}})
	plansv1.RegisterDirectoryServiceServer(srv, directory.New(directoryqry.New(e.system)))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	client := plansv1.NewDirectoryServiceClient(cc)

	t.Run("a new tenant is pooled and ready", func(t *testing.T) {
		resp, err := client.GetPlacement(operator(), &plansv1.GetPlacementRequest{TenantId: live})
		if err != nil {
			t.Fatal(err)
		}
		p := resp.GetPlacement()
		if p.GetCell() != "" || p.GetRegion() != "" || p.GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_READY {
			t.Fatalf("placement = %v, want pooled and ready", p)
		}
	})

	t.Run("operators only, and only a tenant that exists", func(t *testing.T) {
		if _, err := client.GetPlacement(as(live), &plansv1.GetPlacementRequest{TenantId: live}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("a tenant asking: %v", err)
		}
		if _, err := client.ListPlacements(as(live), &plansv1.ListPlacementsRequest{}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("a tenant listing: %v", err)
		}
		if _, err := client.GetPlacement(operator(), &plansv1.GetPlacementRequest{TenantId: uuid.NewString()}); status.Code(err) != codes.NotFound {
			t.Fatalf("unknown tenant: %v", err)
		}
		if _, err := client.GetPlacement(operator(), &plansv1.GetPlacementRequest{TenantId: "nope"}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("malformed id: %v", err)
		}
	})

	t.Run("the columns refuse a malformed cell, region or status", func(t *testing.T) {
		for _, set := range []string{"cell = 'Cell One'", "cell = '-c1'", "region = 'EU'", "placement_status = 'half'"} {
			_, err := e.system.Exec(ctx, `UPDATE tenants SET `+set+` WHERE id = $1`, live)
			if pgxx.ErrCode(err) != pgxx.CodeCheckViolation {
				t.Errorf("%s: %v, want a check violation", set, err)
			}
		}
	})

	t.Run("placed tenants are listed by cell, pooled ones never", func(t *testing.T) {
		for _, s := range []struct {
			id, cell, region, status string
		}{{live, "c1", "eu", "ready"}, {sandbox, "c1", "eu", "moving"}, {other, "c2", "us", "provisioning"}} {
			if _, err := e.system.Exec(ctx, `UPDATE tenants SET cell = $2, region = $3, placement_status = $4 WHERE id = $1`, s.id, s.cell, s.region, s.status); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			_, _ = e.system.Exec(ctx, `UPDATE tenants SET cell = '', region = '', placement_status = 'ready' WHERE id = ANY($1)`, []string{live, sandbox, other})
		})

		all, err := client.ListPlacements(operator(), &plansv1.ListPlacementsRequest{})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]*plansv1.Placement{}
		for _, p := range all.GetPlacements() {
			got[p.GetTenantId()] = p
		}
		if len(got) != 3 || got[live].GetCell() != "c1" || got[sandbox].GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_MOVING ||
			got[other].GetRegion() != "us" || got[other].GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_PROVISIONING {
			t.Fatalf("all placements = %v", all.GetPlacements())
		}
		c1, err := client.ListPlacements(operator(), &plansv1.ListPlacementsRequest{Cell: "c1"})
		if err != nil || len(c1.GetPlacements()) != 2 {
			t.Fatalf("c1 = %v %v, want 2", c1, err)
		}

		var seen []string
		token := ""
		for {
			resp, err := client.ListPlacements(operator(), &plansv1.ListPlacementsRequest{PageSize: 2, PageToken: token})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range resp.GetPlacements() {
				seen = append(seen, p.GetTenantId())
			}
			if token = resp.GetNextPageToken(); token == "" {
				break
			}
		}
		if len(seen) != 3 {
			t.Fatalf("paged through %v, want the 3 placed tenants once each", seen)
		}

		// pkg/tenantdir over the same service.
		dir := tenantdir.NewGRPC(client, tenantdir.Options{})
		liveID, otherID := uuid.MustParse(live), uuid.MustParse(other)
		if p, err := dir.Placement(ctx, liveID); err != nil || p.Cell != "c1" || !p.Serving() || p.Pooled() {
			t.Fatalf("directory live = %+v %v", p, err)
		}
		if p, err := dir.Placement(ctx, otherID); err != nil || p.Serving() || p.Status != tenantdir.StatusProvisioning {
			t.Fatalf("directory other = %+v %v", p, err)
		}
		if _, err := dir.Placement(ctx, uuid.New()); err == nil {
			t.Fatal("an unknown tenant has a placement")
		}
		placed, err := dir.Placed(ctx, "c1")
		if err != nil || len(placed) != 2 {
			t.Fatalf("Placed(c1) = %+v %v", placed, err)
		}
	})
}
