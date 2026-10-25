package command

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	ingestv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/ingest/v1"
	"github.com/0furkancolak/payrudder/services/ingest-svc/internal/store/sqlc"
)

// dedup claims the idempotency keys and returns the events that are new. Duplicates and reuses are written to results.
func (c *Commands) dedup(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, bucket time.Time, earlier []time.Time, todo []*pending, results []*ingestv1.EventResult) ([]*pending, error) {
	var keyed []*pending
	var accepted []*pending
	for _, p := range todo {
		if p.key == "" {
			accepted = append(accepted, p) // sandbox without a key: no dedup
		} else {
			keyed = append(keyed, p)
		}
	}
	if len(keyed) == 0 {
		return accepted, nil
	}
	// Sorted so that concurrent batches claim keys in the same order and cannot deadlock on each other.
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })

	fresh, err := skipEarlierBuckets(ctx, q, tenantID, earlier, keyed, results)
	if err != nil {
		return nil, err
	}
	if len(fresh) == 0 {
		return accepted, nil
	}
	won, err := claimKeys(ctx, q, tenantID, bucket, fresh)
	if err != nil {
		return nil, err
	}
	var lost []*pending
	for _, p := range fresh {
		if won[p.key] {
			accepted = append(accepted, p)
		} else {
			lost = append(lost, p)
		}
	}
	if err := readClaimed(ctx, q, tenantID, bucket, lost, results); err != nil {
		return nil, err
	}
	sort.Slice(accepted, func(i, j int) bool { return accepted[i].idx < accepted[j].idx })
	return accepted, nil
}

// findKeys reads the rows the given buckets hold for the keys of ps, by key.
func findKeys(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, buckets []time.Time, ps []*pending) (map[string]sqlc.FindIdempotencyKeysRow, error) {
	keys := make([]string, len(ps))
	for i, p := range ps {
		keys[i] = p.key
	}
	rows, err := q.FindIdempotencyKeys(ctx, sqlc.FindIdempotencyKeysParams{TenantID: tenantID, Keys: keys, Buckets: buckets})
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]sqlc.FindIdempotencyKeysRow, len(rows))
	for _, r := range rows {
		byKey[r.IdempotencyKey] = r
	}
	return byKey, nil
}

// skipEarlierBuckets writes the result of every event whose key one of the two previous months' buckets already holds, which ON
// CONFLICT does not cover, and returns the others.
func skipEarlierBuckets(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, earlier []time.Time, keyed []*pending, results []*ingestv1.EventResult) ([]*pending, error) {
	known, err := findKeys(ctx, q, tenantID, earlier, keyed)
	if err != nil {
		return nil, fmt.Errorf("find earlier buckets: %w", err)
	}
	var fresh []*pending
	for _, p := range keyed {
		if r, ok := known[p.key]; ok {
			results[p.idx] = existingResult(r, p)
		} else {
			fresh = append(fresh, p)
		}
	}
	return fresh, nil
}

// claimKeys inserts the keys into this month's bucket and returns the ones this transaction won.
func claimKeys(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, bucket time.Time, fresh []*pending) (map[string]bool, error) {
	ins := sqlc.InsertIdempotencyKeysParams{TenantID: tenantID, CreatedAt: bucket}
	for _, p := range fresh {
		ins.Keys = append(ins.Keys, p.key)
		ins.EventIds = append(ins.EventIds, p.event.EventId)
		ins.Hashes = append(ins.Hashes, p.hash)
	}
	won, err := q.InsertIdempotencyKeys(ctx, ins)
	if err != nil {
		return nil, fmt.Errorf("insert idempotency keys: %w", err)
	}
	isNew := make(map[string]bool, len(won))
	for _, k := range won {
		isNew[k] = true
	}
	return isNew, nil
}

// readClaimed writes the result of the events whose keys another transaction claimed first (it has committed by now,
// because ON CONFLICT waited for it).
func readClaimed(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, bucket time.Time, lost []*pending, results []*ingestv1.EventResult) error {
	if len(lost) == 0 {
		return nil
	}
	byKey, err := findKeys(ctx, q, tenantID, []time.Time{bucket}, lost)
	if err != nil {
		return fmt.Errorf("find conflicting keys: %w", err)
	}
	for _, p := range lost {
		r, ok := byKey[p.key]
		if !ok {
			return fmt.Errorf("idempotency key %q conflicted but cannot be read back", p.key)
		}
		results[p.idx] = existingResult(r, p)
	}
	return nil
}

func existingResult(r sqlc.FindIdempotencyKeysRow, p *pending) *ingestv1.EventResult {
	if bytes.Equal(r.PayloadHash, p.hash) {
		return &ingestv1.EventResult{Status: ingestv1.EventResult_STATUS_DUPLICATE, EventId: r.EventID}
	}
	return rejected(keyReused())
}
