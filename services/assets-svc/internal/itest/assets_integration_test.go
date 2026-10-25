//go:build integration

// Package itest runs assets-svc against real Postgres and MinIO.
package itest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	assetsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/assets/v1"
	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/pkg/testkit"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/background"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/service"
	"github.com/0furkancolak/payrudder/services/assets-svc/migrations"
)

type world struct {
	store  *blob.Store
	app    *pgxpool.Pool
	system *pgxpool.Pool
	svc    *service.Service
	quota  map[string]int64
}

func start(t *testing.T) *world {
	t.Helper()
	pg := testkit.StartPostgres(t)
	db := testkit.CreateServiceDB(t, pg, "assets")
	if err := pgxx.Migrate(context.Background(), db.Migrator, migrations.FS, nil); err != nil {
		t.Fatal(err)
	}
	w := &world{}
	var err error
	if w.app, err = pgxx.NewPool(context.Background(), db.App); err != nil {
		t.Fatal(err)
	}
	if w.system, err = pgxx.NewPool(context.Background(), db.System); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.app.Close)
	t.Cleanup(w.system.Close)
	m := testkit.StartMinio(t)
	if w.store, err = blob.New(blob.Config{Endpoint: m.Endpoint, Bucket: "assets", AccessKey: m.AccessKey, SecretKey: m.SecretKey, ForcePathStyle: true}); err != nil {
		t.Fatal(err)
	}
	if err := w.store.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	w.quota = map[string]int64{"free": 1 * policy.MiB, "standard": 10 * policy.GiB}
	w.svc = service.New(w.app, w.store, policy.New(policy.DefaultGeneralTypes), service.Config{Quotas: w.quota})
	return w
}

func ctxOf(id uuid.UUID, tier string) context.Context {
	return tenant.NewContext(context.Background(), tenant.Info{ID: id, Tier: tier})
}

// zipData returns n bytes that start with the ZIP magic (so content sniffing agrees with application/zip) followed by
// reproducible random data.
func zipData(n int, seed uint64) []byte {
	b := make([]byte, n)
	rng := rand.New(rand.NewPCG(seed, seed+1))
	for i := range b {
		b[i] = byte(rng.UintN(256))
	}
	copy(b, "PK\x03\x04")
	return b
}

func hexSHA(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// put uploads one part to its presigned URL and returns the ETag S3 answered.
func put(t *testing.T, url string, body []byte) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("PUT part: %d", resp.StatusCode)
	}
	return resp.Header.Get("ETag")
}

// uploadParts uploads the listed part numbers of data with the given parallelism and returns their ETags.
func uploadParts(t *testing.T, f *assetsv1.UploadFile, data []byte, parts []int32, urls map[int32]string, parallel int) map[int32]string {
	t.Helper()
	etags := map[int32]string{}
	var mu sync.Mutex
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, n := range parts {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			lo := int64(n-1) * f.GetPartSize()
			hi := min(lo+f.GetPartSize(), int64(len(data)))
			etag := put(t, urls[n], data[lo:hi])
			mu.Lock()
			etags[n] = etag
			mu.Unlock()
		}()
	}
	wg.Wait()
	return etags
}

func urlMap(f *assetsv1.UploadFile) map[int32]string {
	m := map[int32]string{}
	for _, p := range f.GetPresignedParts() {
		m[p.GetPartNumber()] = p.GetUrl()
	}
	return m
}

func numbers(from, to int32) []int32 {
	var out []int32
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func reason(err error) string {
	for _, d := range status.Convert(err).Details() {
		if ei, ok := d.(*errdetails.ErrorInfo); ok {
			return ei.GetReason()
		}
	}
	return ""
}

func completeReq(sess *assetsv1.UploadSession, f *assetsv1.UploadFile, etags map[int32]string) *assetsv1.CompleteFileRequest {
	req := &assetsv1.CompleteFileRequest{SessionId: sess.GetId(), AssetId: f.GetAssetId()}
	for n := int32(1); n <= f.GetPartCount(); n++ {
		req.Parts = append(req.Parts, &assetsv1.PartETag{PartNumber: n, Etag: etags[n]})
	}
	return req
}

func TestDirectUploads(t *testing.T) {
	w := start(t)
	ctx := ctxOf(uuid.New(), "standard")

	t.Run("ThreeFilesWithParallelParts", func(t *testing.T) {
		sizes := []int{200 << 20, 20 << 20, 1 << 10}
		datas := make([][]byte, len(sizes))
		var specs []*assetsv1.FileSpec
		for i, n := range sizes {
			datas[i] = zipData(n, uint64(i+1))
			specs = append(specs, &assetsv1.FileSpec{Filename: fmt.Sprintf("file%d.zip", i), ContentType: "application/zip", Size: int64(n),
				Purpose: assetsv1.Purpose_PURPOSE_GENERAL, Sha256: hexSHA(datas[i])})
		}
		sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: specs, IdempotencyKey: "three"})
		if err != nil {
			t.Fatal(err)
		}
		if len(sess.GetFiles()) != 3 {
			t.Fatalf("session = %v", sess)
		}
		// Files may come back in any order; match them by size.
		bySize := map[int64]*assetsv1.UploadFile{}
		for _, f := range sess.GetFiles() {
			bySize[f.GetSize()] = f
		}
		for i, n := range sizes {
			f := bySize[int64(n)]
			wantParts := (int64(n) + 8<<20 - 1) / (8 << 20)
			if f.GetPartSize() != 8<<20 || int64(f.GetPartCount()) != wantParts || int64(len(f.GetPresignedParts())) != min(wantParts, 100) {
				t.Fatalf("file %d: part size %d count %d presigned %d", i, f.GetPartSize(), f.GetPartCount(), len(f.GetPresignedParts()))
			}
			etags := uploadParts(t, f, datas[i], numbers(1, f.GetPartCount()), urlMap(f), 8)
			asset, err := w.complete(ctx, completeReq(sess, f, etags))
			if err != nil {
				t.Fatalf("complete %d: %v", i, err)
			}
			if asset.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY || asset.GetSizeBytes() != int64(n) {
				t.Fatalf("file %d = %v", i, asset)
			}
			// Completing twice returns the same asset.
			again, err := w.complete(ctx, completeReq(sess, f, etags))
			if err != nil || again.GetId() != asset.GetId() || again.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY {
				t.Fatalf("second complete = %v, %v", again, err)
			}
			// The stored object has the bytes that were sent.
			ti, _ := tenant.Require(ctx)
			key := fmt.Sprintf("%s/assets/%s", ti.ID, asset.GetId())
			rc, err := w.store.Open(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.New()
			nCopied, _ := io.Copy(h, rc)
			rc.Close()
			if nCopied != int64(n) || hex.EncodeToString(h.Sum(nil)) != hexSHA(datas[i]) {
				t.Fatalf("object %d: %d bytes, sha %x", i, nCopied, h.Sum(nil))
			}
		}
		// The session finished, and the same idempotency key answers with it.
		again, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: specs, IdempotencyKey: "three"})
		if err != nil || again.GetId() != sess.GetId() || again.GetStatus() != "COMPLETED" {
			t.Fatalf("idempotent create = %v, %v", again, err)
		}
		// The verifier agrees with the declared types and hashes.
		jobs := &background.Jobs{DB: w.system, Store: w.store}
		if n, err := jobs.VerifyUploads(context.Background(), 10); err != nil || n != 3 {
			t.Fatalf("verified %d, %v", n, err)
		}
		list, err := w.svc.ListAssets(ctx, &assetsv1.ListAssetsRequest{})
		if err != nil || len(list.GetAssets()) != 3 {
			t.Fatalf("list = %v, %v", list, err)
		}
	})

	t.Run("InterruptedUploadResumes", func(t *testing.T) {
		data := zipData(40<<20, 9) // 5 parts
		sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "big.zip", ContentType: "application/zip", Size: int64(len(data)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if err != nil {
			t.Fatal(err)
		}
		f := sess.GetFiles()[0]
		if f.GetPartCount() != 5 {
			t.Fatalf("part count = %d", f.GetPartCount())
		}
		first := uploadParts(t, f, data, []int32{1, 2, 3}, urlMap(f), 3) // the connection dies here
		got, err := w.getFile(ctx, &assetsv1.GetUploadFileRequest{SessionId: sess.GetId(), AssetId: f.GetAssetId()})
		if err != nil || len(got.GetUploadedParts()) != 3 || got.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_UPLOADING {
			t.Fatalf("resume state = %v, %v", got, err)
		}
		have := map[int32]string{}
		for _, p := range got.GetUploadedParts() {
			have[p.GetPartNumber()] = p.GetEtag()
			if p.GetEtag() != first[p.GetPartNumber()][1:len(first[p.GetPartNumber()])-1] && p.GetEtag() != first[p.GetPartNumber()] {
				t.Errorf("part %d etag %s vs %s", p.GetPartNumber(), p.GetEtag(), first[p.GetPartNumber()])
			}
		}
		// Only the missing parts are sent; their URLs come from PresignParts.
		pp, err := w.svc.PresignParts(ctx, &assetsv1.PresignPartsRequest{SessionId: sess.GetId(), AssetId: f.GetAssetId(), FromPart: 4, Count: 10})
		if err != nil || len(pp.GetParts()) != 2 {
			t.Fatalf("presign parts = %v, %v", pp, err)
		}
		urls := map[int32]string{}
		for _, p := range pp.GetParts() {
			urls[p.GetPartNumber()] = p.GetUrl()
		}
		rest := uploadParts(t, f, data, []int32{4, 5}, urls, 2)
		all := map[int32]string{}
		for n, e := range have {
			all[n] = e
		}
		for n, e := range rest {
			all[n] = e
		}
		asset, err := w.complete(ctx, completeReq(sess, f, all))
		if err != nil || asset.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY || asset.GetSizeBytes() != int64(len(data)) {
			t.Fatalf("complete after resume = %v, %v", asset, err)
		}
	})

	t.Run("WrongOrMissingETagRejectsAndLeavesNoObject", func(t *testing.T) {
		data := zipData(20<<20, 5) // 3 parts
		for name, mutate := range map[string]func(map[int32]string) map[int32]string{
			"wrong etag":   func(e map[int32]string) map[int32]string { e[2] = "\"deadbeef\""; return e },
			"missing part": func(e map[int32]string) map[int32]string { delete(e, 3); return e },
		} {
			sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
				{Filename: "x.zip", ContentType: "application/zip", Size: int64(len(data)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
			if err != nil {
				t.Fatal(err)
			}
			f := sess.GetFiles()[0]
			etags := mutate(uploadParts(t, f, data, numbers(1, f.GetPartCount()), urlMap(f), 3))
			asset, err := w.complete(ctx, completeReq(sess, f, etags))
			if err != nil || asset.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_REJECTED || asset.GetRejectReason() != policy.PartMismatch {
				t.Fatalf("%s: %v, %v; want REJECTED/PART_MISMATCH", name, asset, err)
			}
			ti, _ := tenant.Require(ctx)
			if _, err := w.store.Head(context.Background(), fmt.Sprintf("%s/assets/%s", ti.ID, asset.GetId())); !errors.Is(err, blob.ErrNotFound) {
				t.Fatalf("%s: an object was left behind (%v)", name, err)
			}
			ups, _ := w.store.ListMultipartUploads(context.Background(), fmt.Sprintf("%s/assets/%s", ti.ID, asset.GetId()))
			if len(ups) != 0 {
				t.Fatalf("%s: multipart upload still open: %v", name, ups)
			}
			again, err := w.complete(ctx, completeReq(sess, f, etags))
			if err != nil || again.GetId() != asset.GetId() || again.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_REJECTED {
				t.Fatalf("%s: second complete = %v, %v", name, again, err)
			}
		}
	})

	t.Run("ExpiredSessionIsSweptAndItsUploadAborted", func(t *testing.T) {
		data := zipData(10<<20, 3)
		sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "slow.zip", ContentType: "application/zip", Size: int64(len(data)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if err != nil {
			t.Fatal(err)
		}
		f := sess.GetFiles()[0]
		uploadParts(t, f, data, []int32{1}, urlMap(f), 1)
		ti, _ := tenant.Require(ctx)
		prefix := fmt.Sprintf("%s/assets/%s", ti.ID, f.GetAssetId())
		if ups, _ := w.store.ListMultipartUploads(context.Background(), prefix); len(ups) != 1 {
			t.Fatalf("expected one open multipart upload, got %v", ups)
		}
		jobs := &background.Jobs{DB: w.system, Store: w.store, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
		n, err := jobs.SweepExpired(context.Background(), 10)
		if err != nil || n < 1 {
			t.Fatalf("swept %d, %v", n, err)
		}
		if ups, _ := w.store.ListMultipartUploads(context.Background(), prefix); len(ups) != 0 {
			t.Fatalf("the multipart upload survived the sweep: %v", ups)
		}
		got, err := w.getFile(ctx, &assetsv1.GetUploadFileRequest{SessionId: sess.GetId(), AssetId: f.GetAssetId()})
		if err != nil || got.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_ABORTED {
			t.Fatalf("file after sweep = %v, %v", got, err)
		}
		if _, err := w.complete(ctx, &assetsv1.CompleteFileRequest{SessionId: sess.GetId(), AssetId: f.GetAssetId()}); status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("completing an aborted file: %v", err)
		}
	})

	t.Run("AnotherTenantSeesNothing", func(t *testing.T) {
		data := zipData(1<<10, 2)
		sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "mine.zip", ContentType: "application/zip", Size: int64(len(data)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if err != nil {
			t.Fatal(err)
		}
		f := sess.GetFiles()[0]
		etags := uploadParts(t, f, data, []int32{1}, urlMap(f), 1)
		asset, err := w.complete(ctx, completeReq(sess, f, etags))
		if err != nil {
			t.Fatal(err)
		}
		other := ctxOf(uuid.New(), "standard")
		for name, call := range map[string]func() error{
			"complete": func() error { _, err := w.complete(other, completeReq(sess, f, etags)); return err },
			"presign": func() error {
				_, err := w.svc.PresignParts(other, &assetsv1.PresignPartsRequest{SessionId: sess.GetId(), AssetId: f.GetAssetId(), FromPart: 1, Count: 1})
				return err
			},
			"get": func() error {
				_, err := w.getAsset(other, &assetsv1.GetAssetRequest{AssetId: asset.GetId()})
				return err
			},
			"download": func() error {
				_, err := w.svc.GetDownloadUrl(other, &assetsv1.GetDownloadUrlRequest{AssetId: asset.GetId()})
				return err
			},
			"delete": func() error {
				_, err := w.deleteAsset(other, &assetsv1.DeleteAssetRequest{AssetId: asset.GetId()})
				return err
			},
			"abort": func() error {
				_, err := w.abort(other, &assetsv1.AbortUploadRequest{SessionId: sess.GetId()})
				return err
			},
		} {
			if err := call(); status.Code(err) != codes.NotFound {
				t.Errorf("%s as another tenant: %v, want NOT_FOUND", name, err)
			}
		}
		// The owner downloads through a presigned attachment link.
		dl, err := w.svc.GetDownloadUrl(ctx, &assetsv1.GetDownloadUrlRequest{AssetId: asset.GetId()})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Get(dl.GetUrl())
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Equal(body, data) || resp.Header.Get("Content-Disposition") == "" {
			t.Fatalf("download: %d, %d bytes, disposition %q", resp.StatusCode, len(body), resp.Header.Get("Content-Disposition"))
		}
	})

	t.Run("QuotaIsEnforced", func(t *testing.T) {
		free := ctxOf(uuid.New(), "free") // 1 MiB
		_, err := w.create(free, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "huge.zip", ContentType: "application/zip", Size: 5 << 20, Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if status.Code(err) != codes.FailedPrecondition || reason(err) != policy.StorageQuotaExceeded {
			t.Fatalf("over-quota session: %v (%s)", err, reason(err))
		}
		// Two files that each fit but not together: the second completion is refused.
		a, b := zipData(700<<10, 1), zipData(700<<10, 2)
		var sessions []*assetsv1.UploadSession
		for _, d := range [][]byte{a, b} {
			s, err := w.create(free, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
				{Filename: "p.zip", ContentType: "application/zip", Size: int64(len(d)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
			if err != nil {
				t.Fatal(err)
			}
			sessions = append(sessions, s)
		}
		var results []*assetsv1.Asset
		for i, d := range [][]byte{a, b} {
			f := sessions[i].GetFiles()[0]
			etags := uploadParts(t, f, d, []int32{1}, urlMap(f), 1)
			r, err := w.complete(free, completeReq(sessions[i], f, etags))
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, r)
		}
		if results[0].GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY || results[1].GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_REJECTED ||
			results[1].GetRejectReason() != policy.StorageQuotaExceeded {
			t.Fatalf("results = %v %v; want READY then REJECTED/STORAGE_QUOTA_EXCEEDED", results[0], results[1])
		}
		// Deleting the first frees its bytes.
		if _, err := w.deleteAsset(free, &assetsv1.DeleteAssetRequest{AssetId: results[0].GetId()}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.create(free, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "again.zip", ContentType: "application/zip", Size: 900 << 10, Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}}); err != nil {
			t.Fatalf("after the delete the quota is free again: %v", err)
		}
	})

	t.Run("InvalidFilesAreRefusedAtCreation", func(t *testing.T) {
		for name, f := range map[string]*assetsv1.FileSpec{
			"svg logo":     {Filename: "l.svg", ContentType: "image/svg+xml", Size: 10, Purpose: assetsv1.Purpose_PURPOSE_INVOICE_LOGO},
			"path in name": {Filename: "../etc/passwd", ContentType: "application/zip", Size: 10, Purpose: assetsv1.Purpose_PURPOSE_GENERAL},
			"no size":      {Filename: "a.zip", ContentType: "application/zip", Purpose: assetsv1.Purpose_PURPOSE_GENERAL},
		} {
			if _, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{f}}); status.Code(err) != codes.InvalidArgument {
				t.Errorf("%s: %v", name, err)
			}
		}
		many := make([]*assetsv1.FileSpec, 51)
		for i := range many {
			many[i] = &assetsv1.FileSpec{Filename: "a.zip", ContentType: "application/zip", Size: 1, Purpose: assetsv1.Purpose_PURPOSE_GENERAL}
		}
		if _, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: many}); status.Code(err) != codes.InvalidArgument {
			t.Errorf("51 files: %v", err)
		}
	})

	t.Run("OrphanedObjectsAreDeleted", func(t *testing.T) {
		ti, _ := tenant.Require(ctx)
		orphan := fmt.Sprintf("%s/assets/%s", ti.ID, uuid.New())
		if err := w.store.Put(context.Background(), orphan, []byte("orphan"), blob.PutOptions{}); err != nil {
			t.Fatal(err)
		}
		jobs := &background.Jobs{DB: w.system, Store: w.store, Now: func() time.Time { return time.Now().Add(72 * time.Hour) }}
		n, err := jobs.ReconcileOrphans(context.Background())
		if err != nil || n < 1 {
			t.Fatalf("deleted %d, %v", n, err)
		}
		if _, err := w.store.Head(context.Background(), orphan); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal("the orphan survived")
		}
		// Objects of real assets are untouched.
		list, _ := w.svc.ListAssets(ctx, &assetsv1.ListAssetsRequest{})
		for _, a := range list.GetAssets() {
			if _, err := w.store.Head(context.Background(), fmt.Sprintf("%s/assets/%s", ti.ID, a.GetId())); err != nil {
				t.Fatalf("a READY asset lost its object: %v", err)
			}
		}
	})

	t.Run("VerifierCatchesASpoofedDirectUpload", func(t *testing.T) {
		pdfBytes := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("x"), 2000)...)
		sess, err := w.create(ctx, &assetsv1.CreateUploadSessionRequest{Files: []*assetsv1.FileSpec{
			{Filename: "fake.zip", ContentType: "application/zip", Size: int64(len(pdfBytes)), Purpose: assetsv1.Purpose_PURPOSE_GENERAL}}})
		if err != nil {
			t.Fatal(err)
		}
		f := sess.GetFiles()[0]
		etags := uploadParts(t, f, pdfBytes, []int32{1}, urlMap(f), 1)
		asset, err := w.complete(ctx, completeReq(sess, f, etags))
		if err != nil || asset.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY {
			t.Fatalf("complete = %v, %v", asset, err)
		}
		jobs := &background.Jobs{DB: w.system, Store: w.store}
		if _, err := jobs.VerifyUploads(context.Background(), 50); err != nil {
			t.Fatal(err)
		}
		got, err := w.getAsset(ctx, &assetsv1.GetAssetRequest{AssetId: asset.GetId()})
		if err != nil || got.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_REJECTED || got.GetRejectReason() != policy.ContentTypeMismatch {
			t.Fatalf("after verification = %v, %v", got, err)
		}
	})
}

// fakeStream drives UploadAsset like the gateway does.
type fakeStream struct {
	grpc.ServerStream
	ctx    context.Context
	chunks []*assetsv1.UploadAssetRequest
	failAt int // Recv number (1-based) that fails with the context error; 0 never
	n      int
	reply  *assetsv1.Asset
	cancel context.CancelFunc
}

func (f *fakeStream) Context() context.Context { return f.ctx }
func (f *fakeStream) Recv() (*assetsv1.UploadAssetRequest, error) {
	f.n++
	if f.failAt > 0 && f.n >= f.failAt {
		f.cancel()
		return nil, context.Canceled
	}
	if len(f.chunks) == 0 {
		return nil, io.EOF
	}
	c := f.chunks[0]
	f.chunks = f.chunks[1:]
	return c, nil
}
func (f *fakeStream) SendAndClose(r *assetsv1.UploadAssetResponse) error {
	f.reply = r.GetAsset()
	return nil
}
func (f *fakeStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeStream) SendHeader(metadata.MD) error { return nil }
func (f *fakeStream) SetTrailer(metadata.MD)       {}
func (f *fakeStream) SendMsg(any) error            { return nil }
func (f *fakeStream) RecvMsg(any) error            { return nil }

func stream(ctx context.Context, name, ct string, purpose assetsv1.Purpose, data []byte, failAt int) (*fakeStream, context.CancelFunc) {
	sctx, cancel := context.WithCancel(ctx)
	fs := &fakeStream{ctx: sctx, failAt: failAt, cancel: cancel}
	fs.chunks = append(fs.chunks, &assetsv1.UploadAssetRequest{Msg: &assetsv1.UploadAssetRequest_Header{Header: &assetsv1.UploadHeader{Filename: name, ContentType: ct, Purpose: purpose}}})
	for off := 0; off < len(data); off += 256 << 10 {
		fs.chunks = append(fs.chunks, &assetsv1.UploadAssetRequest{Msg: &assetsv1.UploadAssetRequest_Chunk{Chunk: data[off:min(off+256<<10, len(data))]}})
	}
	return fs, cancel
}

func TestProxiedUploads(t *testing.T) {
	w := start(t)
	ctx := ctxOf(uuid.New(), "standard")
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 100<<10)...)

	t.Run("ValidFileBecomesReadyWithItsHash", func(t *testing.T) {
		fs, cancel := stream(ctx, "logo.png", "image/png", assetsv1.Purpose_PURPOSE_INVOICE_LOGO, png, 0)
		defer cancel()
		if err := w.svc.UploadAsset(fs); err != nil {
			t.Fatal(err)
		}
		a := fs.reply
		if a.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_READY || a.GetSizeBytes() != int64(len(png)) || a.GetSha256() != hexSHA(png) {
			t.Fatalf("asset = %v", a)
		}
		ti, _ := tenant.Require(ctx)
		if info, err := w.store.Head(context.Background(), fmt.Sprintf("%s/assets/%s", ti.ID, a.GetId())); err != nil || info.Size != int64(len(png)) || info.ContentType != "image/png" {
			t.Fatalf("object = %+v, %v", info, err)
		}
	})

	t.Run("SpoofedContentTypeIsRejectedBeforeAnythingIsStored", func(t *testing.T) {
		pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("y"), 4000)...)
		fs, cancel := stream(ctx, "fake.png", "image/png", assetsv1.Purpose_PURPOSE_INVOICE_LOGO, pdf, 0)
		defer cancel()
		if err := w.svc.UploadAsset(fs); err != nil {
			t.Fatal(err)
		}
		a := fs.reply
		if a.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_REJECTED || a.GetRejectReason() != policy.ContentTypeMismatch {
			t.Fatalf("asset = %v", a)
		}
		ti, _ := tenant.Require(ctx)
		if _, err := w.store.Head(context.Background(), fmt.Sprintf("%s/assets/%s", ti.ID, a.GetId())); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal("a rejected upload left an object")
		}
	})

	t.Run("OversizeStreamIsCutOffAndRejected", func(t *testing.T) {
		big := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{1}, 3<<20)...) // a 2 MiB logo limit
		fs, cancel := stream(ctx, "big.png", "image/png", assetsv1.Purpose_PURPOSE_INVOICE_LOGO, big, 0)
		defer cancel()
		if err := w.svc.UploadAsset(fs); err != nil {
			t.Fatal(err)
		}
		if fs.reply.GetStatus() != assetsv1.AssetStatus_ASSET_STATUS_REJECTED || fs.reply.GetRejectReason() != policy.FileTooLarge {
			t.Fatalf("asset = %v", fs.reply)
		}
		ti, _ := tenant.Require(ctx)
		if ups, _ := w.store.ListMultipartUploads(context.Background(), ti.ID.String()+"/"); len(ups) != 0 {
			t.Fatalf("multipart uploads left behind: %v", ups)
		}
	})

	t.Run("ClientDisconnectAbortsTheMultipartUpload", func(t *testing.T) {
		big := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{2}, 20<<20)...)
		fs, cancel := stream(ctx, "doc.pdf", "application/pdf", assetsv1.Purpose_PURPOSE_INVOICE_ATTACHMENT, big, 40) // dies after ~10 MiB
		defer cancel()
		err := w.svc.UploadAsset(fs)
		if err == nil {
			t.Fatal("a broken stream must fail the call")
		}
		ti, _ := tenant.Require(ctx)
		if ups, _ := w.store.ListMultipartUploads(context.Background(), ti.ID.String()+"/"); len(ups) != 0 {
			t.Fatalf("the S3 multipart upload was not aborted: %v", ups)
		}
		var aborted int
		if err := w.system.QueryRow(context.Background(), `SELECT count(*) FROM assets WHERE tenant_id = $1 AND status = 'ABORTED' AND filename = 'doc.pdf'`, ti.ID).Scan(&aborted); err != nil || aborted != 1 {
			t.Fatalf("aborted assets = %d, %v", aborted, err)
		}
	})
}

// The service returns response wrappers; these helpers give the tests the bare messages.
func (w *world) create(ctx context.Context, req *assetsv1.CreateUploadSessionRequest) (*assetsv1.UploadSession, error) {
	r, err := w.svc.CreateUploadSession(ctx, req)
	return r.GetSession(), err
}

func (w *world) getFile(ctx context.Context, req *assetsv1.GetUploadFileRequest) (*assetsv1.UploadFile, error) {
	r, err := w.svc.GetUploadFile(ctx, req)
	return r.GetFile(), err
}

func (w *world) complete(ctx context.Context, req *assetsv1.CompleteFileRequest) (*assetsv1.Asset, error) {
	r, err := w.svc.CompleteFile(ctx, req)
	return r.GetAsset(), err
}

func (w *world) abort(ctx context.Context, req *assetsv1.AbortUploadRequest) (*assetsv1.UploadSession, error) {
	r, err := w.svc.AbortUpload(ctx, req)
	return r.GetSession(), err
}

func (w *world) getAsset(ctx context.Context, req *assetsv1.GetAssetRequest) (*assetsv1.Asset, error) {
	r, err := w.svc.GetAsset(ctx, req)
	return r.GetAsset(), err
}

func (w *world) deleteAsset(ctx context.Context, req *assetsv1.DeleteAssetRequest) (*assetsv1.Asset, error) {
	r, err := w.svc.DeleteAsset(ctx, req)
	return r.GetAsset(), err
}
