//go:build integration

package itest

import (
	"context"
	"errors"
	"net"
	"strings"
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
	directorycmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/command"
	directoryqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/directory/query"
)

// directoryClient serves DirectoryService over the env's system pool. ready is the inventory check behind the refusal to place a tenant in a cell (nil: the real
// inventory of pkg/tenantdir, which today refuses).
func directoryClient(t *testing.T, e *env, ready func() error) plansv1.DirectoryServiceClient {
	t.Helper()
	srv, _ := grpcx.NewServer(grpcx.ServerOptions{Logger: testkit.Logger(), Extra: []grpc.ServerOption{grpc.ChainUnaryInterceptor(tenant.UnaryServerInterceptor())}})
	plansv1.RegisterDirectoryServiceServer(srv, directory.New(directoryqry.New(e.system), directorycmd.New(e.system, ready, testkit.Logger())))
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	cc, err := grpcx.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return plansv1.NewDirectoryServiceClient(cc)
}

// testDirectoryMoves: the steps of a move are transitions of the placement, a tenant can go to a cell only when the inventory says every job is converted, and
// nothing but an operator moves one.
func testDirectoryMoves(t *testing.T) {
	e := newEnv(t)
	ctx := operator()
	live := e.account(t, "Directory Moves")[plansv1.Environment_ENVIRONMENT_LIVE].GetId()
	refusing := directoryClient(t, e, nil) // the real inventory
	allowing := directoryClient(t, e, func() error { return nil })
	notReady := directoryClient(t, e, func() error { return errors.New("2 of 3 jobs are not converted") })
	want := func(t *testing.T, err error, c codes.Code, reason string) {
		t.Helper()
		if status.Code(err) != c || !strings.HasPrefix(status.Convert(err).Message(), reason+": ") {
			t.Fatalf("err = %v, want %v and a message starting %q", err, c, reason)
		}
	}
	state := func(t *testing.T) *plansv1.Placement {
		t.Helper()
		r, err := allowing.GetPlacement(ctx, &plansv1.GetPlacementRequest{TenantId: live})
		if err != nil {
			t.Fatal(err)
		}
		return r.GetPlacement()
	}
	t.Cleanup(func() {
		_, _ = e.system.Exec(context.Background(), `UPDATE tenants SET cell = '', region = '', placement_status = 'ready' WHERE id = $1`, live)
	})

	// The platform's real inventory refuses a placement in a cell, because the jobs of most services are not converted yet.
	_, err := refusing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: "c1"})
	want(t, err, codes.FailedPrecondition, "cells_not_ready")
	if p := state(t); p.GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_READY || p.GetCell() != "" {
		t.Fatalf("a refused move changed the placement: %v", p)
	}
	_, err = notReady.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: "c1"})
	want(t, err, codes.FailedPrecondition, "cells_not_ready")

	// Operators only, valid names only, a tenant that exists, and not to where it already is.
	if _, err := allowing.BeginMove(as(live), &plansv1.BeginMoveRequest{TenantId: live, ToCell: "c1"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("a tenant moving itself: %v", err)
	}
	for _, bad := range []string{"C1", "-c1", "c 1"} {
		if _, err := allowing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: bad}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("cell %q: %v", bad, err)
		}
	}
	if _, err := allowing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: uuid.NewString(), ToCell: "c1"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown tenant: %v", err)
	}
	_, err = allowing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: ""})
	want(t, err, codes.FailedPrecondition, "same_place")
	_, err = allowing.FinishMove(ctx, &plansv1.FinishMoveRequest{TenantId: live, Cell: "c1"})
	want(t, err, codes.FailedPrecondition, "not_moving")
	_, err = allowing.AbortMove(ctx, &plansv1.AbortMoveRequest{TenantId: live})
	want(t, err, codes.FailedPrecondition, "not_moving")

	// pooled -> c1: fenced, then finished.
	begun, err := allowing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: "c1"})
	if err != nil || begun.GetPlacement().GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_MOVING || begun.GetPlacement().GetCell() != "" {
		t.Fatalf("begin = %v %v: the tenant is fenced where it still is", begun, err)
	}
	_, err = allowing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: "c2"})
	want(t, err, codes.FailedPrecondition, "move_in_progress")
	// The cell check applies when the move ends too: a platform that stopped being ready (a job added to the inventory) does not finish it.
	_, err = notReady.FinishMove(ctx, &plansv1.FinishMoveRequest{TenantId: live, Cell: "c1", Region: "eu"})
	want(t, err, codes.FailedPrecondition, "cells_not_ready")
	for _, bad := range []*plansv1.FinishMoveRequest{{TenantId: live, Cell: "Bad"}, {TenantId: live, Cell: "c1", Region: "EU"}, {TenantId: live, Cell: "", Region: "eu"}} {
		if _, err := allowing.FinishMove(ctx, bad); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("finish %v: %v", bad, err)
		}
	}
	done, err := allowing.FinishMove(ctx, &plansv1.FinishMoveRequest{TenantId: live, Cell: "c1", Region: "eu"})
	if p := done.GetPlacement(); err != nil || p.GetCell() != "c1" || p.GetRegion() != "eu" || p.GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_READY {
		t.Fatalf("finish = %v %v", done, err)
	}
	_, err = allowing.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: "c1"})
	want(t, err, codes.FailedPrecondition, "same_place")

	// c1 -> pooled: leaving a cell is never refused, and an abort leaves the placement where it was.
	if _, err := notReady.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: ""}); err != nil {
		t.Fatalf("moving out of a cell: %v", err)
	}
	aborted, err := allowing.AbortMove(ctx, &plansv1.AbortMoveRequest{TenantId: live})
	if p := aborted.GetPlacement(); err != nil || p.GetCell() != "c1" || p.GetRegion() != "eu" || p.GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_READY {
		t.Fatalf("abort = %v %v", aborted, err)
	}
	if _, err := notReady.BeginMove(ctx, &plansv1.BeginMoveRequest{TenantId: live, ToCell: ""}); err != nil {
		t.Fatal(err)
	}
	back, err := notReady.FinishMove(ctx, &plansv1.FinishMoveRequest{TenantId: live})
	if p := back.GetPlacement(); err != nil || p.GetCell() != "" || p.GetRegion() != "" || p.GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_READY {
		t.Fatalf("back to pooled = %v %v", back, err)
	}
}

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

	client := directoryClient(t, e, nil)

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
		for _, set := range []string{"cell = 'Cell One'", "cell = '-c1'", "region = 'EU'", "placement_status = 'half'", "placement_status = 'provisioning'"} {
			_, err := e.system.Exec(ctx, `UPDATE tenants SET `+set+` WHERE id = $1`, live)
			if pgxx.ErrCode(err) != pgxx.CodeCheckViolation {
				t.Errorf("%s: %v, want a check violation", set, err)
			}
		}
	})

	t.Run("placed tenants are listed by cell, pooled ones never", func(t *testing.T) {
		for _, s := range []struct {
			id, cell, region, status string
		}{{live, "c1", "eu", "ready"}, {sandbox, "c1", "eu", "moving"}, {other, "c2", "us", "moving"}} {
			if _, err := e.system.Exec(ctx, `UPDATE tenants SET cell = $2, region = $3, placement_status = $4 WHERE id = $1`, s.id, s.cell, s.region, s.status); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			_, _ = e.system.Exec(ctx, `UPDATE tenants SET cell = '', region = '', placement_status = 'ready' WHERE id = ANY($1)`, []string{live, sandbox, other})
		})

		// In cells mode (ITEST_CELLS=1) every tenant of the suite is placed, so the listing holds more than these three: what the test is about is these.
		mine := func(id string) bool { return id == live || id == sandbox || id == other }
		all, err := client.ListPlacements(operator(), &plansv1.ListPlacementsRequest{PageSize: 1000})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]*plansv1.Placement{}
		for _, p := range all.GetPlacements() {
			if mine(p.GetTenantId()) {
				got[p.GetTenantId()] = p
			}
		}
		if len(got) != 3 || got[live].GetCell() != "c1" || got[sandbox].GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_MOVING ||
			got[other].GetRegion() != "us" || got[other].GetStatus() != plansv1.PlacementStatus_PLACEMENT_STATUS_MOVING {
			t.Fatalf("all placements = %v", all.GetPlacements())
		}
		c1, err := client.ListPlacements(operator(), &plansv1.ListPlacementsRequest{Cell: "c1", PageSize: 1000})
		inC1 := 0
		for _, p := range c1.GetPlacements() {
			if mine(p.GetTenantId()) {
				inC1++
			}
		}
		if err != nil || inC1 != 2 {
			t.Fatalf("c1 = %v %v, want 2 of the 3", c1, err)
		}

		var seen []string
		token := ""
		for {
			resp, err := client.ListPlacements(operator(), &plansv1.ListPlacementsRequest{PageSize: 2, PageToken: token})
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range resp.GetPlacements() {
				if mine(p.GetTenantId()) {
					seen = append(seen, p.GetTenantId())
				}
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
		if p, err := dir.Placement(ctx, otherID); err != nil || p.Serving() || p.Status != tenantdir.StatusMoving {
			t.Fatalf("directory other = %+v %v", p, err)
		}
		if _, err := dir.Placement(ctx, uuid.New()); err == nil {
			t.Fatal("an unknown tenant has a placement")
		}
		placed, err := dir.Placed(ctx, "c1")
		inC1 = 0
		for _, p := range placed {
			if mine(p.TenantID.String()) {
				inC1++
			}
		}
		if err != nil || inC1 != 2 {
			t.Fatalf("Placed(c1) = %+v %v, want 2 of the 3", placed, err)
		}
	})
}
