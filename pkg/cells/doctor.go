package cells

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/tenantdir"
)

// Level is how much a finding matters.
type Level string

const (
	// Error is a state in which a tenant cannot be served or data is at risk.
	Error Level = "error"
	// Warn is a state that is wrong or leftover and does not stop a tenant being served.
	Warn Level = "warn"
	// Info is for what an operator should know.
	Info Level = "info"
)

// Finding is one thing the doctor noticed.
type Finding struct {
	Level   Level
	Service string
	Cell    string
	Tenant  uuid.UUID
	Message string
}

func (f Finding) String() string {
	where := f.Cell
	if f.Service != "" {
		where = f.Service + "@" + f.Cell
	}
	t := ""
	if f.Tenant != uuid.Nil {
		t = " tenant " + f.Tenant.String()
	}
	return fmt.Sprintf("%-5s %s%s: %s", f.Level, where, t, f.Message)
}

// DoctorOptions tune Doctor.
type DoctorOptions struct {
	// RetiredAfter is how old a schema set aside by a move is before the doctor warns it should be dropped (default 14 days).
	RetiredAfter time.Duration
	Now          func() time.Time
}

// Doctor compares the directory with every cell database of the config, service by service, and reports what disagrees:
//
//   - a cell that cannot be reached, or lacks the shared schema
//   - a tenant the directory places in a cell whose schema is missing there (error), or that is stuck in the moving state (warn)
//   - a tenant schema no placement points at: a leftover of a move, or of a tenant placed elsewhere (warn)
//   - a tenant schema that is behind the tenant migrations (warn: run cells migrate)
//   - a schema set aside by a move that has been there longer than RetiredAfter (warn) or not (info)
//   - the platform's job inventory: how many jobs still keep the directory from placing a tenant in a cell (info)
//
// It changes nothing. The findings are in a stable order.
func Doctor(ctx context.Context, cfg Config, dir Directory, o DoctorOptions) ([]Finding, error) {
	if o.RetiredAfter <= 0 {
		o.RetiredAfter = 14 * 24 * time.Hour
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	var out []Finding
	add := func(l Level, service, cell string, tenant uuid.UUID, format string, args ...any) {
		out = append(out, Finding{Level: l, Service: service, Cell: cell, Tenant: tenant, Message: fmt.Sprintf(format, args...)})
	}

	placed, err := dir.Placed(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("cells: list the directory: %w", err)
	}
	byCell := map[string][]tenantdir.Placement{}
	placedIn := map[uuid.UUID]tenantdir.Placement{}
	for _, p := range placed {
		byCell[p.Cell] = append(byCell[p.Cell], p)
		placedIn[p.TenantID] = p
		if _, ok := cfg.Cells[p.Cell]; !ok {
			add(Error, "", p.Cell, p.TenantID, "the directory places this tenant in a cell the config does not know")
		}
		if !p.Serving() {
			add(Warn, "", p.Cell, p.TenantID, "the tenant is %s: every request and job for it is refused. If no move is running, abort it (payrudderctl cells abort)", p.Status)
		}
	}

	for _, service := range cfg.ServiceNames() {
		schemas, err := cfg.Schemas(service)
		if err != nil {
			add(Error, service, "", uuid.Nil, "%v", err)
			continue
		}
		latest, err := pgxx.LatestTenantVersion(schemas.Migrations)
		if err != nil {
			add(Error, service, "", uuid.Nil, "tenant migrations: %v", err)
			continue
		}
		for _, cell := range cfg.CellNames() {
			out = append(out, checkCell(ctx, cfg, service, cell, latest, byCell[cell], placedIn, dir, o)...)
		}
	}

	if err := tenantdir.CellsReady(); err != nil {
		add(Info, "", "", uuid.Nil, "the directory refuses placements in cells: %v", err)
	}
	slices.SortStableFunc(out, func(a, b Finding) int {
		if c := levelRank(a.Level) - levelRank(b.Level); c != 0 {
			return c
		}
		return 0
	})
	return out, nil
}

func levelRank(l Level) int {
	switch l {
	case Error:
		return 0
	case Warn:
		return 1
	}
	return 2
}

func checkCell(ctx context.Context, cfg Config, service, cell string, latest int64, placed []tenantdir.Placement, placedIn map[uuid.UUID]tenantdir.Placement, dir Directory, o DoctorOptions) []Finding {
	var out []Finding
	add := func(l Level, tenant uuid.UUID, format string, args ...any) {
		out = append(out, Finding{Level: l, Service: service, Cell: cell, Tenant: tenant, Message: fmt.Sprintf(format, args...)})
	}
	ep, err := cfg.Endpoint(service, cell)
	if err != nil {
		add(Error, uuid.Nil, "%v", err)
		return out
	}
	conn, err := pgx.Connect(ctx, ep.MigratorDSN)
	if err != nil {
		add(Error, uuid.Nil, "the database cannot be reached: %v", err)
		return out
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	var shared bool
	if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, pgxx.SharedSchema).Scan(&shared); err != nil {
		add(Error, uuid.Nil, "cannot look at the schemas: %v", err)
		return out
	}
	if !shared {
		add(Error, uuid.Nil, "the %s schema is missing (run payrudderctl cells migrate)", pgxx.SharedSchema)
	}

	have, err := pgxx.TenantSchemaIDs(ctx, conn)
	if err != nil {
		add(Error, uuid.Nil, "cannot list the tenant schemas: %v", err)
		return out
	}
	haveSet := map[uuid.UUID]bool{}
	for _, id := range have {
		haveSet[id] = true
	}
	wantSet := map[uuid.UUID]bool{}
	for _, p := range placed {
		wantSet[p.TenantID] = true
		if !haveSet[p.TenantID] {
			add(Error, p.TenantID, "placed here by the directory but %s does not exist", tenantdir.Schema(p.TenantID))
		}
	}
	for _, id := range have {
		if wantSet[id] {
			continue
		}
		where := "the directory does not know the tenant here"
		if p, ok := placedIn[id]; ok {
			where = fmt.Sprintf("the directory places it in cell %q", p.Cell)
		} else if cur, err := dir.Placement(ctx, id); err == nil {
			where = "the directory has it in the pooled database"
			if !cur.Pooled() {
				where = fmt.Sprintf("the directory places it in cell %q", cur.Cell)
			}
		}
		add(Warn, id, "schema %s is here but %s: a leftover of a move or of a placement that was undone", tenantdir.Schema(id), where)
	}
	for _, id := range have {
		v, ok, err := pgxx.TenantSchemaVersion(ctx, conn, id)
		switch {
		case err != nil:
			add(Error, id, "cannot read the migration version: %v", err)
		case !ok:
			add(Error, id, "the schema has no migration history: it was not built by the tenant migrations")
		case v < latest:
			add(Warn, id, "migrations are behind: at %d, latest is %d (run payrudderctl cells migrate)", v, latest)
		case v > latest:
			add(Warn, id, "migrations are ahead of this checkout: at %d, latest known is %d", v, latest)
		}
	}
	retired, err := pgxx.RetiredSchemas(ctx, conn)
	if err != nil {
		add(Error, uuid.Nil, "cannot list the schemas set aside by moves: %v", err)
	}
	for _, r := range retired {
		age := o.Now().Sub(r.At).Round(time.Hour)
		if age >= o.RetiredAfter {
			add(Warn, r.TenantID, "schema %s was set aside by a move %s ago: drop it once the move is trusted (payrudderctl cells retired --drop)", r.Name, age)
		} else {
			add(Info, r.TenantID, "schema %s was set aside by a move %s ago", r.Name, age)
		}
	}
	return out
}
