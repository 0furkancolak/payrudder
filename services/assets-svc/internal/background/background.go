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

	"github.com/0furkancolak/quotify/pkg/blob"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/assets-svc/internal/policy"
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
	m := otel.Meter("github.com/0furkancolak/quotify/services/assets-svc/background")
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
	rows, err := j.DB.Query(ctx, `SELECT tenant_id, id FROM upload_sessions WHERE status = 'OPEN' AND expires_at < $1 ORDER BY expires_at LIMIT $2`, j.Now(), limit)
	if err != nil {
		return 0, err
	}
	type key struct{ tenant, id uuid.UUID }
	var due []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.tenant, &k.id); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	done := 0
	for _, k := range due {
		err := pgxx.InTx(ctx, j.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var st string
			err := tx.QueryRow(ctx, `SELECT status FROM upload_sessions WHERE tenant_id = $1 AND id = $2 FOR UPDATE SKIP LOCKED`, k.tenant, k.id).Scan(&st)
			if errors.Is(err, pgx.ErrNoRows) || (err == nil && st != "OPEN") {
				return nil // another pod took it
			}
			if err != nil {
				return err
			}
			arows, err := tx.Query(ctx, `SELECT id, object_key, s3_upload_id FROM assets WHERE tenant_id = $1 AND session_id = $2 AND status IN ('PENDING','UPLOADING')`, k.tenant, k.id)
			if err != nil {
				return err
			}
			type pending struct {
				id       uuid.UUID
				key      string
				uploadID *string
			}
			var as []pending
			for arows.Next() {
				var p pending
				if err := arows.Scan(&p.id, &p.key, &p.uploadID); err != nil {
					arows.Close()
					return err
				}
				as = append(as, p)
			}
			arows.Close()
			for _, p := range as {
				if p.uploadID != nil {
					if err := j.Store.AbortMultipart(ctx, p.key, *p.uploadID); err != nil {
						return err
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE assets SET status = 'ABORTED', s3_upload_id = NULL WHERE tenant_id = $1 AND id = $2`, k.tenant, p.id); err != nil {
					return err
				}
			}
			_, err = tx.Exec(ctx, `UPDATE upload_sessions SET status = 'EXPIRED' WHERE tenant_id = $1 AND id = $2`, k.tenant, k.id)
			done++
			return err
		})
		if err != nil {
			return done, err
		}
	}
	j.swept.Add(ctx, int64(done))
	return done, nil
}

// VerifyUploads checks direct uploads that became READY: the content must match the declared type (sniffed from the
// first bytes), and a declared sha256 must match the object's. A mismatch rejects (type) or marks CORRUPT (hash).
func (j *Jobs) VerifyUploads(ctx context.Context, limit int) (int, error) {
	if err := j.init(); err != nil {
		return 0, err
	}
	rows, err := j.DB.Query(ctx, `SELECT tenant_id, id, object_key, content_type, size_bytes, sha256 FROM assets
		WHERE status = 'READY' AND verified_at IS NULL AND session_id IS NOT NULL ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return 0, err
	}
	type item struct {
		tenant, id  uuid.UUID
		key, ct     string
		size        *int64
		declaredSHA []byte
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.tenant, &it.id, &it.key, &it.ct, &it.size, &it.declaredSHA); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, it := range items {
		result, reason := "ok", ""
		head, err := j.Store.GetRange(ctx, it.key, 512)
		switch {
		case errors.Is(err, blob.ErrNotFound):
			result, reason = "missing", "OBJECT_MISSING"
		case err != nil:
			return 0, err
		case !policy.Sniff(it.ct, head):
			result, reason = "type_mismatch", policy.ContentTypeMismatch
		case len(it.declaredSHA) == 32:
			body, err := j.Store.Open(ctx, it.key)
			if err != nil {
				return 0, err
			}
			h := sha256.New()
			_, cerr := io.Copy(h, body)
			_ = body.Close()
			if cerr != nil {
				return 0, cerr
			}
			if !bytes.Equal(h.Sum(nil), it.declaredSHA) {
				result, reason = "corrupt", "SHA256_MISMATCH"
			}
		}
		err = pgxx.InTx(ctx, j.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
			switch result {
			case "ok":
				_, err := tx.Exec(ctx, `UPDATE assets SET verified_at = $3 WHERE tenant_id = $1 AND id = $2`, it.tenant, it.id, j.Now())
				return err
			case "corrupt":
				_, err := tx.Exec(ctx, `UPDATE assets SET status = 'CORRUPT', reject_reason = $3, verified_at = $4 WHERE tenant_id = $1 AND id = $2`, it.tenant, it.id, reason, j.Now())
				return err
			default: // the content is not what was declared (or gone): the file is refused and its bytes leave the quota
				if result == "type_mismatch" {
					if err := j.Store.Delete(ctx, it.key); err != nil {
						return err
					}
				}
				if it.size != nil {
					if _, err := tx.Exec(ctx, `UPDATE storage_usage SET bytes_used = GREATEST(bytes_used - $2, 0) WHERE tenant_id = $1`, it.tenant, *it.size); err != nil {
						return err
					}
				}
				_, err := tx.Exec(ctx, `UPDATE assets SET status = 'REJECTED', reject_reason = $3, verified_at = $4 WHERE tenant_id = $1 AND id = $2`, it.tenant, it.id, reason, j.Now())
				return err
			}
		})
		if err != nil {
			return 0, err
		}
		j.verified.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
	}
	return len(items), nil
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
		if len(batch) == 0 {
			return nil
		}
		ids := make([]uuid.UUID, 0, len(batch))
		byID := map[uuid.UUID]string{}
		for _, k := range batch {
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
		rows, err := j.DB.Query(ctx, `SELECT id FROM assets WHERE id = ANY($1)`, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			delete(byID, id)
		}
		rows.Close()
		for _, k := range byID {
			if err := j.Store.Delete(ctx, k); err != nil {
				return err
			}
			deleted++
			j.orphans.Add(ctx, 1)
		}
		batch = batch[:0]
		return rows.Err()
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
