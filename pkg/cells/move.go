package cells

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// MoveOptions tune MoveTenant.
type MoveOptions struct {
	// Settle is how long to wait after the directory marks the tenant as moving, so that a service whose cached answer is older still routes by the old one for no
	// longer than that (default 2 × the directory's default TTL, 60s). It is the only fence for a tenant that leaves the pooled database; a tenant that leaves a
	// cell is also fenced in the database (pgxx.Move.Fence), and the wait only keeps the stale writers' errors short.
	Settle time.Duration
	// FenceWait bounds the wait for in-flight transactions of a cell schema (default 30s).
	FenceWait time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
	Log *slog.Logger
}

func (o *MoveOptions) defaults() {
	if o.Settle == 0 {
		o.Settle = 60 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
}

// MoveReport says what a move did.
type MoveReport struct {
	TenantID uuid.UUID
	From, To string // cell names; "" is the pooled database
	// Copied is what was copied, by service.
	Copied map[string]pgxx.CopyReport
	// Retired is, by service, the schema the source was set aside as ("" when the pooled rows were deleted).
	Retired map[string]string
	// Problems are failures after the point of no return (the directory points at the destination): the move is done, these need a look.
	Problems []error
}

// MoveTenant moves one tenant's data, in every service of the config, from where the directory says it is to toCell ("" for the pooled database):
//
//  1. the directory fences the tenant (moving): services refuse its requests and jobs skip it. A move to a cell is refused here while any job is not converted.
//  2. the move waits Settle, then closes each source schema (cells) to the services' roles and waits for transactions in it.
//  3. each service's data is copied and checked against the source, row for row (count and checksum), in one transaction per service.
//  4. the directory points at the destination and the tenant serves again.
//  5. each source is retired: a cell's schema is set aside (DropRetired removes it later), the pooled rows are deleted.
//
// Until step 4 the move can be given up with nothing lost: everything done so far is undone (sources opened again, destinations emptied or dropped, the directory
// unfenced), and that is what an error before step 4 does. After step 4 the tenant is served from the destination and a failure in step 5 only leaves old
// data behind, reported in Problems.
func MoveTenant(ctx context.Context, cfg Config, dir Directory, tenantID uuid.UUID, toCell string, o MoveOptions) (MoveReport, error) {
	o.defaults()
	rep := MoveReport{TenantID: tenantID, To: toCell, Copied: map[string]pgxx.CopyReport{}, Retired: map[string]string{}}
	if toCell != "" {
		if _, ok := cfg.Cells[toCell]; !ok {
			return rep, fmt.Errorf("cells: cell %q is not in the config", toCell)
		}
	}
	cur, err := dir.Placement(ctx, tenantID)
	if err != nil {
		return rep, fmt.Errorf("cells: placement of %s: %w", tenantID, err)
	}
	rep.From = cur.Cell
	if cur.Status != tenantdir.StatusReady {
		return rep, fmt.Errorf("cells: tenant %s is %s: finish or abort that first", tenantID, cur.Status)
	}
	if cur.Cell == toCell {
		return rep, fmt.Errorf("cells: tenant %s is already there", tenantID)
	}
	if cur.Cell != "" {
		if _, ok := cfg.Cells[cur.Cell]; !ok {
			return rep, fmt.Errorf("cells: the tenant is in cell %q, which is not in the config", cur.Cell)
		}
	}
	moves, err := planMoves(cfg, tenantID, cur.Cell, toCell, o)
	if err != nil {
		return rep, err
	}

	if err := dir.BeginMove(ctx, tenantID, toCell); err != nil {
		return rep, fmt.Errorf("cells: fence the tenant in the directory: %w", err)
	}
	o.Log.InfoContext(ctx, "move begun", "tenant_id", tenantID, "from", cur.Cell, "to", toCell, "settle", o.Settle)
	// Everything from here until the directory points at the destination is undone on failure. The context of the undo is detached: a cancelled move still cleans up.
	var fenced, touched []pgxx.Move
	giveUp := func(cause error) (MoveReport, error) {
		undo := context.WithoutCancel(ctx)
		var errs []error
		for _, m := range touched {
			errs = append(errs, m.DiscardDestination(undo))
		}
		for _, m := range fenced {
			errs = append(errs, m.Unfence(undo))
		}
		errs = append(errs, dir.AbortMove(undo, tenantID))
		if err := errors.Join(errs...); err != nil {
			return rep, fmt.Errorf("cells: move given up (%w), and undoing it failed, look at it with doctor: %w", cause, err)
		}
		return rep, fmt.Errorf("cells: move given up, everything undone: %w", cause)
	}

	select {
	case <-ctx.Done():
		return giveUp(ctx.Err())
	case <-time.After(o.Settle):
	}
	for _, name := range cfg.ServiceNames() {
		m := moves[name]
		fenced = append(fenced, m) // even a failed fence may have closed some roles
		if err := m.Fence(ctx); err != nil {
			return giveUp(fmt.Errorf("fence %s: %w", name, err))
		}
	}
	for _, name := range cfg.ServiceNames() {
		m := moves[name]
		touched = append(touched, m)
		r, err := m.Copy(ctx)
		if err != nil {
			return giveUp(fmt.Errorf("copy %s: %w", name, err))
		}
		rep.Copied[name] = r
	}
	if err := dir.FinishMove(ctx, tenantID, toCell, cfg.Region(toCell)); err != nil {
		// A reply that was lost is not a refusal: if the directory did move, undoing the copy would leave a tenant with no data. Ask it, and when it cannot say,
		// touch nothing.
		p, perr := dir.Placement(context.WithoutCancel(ctx), tenantID)
		switch {
		case perr != nil:
			return rep, fmt.Errorf("cells: the directory did not answer the end of the move for %s (%w) and cannot be asked (%w): nothing is undone; run doctor, then either abort the move (payrudderctl cells abort) or finish it", tenantID, err, perr)
		case p.Status == tenantdir.StatusMoving:
			return giveUp(fmt.Errorf("point the directory at the destination: %w", err))
		case p.Cell != toCell:
			return rep, fmt.Errorf("cells: the directory has %s in %q after the move to %q failed (%w): nothing is undone; look at it with doctor", tenantID, p.Cell, toCell, err)
		}
		o.Log.WarnContext(ctx, "the directory moved the tenant although the call failed", "tenant_id", tenantID, "error", err)
	}
	o.Log.InfoContext(ctx, "move finished in the directory", "tenant_id", tenantID, "to", toCell)

	// The point of no return: the tenant is served from the destination.
	for _, name := range cfg.ServiceNames() {
		retired, err := moves[name].RetireSource(context.WithoutCancel(ctx), o.Now())
		if err != nil {
			rep.Problems = append(rep.Problems, fmt.Errorf("retire the source of %s: %w", name, err))
			continue
		}
		rep.Retired[name] = retired
	}
	return rep, nil
}

// AbortTenantMove undoes a move that was left half done (the tool was killed, a database went away): it opens the source schemas again, discards what the
// destination holds and unfences the tenant in the directory. toCell is where the move was going. It must not be used after the directory finished the move.
func AbortTenantMove(ctx context.Context, cfg Config, dir Directory, tenantID uuid.UUID, toCell string, o MoveOptions) error {
	o.defaults()
	cur, err := dir.Placement(ctx, tenantID)
	if err != nil {
		return err
	}
	if cur.Status != tenantdir.StatusMoving {
		return fmt.Errorf("cells: tenant %s is %s, not moving", tenantID, cur.Status)
	}
	moves, err := planMoves(cfg, tenantID, cur.Cell, toCell, o)
	if err != nil {
		return err
	}
	var errs []error
	for _, name := range cfg.ServiceNames() {
		errs = append(errs, moves[name].DiscardDestination(ctx), moves[name].Unfence(ctx))
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return dir.AbortMove(ctx, tenantID)
}

func planMoves(cfg Config, tenantID uuid.UUID, from, to string, o MoveOptions) (map[string]pgxx.Move, error) {
	moves := map[string]pgxx.Move{}
	for _, name := range cfg.ServiceNames() {
		src, err := cfg.Endpoint(name, from)
		if err != nil {
			return nil, err
		}
		dst, err := cfg.Endpoint(name, to)
		if err != nil {
			return nil, err
		}
		schemas, err := cfg.Schemas(name)
		if err != nil {
			return nil, err
		}
		moves[name] = pgxx.Move{TenantID: tenantID, From: src, To: dst, Schemas: schemas, Log: o.Log.With("service", name), FenceWait: o.FenceWait}
	}
	return moves, nil
}
