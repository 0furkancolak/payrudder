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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/blob"
	"github.com/0furkancolak/payrudder/pkg/otelx"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/policy"
	"github.com/0furkancolak/payrudder/services/assets-svc/internal/store"
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

// Jobs are the three background jobs. Each works the pooled database as one place and, in cells mode (ADR 0018), the schema of every tenant placed in a cell as
// a place of its own: the statements are the same, run in that tenant's schema.
type Jobs struct {
	DB *pgxpool.Pool // assets_system
	// Cells is the system-role router of cells mode; nil when cells are off, which is a service that only has the pooled database.
	Cells *pgxx.Router
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
	b := otelx.NewInstruments("github.com/0furkancolak/payrudder/services/assets-svc/background")
	j.swept = b.Counter("assets_sessions_swept", "Expired upload sessions aborted")
	j.verified = b.Counter("assets_verified", "Direct uploads verified, by result")
	j.orphans = b.Counter("assets_orphans_deleted", "Objects without an asset row that were deleted")
	return b.Err()
}

// place is where a job works: the pooled database (tenant is the nil UUID) or one tenant's schema in a cell.
type place struct {
	pool   *pgxpool.Pool
	tenant uuid.UUID
}

// tx runs fn in a transaction in the place.
func (p place) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	if p.tenant == uuid.Nil {
		return pgxx.InTx(ctx, p.pool, pgx.TxOptions{}, fn)
	}
	return pgxx.InTenantSchemaTx(ctx, p.pool, p.tenant, fn)
}

// places lists where the jobs work now: the pooled database, then every tenant placed in a cell that is serving (one that is moving is left alone). The list of
// placed tenants is asked of the directory afresh, so a tenant placed a moment ago is not missed.
func (j *Jobs) places(ctx context.Context) ([]place, error) {
	out := []place{{pool: j.DB}}
	if j.Cells == nil {
		return out, nil
	}
	placed, err := j.Cells.PlacedWithin(ctx, 0)
	for _, t := range placed {
		if t.Serving() {
			out = append(out, place{pool: t.Pool, tenant: t.TenantID})
		}
	}
	return out, err
}

// everyPlace runs fn in every place; a place that fails does not stop the others, and the errors come back joined (a single one as it is).
func (j *Jobs) everyPlace(ctx context.Context, fn func(place) error) error {
	ps, err := j.places(ctx)
	errs := []error{err}
	for _, p := range ps {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(p); err != nil {
			errs = append(errs, err)
		}
	}
	return joinErrors(errs)
}

func joinErrors(errs []error) error {
	var real []error
	for _, e := range errs {
		if e != nil {
			real = append(real, e)
		}
	}
	if len(real) == 1 {
		return real[0]
	}
	return errors.Join(real...)
}

// SweepExpired aborts the S3 multipart uploads of expired OPEN sessions and marks them EXPIRED. It returns how many
// sessions it handled. limit bounds each place.
func (j *Jobs) SweepExpired(ctx context.Context, limit int) (int, error) {
	if err := j.init(); err != nil {
		return 0, err
	}
	done := 0
	err := j.everyPlace(ctx, func(p place) error {
		var due []sqlc.ListExpiredSessionsRow
		if err := p.tx(ctx, func(tx pgx.Tx) (err error) {
			due, err = sqlc.New(tx).ListExpiredSessions(ctx, sqlc.ListExpiredSessionsParams{Now: j.Now(), Batch: int32(limit)}) //nolint:gosec // a small batch size
			return err
		}); err != nil {
			return err
		}
		for _, k := range due {
			swept, err := j.sweepSession(ctx, p, k)
			if swept {
				done++
				j.swept.Add(ctx, 1)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return done, err
}

// sweepSession expires one session under its row lock; it reports false when another pod took it.
func (j *Jobs) sweepSession(ctx context.Context, p place, k sqlc.ListExpiredSessionsRow) (swept bool, err error) {
	err = p.tx(ctx, func(tx pgx.Tx) error {
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
	n := 0
	err := j.everyPlace(ctx, func(p place) error {
		var items []sqlc.ListUnverifiedAssetsRow
		if err := p.tx(ctx, func(tx pgx.Tx) (err error) {
			items, err = sqlc.New(tx).ListUnverifiedAssets(ctx, int32(limit)) //nolint:gosec // a small batch size
			return err
		}); err != nil {
			return err
		}
		for _, it := range items {
			result, reason, err := j.inspect(ctx, it)
			if err != nil {
				return err
			}
			if err := j.recordVerification(ctx, p, it, result, reason); err != nil {
				return err
			}
			n++
			j.verified.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
		}
		return nil
	})
	return n, err
}

// inspect checks one object and returns the verdict (ok, missing, type_mismatch or corrupt) with the reject reason.
func (j *Jobs) inspect(ctx context.Context, it sqlc.ListUnverifiedAssetsRow) (result, reason string, err error) {
	head, err := j.Store.GetRange(ctx, it.ObjectKey, policy.SniffBytes)
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

func (j *Jobs) recordVerification(ctx context.Context, p place, it sqlc.ListUnverifiedAssetsRow, result, reason string) error {
	return p.tx(ctx, func(tx pgx.Tx) error {
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
	// Where each tenant's rows are. An object whose tenant lives in a cell is checked against that tenant's schema, one whose tenant is moving is left alone (its
	// rows are in neither place for a moment), and the others against the pooled database. If the directory cannot say, nothing is deleted: an object is an orphan
	// only if its row is nowhere.
	where := map[uuid.UUID]place{}
	moving := map[uuid.UUID]bool{}
	if j.Cells != nil {
		placed, err := j.Cells.PlacedWithin(ctx, 0)
		if err != nil {
			return 0, err
		}
		for _, t := range placed {
			if t.Serving() {
				where[t.TenantID] = place{pool: t.Pool, tenant: t.TenantID}
			} else {
				moving[t.TenantID] = true
			}
		}
	}
	deleted := 0
	var batch []string
	flush := func() error {
		n, err := j.deleteOrphans(ctx, batch, where, moving)
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

// deleteOrphans deletes the objects of keys that have no asset row and returns how many it deleted. where says which tenants have their rows in a cell, moving
// which tenants' objects are not to be touched.
func (j *Jobs) deleteOrphans(ctx context.Context, keys []string, where map[uuid.UUID]place, moving map[uuid.UUID]bool) (int, error) {
	// The ids to look for, by place (a tenant in a cell has a place of its own; the pooled database's is the nil tenant).
	type group struct {
		p    place
		ids  []uuid.UUID
		byID map[uuid.UUID]string
	}
	groups := map[uuid.UUID]*group{}
	for _, k := range keys {
		id, ok := store.AssetIDOfKey(k)
		if !ok {
			continue
		}
		tenantID := store.TenantIDOfKey(k)
		if moving[tenantID] {
			continue
		}
		p, inCell := where[tenantID]
		if !inCell {
			p = place{pool: j.DB}
		}
		g := groups[p.tenant]
		if g == nil {
			g = &group{p: p, byID: map[uuid.UUID]string{}}
			groups[p.tenant] = g
		}
		g.ids = append(g.ids, id)
		g.byID[id] = k
	}
	deleted := 0
	for _, g := range groups {
		var known []uuid.UUID
		if err := g.p.tx(ctx, func(tx pgx.Tx) (err error) {
			known, err = sqlc.New(tx).ListExistingAssetIDs(ctx, g.ids)
			return err
		}); err != nil {
			return deleted, err
		}
		for _, id := range known {
			delete(g.byID, id)
		}
		for _, k := range g.byID {
			if err := j.Store.Delete(ctx, k); err != nil {
				return deleted, err
			}
			deleted++
			j.orphans.Add(ctx, 1)
		}
	}
	return deleted, nil
}

// loop is one job and how often it runs.
type loop struct {
	name  string
	every time.Duration
	fn    func(context.Context) error
}

// Run runs the jobs on their intervals until ctx is done, and returns when every one of them has stopped: a job in the middle of its work is not left
// using the database and the object store after the service has closed them.
func (j *Jobs) Run(ctx context.Context, sweep, verify, orphans time.Duration) {
	if err := j.init(); err != nil {
		j.Log.Error("background jobs not started", "error", err)
		return
	}
	j.run(ctx, []loop{
		{"session sweeper", sweep, func(c context.Context) error { _, err := j.SweepExpired(c, 100); return err }},
		{"upload verifier", verify, func(c context.Context) error { _, err := j.VerifyUploads(c, 50); return err }},
		{"orphan reconciler", orphans, func(c context.Context) error { _, err := j.ReconcileOrphans(c); return err }},
	})
}

func (j *Jobs) run(ctx context.Context, loops []loop) {
	var wg sync.WaitGroup
	for _, l := range loops {
		wg.Go(func() { j.every(ctx, l) })
	}
	wg.Wait()
}

func (j *Jobs) every(ctx context.Context, l loop) {
	t := time.NewTicker(l.every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if ctx.Err() != nil { // both were ready: the cancellation wins
				return
			}
			if err := l.fn(ctx); err != nil && ctx.Err() == nil {
				j.Log.Warn(l.name+" failed", "error", err)
			}
		}
	}
}
