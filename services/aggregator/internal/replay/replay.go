// Package replay rebuilds one tenant's aggregates from the Kafka log into a new generation, next to the live one
// (ADR 0012, docs/07-correctness.md#8-replay--re-rating).
//
// A job moves through five steps:
//
//  1. running: a replay handler (the live apply code, pointed at generation N+1, with its own offsets) reads every
//     partition from the earliest offset that can belong to the first period up to the end offsets taken at the start;
//  2. catching_up: in one transaction that waits for the tenant's in-flight live batches, generation N+1 becomes
//     catching_up and the live consumer's position is recorded as dual_write_from. From then on the live consumer
//     writes the tenant into both generations; the replay reads on up to that position;
//  3. ready: the difference between N and N+1 is computed and stored in the job;
//  4. promoted: one transaction retires N and activates N+1. Live traffic is never paused, other tenants never touched;
//  5. (or discarded: the generation and its rows are deleted.)
//
// Everything is idempotent: processed_events is keyed by generation, offsets are in the database, and a job that is
// interrupted is picked up by any aggregator replica (an advisory lock lets only one run it).
package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"golang.org/x/sync/errgroup"

	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/services/aggregator/internal/apply"
	"github.com/0furkancolak/quotify/services/aggregator/internal/store/sqlc"
)

// Job statuses (replay_jobs.status).
const (
	Running    = "running"
	CatchingUp = "catching_up"
	Ready      = "ready"
	Promoted   = "promoted"
	Discarded  = "discarded"
	Failed     = "failed"
)

// Errors a caller can tell apart.
var (
	ErrInvalid  = errors.New("replay: invalid request")
	ErrOpen     = errors.New("replay: the tenant already has an open replay")
	ErrNotFound = errors.New("replay: no such job")
	ErrState    = errors.New("replay: the job is not in a state that allows this")
)

var periodRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// Config wires a Manager.
type Config struct {
	Pool      *pgxpool.Pool // aggregator_system
	Brokers   []string
	Topic     string // usage.events.v1
	LiveGroup string // the live consumer's group in consumer_offsets
	Meters    apply.MeterSource
	Settings  apply.Settings
	Log       *slog.Logger
	// Lateness is the longest an event can arrive after its period (35 days): the replay starts that far before the first period.
	Lateness time.Duration
	// Scan is how often jobs that need work are looked for (default 2 s).
	Scan time.Duration
	// BatchSize is the records per apply transaction (default 1000).
	BatchSize int
	Now       func() time.Time
}

// Manager runs replay jobs.
type Manager struct {
	cfg      Config
	mu       sync.Mutex
	running  map[uuid.UUID]bool
	failures map[uuid.UUID]int
}

// maxFailures is how many times in a row a job may be interrupted before it is marked failed (and its generation discarded).
const maxFailures = 10

// New returns a Manager.
func New(cfg Config) *Manager {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Lateness == 0 {
		cfg.Lateness = 35 * 24 * time.Hour
	}
	if cfg.Scan == 0 {
		cfg.Scan = 2 * time.Second
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Manager{cfg: cfg, running: map[uuid.UUID]bool{}, failures: map[uuid.UUID]int{}}
}

// Job is a replay_jobs row.
type Job struct {
	ID, TenantID     uuid.UUID
	From, To         string
	SourceGeneration int32
	TargetGeneration int32
	Status           string
	Reason           string
	StartOffsets     map[int32]int64
	EndOffsets       map[int32]int64
	DualWriteFrom    map[int32]int64
	Verification     *Verification
	LastError        string
	CreatedAt        time.Time
	CompletedAt      *time.Time
}

// jobOf converts a row; a job from before source_generation existed read generation 1.
func jobOf(r sqlc.ReplayJob) (*Job, error) {
	j := &Job{
		ID: r.ID, TenantID: r.TenantID, From: r.FromPeriodID, To: r.ToPeriodID, SourceGeneration: 1,
		TargetGeneration: r.TargetGeneration, Status: r.Status, Reason: r.Reason, CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt,
	}
	if r.SourceGeneration != nil {
		j.SourceGeneration = *r.SourceGeneration
	}
	if r.LastError != nil {
		j.LastError = *r.LastError
	}
	var err error
	if j.StartOffsets, err = offsetsOf(r.StartOffsets); err != nil {
		return nil, err
	}
	if j.EndOffsets, err = offsetsOf(r.EndOffsets); err != nil {
		return nil, err
	}
	if j.DualWriteFrom, err = offsetsOf(r.DualWriteFrom); err != nil {
		return nil, err
	}
	if len(r.Verification) > 0 {
		j.Verification = &Verification{}
		if err := json.Unmarshal(r.Verification, j.Verification); err != nil {
			return nil, err
		}
	}
	return j, nil
}

func offsetsOf(b []byte) (map[int32]int64, error) {
	if len(b) == 0 {
		return nil, nil
	}
	var raw map[string]int64
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	out := make(map[int32]int64, len(raw))
	for k, v := range raw {
		var p int32
		if _, err := fmt.Sscanf(k, "%d", &p); err != nil {
			return nil, err
		}
		out[p] = v
	}
	return out, nil
}

func offsetsJSON(m map[int32]int64) []byte {
	raw := make(map[string]int64, len(m))
	for p, o := range m {
		raw[fmt.Sprint(p)] = o
	}
	b, _ := json.Marshal(raw)
	return b
}

// Get loads a job.
func (m *Manager) Get(ctx context.Context, tenantID, jobID uuid.UUID) (*Job, error) {
	r, err := sqlc.New(m.cfg.Pool).GetReplayJob(ctx, sqlc.GetReplayJobParams{TenantID: tenantID, ID: jobID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return jobOf(r)
}

func (m *Manager) group(job *Job) string { return "aggregator-replay-" + job.ID.String() }

// Start creates generation N+1 for the tenant and a job that rebuilds it. The job runs on whichever replica picks it up.
func (m *Manager) Start(ctx context.Context, tenantID uuid.UUID, from, to, reason string) (*Job, error) {
	if to == "" {
		to = from
	}
	start, err := validateStart(from, to, reason)
	if err != nil {
		return nil, err
	}
	startOffsets, endOffsets, err := m.logRange(ctx, start)
	if err != nil {
		return nil, err
	}
	nj := newJob{id: uuid.New(), tenantID: tenantID, from: from, to: to, reason: reason, start: startOffsets, end: endOffsets}
	err = pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return insertJob(ctx, sqlc.New(tx), nj) })
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "one_open_replay" {
		return nil, ErrOpen
	}
	if err != nil {
		return nil, err
	}
	return m.Get(ctx, tenantID, nj.id)
}

// validateStart checks the request and returns the first instant of the first period.
func validateStart(from, to, reason string) (time.Time, error) {
	if !periodRe.MatchString(from) || !periodRe.MatchString(to) || from > to {
		return time.Time{}, fmt.Errorf("%w: periods must look like 2026-10 and from <= to", ErrInvalid)
	}
	if reason == "" {
		return time.Time{}, fmt.Errorf("%w: a reason is required", ErrInvalid)
	}
	start, err := time.Parse("2006-01", from)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return start, nil
}

// logRange reads the topic's offsets per partition: where the first period's events can begin (the lateness before
// start) and where the log ends now.
func (m *Manager) logRange(ctx context.Context, start time.Time) (startOffsets, endOffsets map[int32]int64, err error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(m.cfg.Brokers...))
	if err != nil {
		return nil, nil, err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ends, err := adm.ListEndOffsets(ctx, m.cfg.Topic)
	if err != nil {
		return nil, nil, fmt.Errorf("end offsets: %w", err)
	}
	after, err := adm.ListOffsetsAfterMilli(ctx, start.Add(-m.cfg.Lateness).UnixMilli(), m.cfg.Topic)
	if err != nil {
		return nil, nil, fmt.Errorf("start offsets: %w", err)
	}
	endOffsets, startOffsets = map[int32]int64{}, map[int32]int64{}
	ends.Each(func(o kadm.ListedOffset) { endOffsets[o.Partition] = o.Offset })
	after.Each(func(o kadm.ListedOffset) {
		off := o.Offset
		if off < 0 || off > endOffsets[o.Partition] { // nothing at or after that time: start at the end
			off = endOffsets[o.Partition]
		}
		startOffsets[o.Partition] = off
	})
	return startOffsets, endOffsets, nil
}

type newJob struct {
	id, tenantID     uuid.UUID
	from, to, reason string
	start, end       map[int32]int64
}

// insertJob adds the building generation after the tenant's newest and the job that rebuilds it.
func insertJob(ctx context.Context, q *sqlc.Queries, nj newJob) error {
	if err := q.EnsureActiveGenerationOne(ctx, nj.tenantID); err != nil {
		return err
	}
	source, err := q.TenantActiveGeneration(ctx, nj.tenantID)
	if err != nil {
		return err
	}
	target, err := q.NextGeneration(ctx, nj.tenantID)
	if err != nil {
		return err
	}
	if err := q.InsertBuildingGeneration(ctx, sqlc.InsertBuildingGenerationParams{TenantID: nj.tenantID, Generation: target}); err != nil {
		return err
	}
	return q.InsertReplayJob(ctx, sqlc.InsertReplayJobParams{
		ID: nj.id, TenantID: nj.tenantID, FromPeriodID: nj.from, ToPeriodID: nj.to, TargetGeneration: target, SourceGeneration: &source,
		StartOffsets: offsetsJSON(nj.start), EndOffsets: offsetsJSON(nj.end), Reason: nj.reason,
	})
}

// Run looks for jobs that need work until ctx ends.
func (m *Manager) Run(ctx context.Context) error {
	t := time.NewTicker(m.cfg.Scan)
	defer t.Stop()
	for {
		m.scan(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

func (m *Manager) scan(ctx context.Context) {
	rows, err := sqlc.New(m.cfg.Pool).ListOpenReplayJobs(ctx)
	if err != nil {
		m.cfg.Log.WarnContext(ctx, "replay: scan failed", "error", err)
		return
	}
	jobs := make([]*Job, len(rows))
	for i, r := range rows {
		if jobs[i], err = jobOf(r); err != nil {
			m.cfg.Log.WarnContext(ctx, "replay: bad job row", "error", err)
			return
		}
	}
	for _, j := range jobs {
		m.launch(ctx, j)
	}
}

// launch runs the job on its own goroutine unless this replica is already running it.
func (m *Manager) launch(ctx context.Context, j *Job) {
	m.mu.Lock()
	busy := m.running[j.ID]
	if !busy {
		m.running[j.ID] = true
	}
	m.mu.Unlock()
	if busy {
		return
	}
	go func() {
		defer func() { m.mu.Lock(); delete(m.running, j.ID); m.mu.Unlock() }()
		m.afterRun(ctx, j, m.runJob(ctx, j.TenantID, j.ID))
	}()
}

// afterRun counts consecutive interruptions of a job and gives up on it after maxFailures.
func (m *Manager) afterRun(ctx context.Context, j *Job, err error) {
	if err == nil || ctx.Err() != nil || errors.Is(err, errGone) {
		m.mu.Lock()
		delete(m.failures, j.ID)
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	m.failures[j.ID]++
	n := m.failures[j.ID]
	m.mu.Unlock()
	m.cfg.Log.WarnContext(ctx, "replay: job interrupted; it will be retried", "job", j.ID, "tenant", j.TenantID, "attempt", n, "error", err)
	if n >= maxFailures {
		q := sqlc.New(m.cfg.Pool)
		_ = q.FailReplayJob(ctx, sqlc.FailReplayJobParams{TenantID: j.TenantID, ID: j.ID, LastError: err.Error()})
		_ = q.DiscardOpenGeneration(ctx, sqlc.DiscardOpenGenerationParams{TenantID: j.TenantID, Generation: j.TargetGeneration})
	}
}

// RunOnce advances one job as far as it goes in this call (tests, and a replica taking over).
func (m *Manager) RunOnce(ctx context.Context, tenantID, jobID uuid.UUID) error {
	return m.runJob(ctx, tenantID, jobID)
}

func lockKey(id uuid.UUID) int64 {
	var k int64
	for i := 0; i < 8; i++ {
		k = k<<8 | int64(id[i])
	}
	return k
}

func (m *Manager) runJob(ctx context.Context, tenantID, jobID uuid.UUID) error {
	// One replica per job: a session advisory lock, held on a connection for as long as the job runs.
	conn, err := m.cfg.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	lock := sqlc.New(conn)
	got, err := lock.TryReplayLock(ctx, lockKey(jobID))
	if err != nil || !got {
		return err
	}
	defer func() { _ = lock.ReplayUnlock(context.WithoutCancel(ctx), lockKey(jobID)) }()

	for {
		job, err := m.Get(ctx, tenantID, jobID)
		if err != nil {
			return err
		}
		switch job.Status {
		case Running:
			err = m.readHistory(ctx, job)
		case CatchingUp:
			err = m.catchUpAndVerify(ctx, job)
		default:
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readHistory applies the log up to the end offsets taken at the start, then starts the dual write.
func (m *Manager) readHistory(ctx context.Context, job *Job) error {
	if err := m.readTo(ctx, job, job.EndOffsets); err != nil {
		return err
	}
	return m.beginCatchUp(ctx, job)
}

// catchUpAndVerify applies the log up to the live consumer's position when the dual write began, compares the two
// generations and marks the job ready.
func (m *Manager) catchUpAndVerify(ctx context.Context, job *Job) error {
	if err := m.readTo(ctx, job, job.DualWriteFrom); err != nil {
		return err
	}
	ver, err := m.verify(ctx, job)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(ver)
	if err := sqlc.New(m.cfg.Pool).MarkReplayReady(ctx, sqlc.MarkReplayReadyParams{TenantID: job.TenantID, ID: job.ID, Verification: b}); err != nil {
		return err
	}
	m.cfg.Log.InfoContext(ctx, "replay: ready to promote", "job", job.ID, "tenant", job.TenantID, "changed", ver.RowsChanged, "only_old", ver.RowsOnlyInOld, "only_new", ver.RowsOnlyInNew)
	return nil
}

var errGone = errors.New("replay: the job was discarded or finished elsewhere")

// handler builds the replay handler of a job.
func (m *Manager) handler(job *Job) *apply.Handler {
	return &apply.Handler{
		Pool: m.cfg.Pool, Meters: m.cfg.Meters, Settings: m.cfg.Settings, Log: m.cfg.Log,
		ConsumerGroup: m.group(job), Topic: m.cfg.Topic, MeterRetryDelays: []time.Duration{},
		Replay: &apply.Replay{Tenant: job.TenantID, Generation: job.TargetGeneration, FromPeriod: job.From, ToPeriod: job.To},
	}
}

// readTo applies every partition of the job's range up to limits (exclusive).
func (m *Manager) readTo(ctx context.Context, job *Job, limits map[int32]int64) error {
	h := m.handler(job)
	parts := make([]int32, 0, len(limits))
	for p := range limits {
		parts = append(parts, p)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })
	pos, err := h.LoadOffsets(ctx, m.cfg.Topic, parts)
	if err != nil {
		return err
	}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)
	for _, p := range parts {
		start, ok := pos[p]
		if !ok {
			start = job.StartOffsets[p]
		}
		if start >= limits[p] {
			continue
		}
		g.Go(func() error { return m.readPartition(gctx, job, h, p, start, limits[p]) })
	}
	return g.Wait()
}

func (m *Manager) readPartition(ctx context.Context, job *Job, h *apply.Handler, p int32, from, limit int64) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(m.cfg.Brokers...), kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{m.cfg.Topic: {p: kgo.NewOffset().At(from)}}),
		kgo.FetchMaxWait(500*time.Millisecond))
	if err != nil {
		return err
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	next, idle, batches := from, 0, 0
	for next < limit {
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		fetches := cl.PollRecords(pctx, m.cfg.BatchSize)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var recs []*kgo.Record
		past := false
		fetches.EachRecord(func(r *kgo.Record) {
			if r.Partition != p {
				return
			}
			if r.Offset >= limit {
				past = true
				return
			}
			recs = append(recs, r)
		})
		if len(recs) > 0 {
			idle = 0
			if err := h.Apply(ctx, p, recs); err != nil {
				return err
			}
			next = recs[len(recs)-1].Offset + 1
			batches++
			if batches%20 == 0 { // a discarded job stops reading
				if err := m.stillWanted(ctx, job); err != nil {
					return err
				}
			}
			continue
		}
		if past {
			return nil
		}
		// Nothing arrived: either the log ends before limit (the limit was a high-water mark that is not a record) or the broker is slow.
		if idle++; idle >= 2 {
			ends, err := adm.ListEndOffsets(ctx, m.cfg.Topic)
			if err != nil {
				return err
			}
			if o, ok := ends.Lookup(m.cfg.Topic, p); ok && o.Offset <= next {
				return nil
			}
			if err := m.stillWanted(ctx, job); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) stillWanted(ctx context.Context, job *Job) error {
	status, err := sqlc.New(m.cfg.Pool).ReplayJobStatus(ctx, sqlc.ReplayJobStatusParams{TenantID: job.TenantID, ID: job.ID})
	if err != nil {
		return err
	}
	if status != Running && status != CatchingUp {
		return errGone
	}
	return nil
}

// beginCatchUp flips the generation to catching_up in one transaction that first locks the tenant's active row: the
// live consumer holds it FOR SHARE through every batch that touches the tenant, so the flip waits for those batches, and
// the batches after it see catching_up and dual-write. The live consumer's position at that moment is dual_write_from.
func (m *Manager) beginCatchUp(ctx context.Context, job *Job) error {
	return pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if _, err := q.LockActiveGeneration(ctx, job.TenantID); err != nil {
			return err
		}
		offs, err := q.ListOffsets(ctx, sqlc.ListOffsetsParams{ConsumerGroup: m.cfg.LiveGroup, Topic: m.cfg.Topic})
		if err != nil {
			return err
		}
		dual := make(map[int32]int64, len(offs))
		for _, o := range offs {
			dual[o.Partition] = o.NextOffset
		}
		// A partition the live consumer has never read has nothing before it that the live generation holds.
		for p := range job.EndOffsets {
			if _, ok := dual[p]; !ok {
				dual[p] = 0
			}
		}
		n, err := q.MarkGenerationCatchingUp(ctx, sqlc.MarkGenerationCatchingUpParams{TenantID: job.TenantID, Generation: job.TargetGeneration})
		if err != nil {
			return err
		}
		if n != 1 {
			return errGone
		}
		return q.MarkReplayCatchingUp(ctx, sqlc.MarkReplayCatchingUpParams{TenantID: job.TenantID, ID: job.ID, DualWriteFrom: offsetsJSON(dual)})
	})
}

// lockJob loads the job row FOR UPDATE.
func lockJob(ctx context.Context, q *sqlc.Queries, tenantID, jobID uuid.UUID) (*Job, error) {
	r, err := q.GetReplayJobForUpdate(ctx, sqlc.GetReplayJobForUpdateParams{TenantID: tenantID, ID: jobID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return jobOf(r)
}

// Promote makes the job's generation the tenant's active one.
func (m *Manager) Promote(ctx context.Context, tenantID, jobID uuid.UUID) (*Job, error) {
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		j, err := lockJob(ctx, q, tenantID, jobID)
		if err != nil {
			return err
		}
		if j.Status == Promoted {
			return nil // idempotent
		}
		if j.Status != Ready {
			return fmt.Errorf("%w: the job is %s, only a ready job can be promoted", ErrState, j.Status)
		}
		return swapGenerations(ctx, q, j)
	})
	if err != nil {
		return nil, err
	}
	return m.Get(ctx, tenantID, jobID)
}

// swapGenerations retires the source generation and activates the target one.
func swapGenerations(ctx context.Context, q *sqlc.Queries, j *Job) error {
	// Wait for the live batches that hold the tenant's generation rows; new ones wait for this transaction.
	if err := q.LockLiveGenerations(ctx, j.TenantID); err != nil {
		return err
	}
	if err := q.RetireActiveGeneration(ctx, sqlc.RetireActiveGenerationParams{TenantID: j.TenantID, Generation: j.SourceGeneration}); err != nil {
		return err
	}
	n, err := q.ActivateGeneration(ctx, sqlc.ActivateGenerationParams{TenantID: j.TenantID, Generation: j.TargetGeneration})
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("%w: generation %d is not catching up", ErrState, j.TargetGeneration)
	}
	return q.MarkReplayPromoted(ctx, sqlc.MarkReplayPromotedParams{TenantID: j.TenantID, ID: j.ID})
}

// Discard abandons a generation that was not promoted and deletes its rows.
func (m *Manager) Discard(ctx context.Context, tenantID, jobID uuid.UUID) (*Job, error) {
	var job *Job
	err := pgxx.InTx(ctx, m.cfg.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		j, err := lockJob(ctx, q, tenantID, jobID)
		if err != nil {
			return err
		}
		job = j
		switch j.Status {
		case Discarded:
			return nil
		case Promoted:
			return fmt.Errorf("%w: a promoted generation is live; replay again to go back", ErrState)
		}
		if err := q.LockLiveGenerations(ctx, tenantID); err != nil {
			return err
		}
		if err := q.DiscardGeneration(ctx, sqlc.DiscardGenerationParams{TenantID: tenantID, Generation: j.TargetGeneration}); err != nil {
			return err
		}
		return q.MarkReplayDiscarded(ctx, sqlc.MarkReplayDiscardedParams{TenantID: tenantID, ID: jobID})
	})
	if err != nil {
		return nil, err
	}
	if err := m.deleteGeneration(ctx, tenantID, job.TargetGeneration); err != nil {
		return nil, err
	}
	return m.Get(ctx, tenantID, jobID)
}

// deleteGeneration removes the rows of an abandoned generation: nothing reads or writes them any more.
func (m *Manager) deleteGeneration(ctx context.Context, tenantID uuid.UUID, generation int32) error {
	q := sqlc.New(m.cfg.Pool)
	if err := q.DeleteGenerationUsage(ctx, sqlc.DeleteGenerationUsageParams{TenantID: tenantID, Generation: generation}); err != nil {
		return err
	}
	if err := q.DeleteGenerationLateUsage(ctx, sqlc.DeleteGenerationLateUsageParams{TenantID: tenantID, Generation: generation}); err != nil {
		return err
	}
	return q.DeleteGenerationProcessedEvents(ctx, sqlc.DeleteGenerationProcessedEventsParams{TenantID: tenantID, Generation: generation})
}
