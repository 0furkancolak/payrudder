// Package background runs assets-svc's housekeeping as the system role: the sweeper that aborts expired upload sessions,
// the verifier that checks direct uploads after the fact (content sniffing, declared sha256), and the orphan reconciler
// that deletes objects without a row.
package background

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store/sqlc"
)

// Store is the object storage the jobs use (*blob.Store).
type Store interface {
	AbortMultipart(ctx context.Context, key, uploadID string) error
	GetRange(ctx context.Context, key string, n int64) ([]byte, error)
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	Delete(ctx context.Context, key string) error
	Keys(ctx context.Context, prefix string, fn func(key string, modified time.Time) error) error
}

// Jobs are the three background jobs.
type Jobs struct {
	DB    *pgxpool.Pool // assets_system
	Store Store
	Now   func() time.Time
	Log   *slog.Logger
	// OrphanAge is how old an unreferenced object must be before it is deleted (default 48 h).
	OrphanAge time.Duration

	swept    metric.Int64Counter
	verified metric.Int64Counter
	orphans  metric.Int64Counter
}

func (j *Jobs) init() error {
	if j.Now == nil {
		j.Now = time.Now
	}
	if j.Log == nil {
		j.Log = slog.Default()
	}
	if j.OrphanAge <= 0 {
		j.OrphanAge = 48 * time.Hour
	}
	if j.swept != nil {
		return nil
	}
	m := otel.Meter("github.com/0furkancolak/payrudder/services/assets-svc/background")
	var err error
	if j.swept, err = m.Int64Counter("assets_sessions_swept", metric.WithDescription("Expired upload sessions aborted")); err != nil {
		return err
	}
	if j.verified, err = m.Int64Counter("assets_verified", metric.WithDescription("Direct uploads verified, by result")); err != nil {
		return err
	}
	j.orphans, err = m.Int64Counter("assets_orphans_deleted", metric.WithDescription("Objects without an asset row that were deleted"))
	return err
}

// SweepExpired aborts the S3 multipart uploads of expired OPEN sessions and marks them EXPIRED. It returns how many
// sessions it handled.
func (j *Jobs) SweepExpired(ctx context.Context, limit int) (int, error) {
	if err := j.init(); err != nil {
		return 0, err
	}
	due, err := sqlc.New(j.DB).ListExpiredSessions(ctx, sqlc.ListExpiredSessionsParams{Now: j.Now(), Batch: int32(limit)}) //nolint:gosec // a small batch size
	if err != nil {
		return 0, err
	}
	done := 0
	for _, k := range due {
		swept, err := j.sweepSession(ctx, k)
		if swept {
			done++
		}
		if err != nil {
			return done, err
		}
	}
	j.swept.Add(ctx, int64(done))
	return done, nil
}

// sweepSession expires one session under its row lock; it reports false when another pod took it.
func (j *Jobs) sweepSession(ctx context.Context, k sqlc.ListExpiredSessionsRow) (swept bool, err error) {
	err = pgxx.InTx(ctx, j.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		st, err := q.LockOpenSessionStatus(ctx, sqlc.LockOpenSessionStatusParams(k))
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && st != "OPEN") {
			return nil
		}
		if err != nil {
			return err
		}
		pending, err := q.ListUploadableSessionObjects(ctx, sqlc.ListUploadableSessionObjectsParams{TenantID: k.TenantID, SessionID: k.ID})
		if err != nil {
			return err
		}
		for _, p := range pending {
			if p.S3UploadID != nil {
				if err := j.Store.AbortMultipart(ctx, p.ObjectKey, *p.S3UploadID); err != nil {
					return err
				}
			}
			if err := q.AbortTenantAsset(ctx, sqlc.AbortTenantAssetParams{TenantID: k.TenantID, ID: p.ID}); err != nil {
				return err
			}
		}
		swept = true
		return q.ExpireSession(ctx, sqlc.ExpireSessionParams(k))
	})
	return swept, err
}

// VerifyUploads checks direct uploads that became READY: the content must match the declared type (sniffed from the
// first bytes), and a declared sha256 must match the object's. A mismatch rejects (type) or marks CORRUPT (hash).
func (j *Jobs) VerifyUploads(ctx context.Context, limit int) (int, error) {
	if err := j.init(); err != nil {
		return 0, err
	}
	items, err := sqlc.New(j.DB).ListUnverifiedAssets(ctx, int32(limit)) //nolint:gosec // a small batch size
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		result, reason, err := j.inspect(ctx, it)
		if err != nil {
			return 0, err
		}
		if err := j.recordVerification(ctx, it, result, reason); err != nil {
			return 0, err
		}
		j.verified.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
	return len(items), nil
}

// inspect checks one object and returns the verdict (ok, missing, type_mismatch or corrupt) with the reject reason.
func (j *Jobs) inspect(ctx context.Context, it sqlc.ListUnverifiedAssetsRow) (result, reason string, err error) {
	head, err := j.Store.GetRange(ctx, it.ObjectKey, 512)
	switch {
	case errors.Is(err, blob.ErrNotFound):
		return "missing", "OBJECT_MISSING", nil
	case err != nil:
		return "", "", err
	case !policy.Sniff(it.ContentType, head):
		return "type_mismatch", policy.ContentTypeMismatch, nil
	case len(it.Sha256) == 32:
		body, err := j.Store.Open(ctx, it.ObjectKey)
		if err != nil {
			return "", "", err
		}
		h := sha256.New()
		_, cerr := io.Copy(h, body)
		_ = body.Close()
		if cerr != nil {
			return "", "", cerr
		}
		if !bytes.Equal(h.Sum(nil), it.Sha256) {
			return "corrupt", "SHA256_MISMATCH", nil
		}
	}
	return "ok", "", nil
}

func (j *Jobs) recordVerification(ctx context.Context, it sqlc.ListUnverifiedAssetsRow, result, reason string) error {
	return pgxx.InTx(ctx, j.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		switch result {
		case "ok":
			return q.MarkAssetVerified(ctx, sqlc.MarkAssetVerifiedParams{TenantID: it.TenantID, ID: it.ID, VerifiedAt: j.Now()})
		case "corrupt":
			return q.MarkAssetCorrupt(ctx, sqlc.MarkAssetCorruptParams{TenantID: it.TenantID, ID: it.ID, Reason: reason, VerifiedAt: j.Now()})
		default: // the content is not what was declared (or gone): the file is refused and its bytes leave the quota
			if result == "type_mismatch" {
				if err := j.Store.Delete(ctx, it.ObjectKey); err != nil {
					return err
				}
			}
			if it.SizeBytes != nil {
				if err := q.ReleaseTenantStorage(ctx, sqlc.ReleaseTenantStorageParams{TenantID: it.TenantID, Size: *it.SizeBytes}); err != nil {
					return err
				}
			}
			return q.RejectVerifiedAsset(ctx, sqlc.RejectVerifiedAssetParams{TenantID: it.TenantID, ID: it.ID, Reason: reason, VerifiedAt: j.Now()})
		}
	})
}

// ReconcileOrphans deletes objects under {tenant}/assets/ that have no asset row and are older than OrphanAge (an upload
// whose row was never written, or whose row was removed).
func (j *Jobs) ReconcileOrphans(ctx context.Context) (int, error) {
	if err := j.init(); err != nil {
		return 0, err
	}
	cutoff := j.Now().Add(-j.OrphanAge)
	deleted := 0
	var batch []string
	flush := func() error {
		n, err := j.deleteOrphans(ctx, batch)
		deleted += n
		batch = batch[:0]
		return err
	}
	err := j.Store.Keys(ctx, "", func(key string, modified time.Time) error {
		if modified.After(cutoff) {
			return nil
		}
		batch = append(batch, key)
		if len(batch) >= 500 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return deleted, err
	}
	return deleted, flush()
}

// deleteOrphans deletes the objects of keys that have no asset row and returns how many it deleted.
func (j *Jobs) deleteOrphans(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	ids := make([]uuid.UUID, 0, len(keys))
	byID := map[uuid.UUID]string{}
	for _, k := range keys {
		parts := strings.Split(k, "/")
		if len(parts) != 3 || parts[1] != "assets" {
			continue
		}
		id, err := uuid.Parse(parts[2])
		if err != nil {
			continue
		}
		ids = append(ids, id)
		byID[id] = k
	}
	known, err := sqlc.New(j.DB).ListExistingAssetIDs(ctx, ids)
	if err != nil {
		return 0, err
	}
	for _, id := range known {
		delete(byID, id)
	}
	deleted := 0
	for _, k := range byID {
		if err := j.Store.Delete(ctx, k); err != nil {
			return deleted, err
		}
		deleted++
		j.orphans.Add(ctx, 1)
	}
	return deleted, nil
}

// Run runs the jobs on their intervals until ctx is done.
func (j *Jobs) Run(ctx context.Context, sweep, verify, orphans time.Duration) {
	if err := j.init(); err != nil {
		j.Log.Error("background jobs not started", "error", err)
		return
	}
	tick := func(d time.Duration, name string, fn func(context.Context) error) {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := fn(ctx); err != nil && ctx.Err() == nil {
					j.Log.Warn(name+" failed", "error", err)
				}
			}
		}
	}
	go tick(sweep, "session sweeper", func(c context.Context) error { _, err := j.SweepExpired(c, 100); return err })
	go tick(verify, "upload verifier", func(c context.Context) error { _, err := j.VerifyUploads(c, 50); return err })
	tick(orphans, "orphan reconciler", func(c context.Context) error { _, err := j.ReconcileOrphans(c); return err })
}
