//go:build integration

package itest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/background"
	"github.com/0furkancolak/payrudder/services/assets-svc/migrations"
)

// A tenant in a cell has its rows in its own schema, so the jobs that look at "every asset row" must look there: the orphan reconciler must not take a cell
// tenant's real object for an orphan because the pooled database does not know it, and must leave the objects of a tenant that is moving alone.
func TestJobsInCellsMode(t *testing.T) {
	w := start(t, true)
	ctx := ctxOf(uuid.New(), "standard")
	ti, _ := tenant.Require(ctx)

	data := zipData(1<<20, 9)
	sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
		{Filename: "a.zip", ContentType: "application/zip", Size: int64(len(data)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
	if err != nil {
		t.Fatal(err)
	}
	f := sess.GetFiles()[0]
	etags := uploadParts(t, f, data, []int32{1}, urlMap(f), 1)
	asset, err := w.complete(ctx, completeReq(sess, f, etags))
	if err != nil || asset.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY {
		t.Fatalf("complete = %v, %v", asset, err)
	}
	realKey := fmt.Sprintf("%s/assets/%s", ti.ID, asset.GetId())
	orphan := fmt.Sprintf("%s/assets/%s", ti.ID, uuid.New())
	if err := w.store.Put(context.Background(), orphan, []byte("orphan"), blob.PutOptions{}); err != nil {
		t.Fatal(err)
	}
	later := func() time.Time { return time.Now().Add(72 * time.Hour) }
	jobs := &background.Jobs{DB: w.system, Cells: w.cells, Store: w.store, Now: later}
	exists := func(key string) bool {
		_, err := w.store.Head(context.Background(), key)
		return err == nil
	}

	t.Run("the pooled database has no row of the tenant", func(t *testing.T) {
		var n int
		if err := w.system.QueryRow(context.Background(), `SELECT count(*) FROM assets WHERE tenant_id = $1`, ti.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("pooled rows of the cell tenant = %d, %v", n, err)
		}
	})

	t.Run("a tenant that is moving keeps its objects, orphans included, until it serves again", func(t *testing.T) {
		w.dir.setMoving(ti.ID, true)
		defer w.dir.setMoving(ti.ID, false)
		if n, err := jobs.ReconcileOrphans(context.Background()); err != nil || n != 0 {
			t.Fatalf("deleted %d, %v while the tenant was moving", n, err)
		}
		if !exists(orphan) || !exists(realKey) {
			t.Fatal("an object of a moving tenant was deleted")
		}
	})

	t.Run("the cell tenant's real object is kept and its orphan is deleted", func(t *testing.T) {
		n, err := jobs.ReconcileOrphans(context.Background())
		if err != nil || n != 1 {
			t.Fatalf("deleted %d, %v, want exactly the orphan", n, err)
		}
		if exists(orphan) {
			t.Fatal("the orphan survived")
		}
		if !exists(realKey) {
			t.Fatal("the real object of a tenant in a cell was taken for an orphan")
		}
	})

	t.Run("the verifier and the sweeper work in the tenant's schema and skip it while it moves", func(t *testing.T) {
		pdf := append([]byte("%PDF-1.7\n"), make([]byte, 2000)...)
		spoof, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "fake.zip", ContentType: "application/zip", Size: int64(len(pdf)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if err != nil {
			t.Fatal(err)
		}
		sf := spoof.GetFiles()[0]
		if _, err := w.complete(ctx, completeReq(spoof, sf, uploadParts(t, sf, pdf, []int32{1}, urlMap(sf), 1))); err != nil {
			t.Fatal(err)
		}
		slow, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "slow.zip", ContentType: "application/zip", Size: 1 << 20, Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if err != nil || len(slow.GetFiles()) != 1 {
			t.Fatalf("slow session = %v, %v", slow, err)
		}

		w.dir.setMoving(ti.ID, true)
		if n, err := jobs.VerifyUploads(context.Background(), 50); err != nil || n != 0 {
			t.Fatalf("verified %d, %v while moving", n, err)
		}
		if n, err := jobs.SweepExpired(context.Background(), 50); err != nil || n != 0 {
			t.Fatalf("swept %d, %v while moving", n, err)
		}
		w.dir.setMoving(ti.ID, false)

		if n, err := jobs.VerifyUploads(context.Background(), 50); err != nil || n != 2 {
			t.Fatalf("verified %d, %v; want the first upload (ok) and the spoofed one (rejected)", n, err)
		}
		got, err := w.getAsset(ctx, &assetsv1.GetAssetRequest{AssetId: asset.GetId()})
		if err != nil || got.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY {
			t.Fatalf("the honest upload after verification = %v, %v", got, err)
		}
		if n, err := jobs.SweepExpired(context.Background(), 50); err != nil || n != 1 {
			t.Fatalf("swept %d, %v; want the session nobody finished", n, err)
		}
	})
}

// The tenant migrations build exactly the schema the public migrations build.
func TestTenantSchemaMatchesPublic(t *testing.T) {
	ctx := testkit.Ctx(t)
	pg := testkit.StartPostgres(t)
	pub := testkit.MigratedDB(t, pg, "assets", migrations.FS)
	cell := testkit.CreateCellDB(t, pg, "assets", "c1")
	id := uuid.New()
	ts := pgxx.TenantSchemas{Migrations: migrations.Tenant(), Roles: []string{"assets_app", "assets_system"}}
	if _, err := pgxx.ProvisionTenant(ctx, cell.Migrator, ts, id, nil); err != nil {
		t.Fatal(err)
	}
	pubConn, err := pgx.Connect(ctx, pub.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pubConn.Close(ctx) }()
	cellConn, err := pgx.Connect(ctx, cell.Migrator)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cellConn.Close(ctx) }()
	want, err := pgxx.SchemaShape(ctx, pubConn, "public")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pgxx.SchemaShape(ctx, cellConn, tenantdir.Schema(id))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) < 20 {
		t.Fatalf("the public shape has only %d lines: the comparison is not looking at the schema", len(want))
	}
	if diff := pgxx.Diff(want, got); len(diff) > 0 {
		for _, l := range diff {
			t.Error(l)
		}
		t.Fatal("the tenant migrations and the public migrations of assets-svc have drifted apart (- public only, + tenant only)")
	}
}
