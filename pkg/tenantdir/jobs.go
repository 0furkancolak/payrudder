package tenantdir

import (
	"fmt"
	"sort"
	"strings"
)

// Job is a piece of a service that must work for a tenant in a cell before the directory lets one be placed there (ADR 0018): its tenant schema, and every
// job that today scans the shared tables of a service on the system role.
type Job struct {
	Service string
	Name    string
	// Converted is true when the job runs per placed tenant, in the tenant's schema, and its tests run in both modes.
	Converted bool
	// Where is the entry point, for whoever converts it.
	Where string
}

func (j Job) String() string { return j.Service + ": " + j.Name }

// Jobs is the inventory of phase 28 stage 3, made by reading every main.go and worker package. A job is added here when it is written, and flipped to
// Converted in the commit that converts it and its tests, so the list of what is left is always the list the directory refuses a placement for. A service with
// no tenant table in Postgres (notifier, limits-svc, gateway, invoice-worker, analytics-svc) has no entry: nothing of it is in a cell.
var Jobs = []Job{
	{"ingest-svc", "tenant schema", true, "services/ingest-svc/migrations/tenant"},
	{"ingest-svc", "event write path", true, "internal/events/command/write.go"},
	{"ingest-svc", "outbox relay", true, "pkg/outbox"},
	{"ingest-svc", "outbox monitor (depth, age)", true, "internal/outbox/monitor"},
	{"ingest-svc", "outbox watermark", true, "internal/outbox/query"},
	{"ingest-svc", "idempotency partition maintenance", true, "internal/maintenance"},
	{"ingest-svc", "sandbox reset", true, "internal/sandboxreset"},

	{"plans-svc", "tenant schema", false, "migrations"},
	{"plans-svc", "outbox relay", false, "cmd/plans-svc newRelay"},
	{"plans-svc", "webhook dispatcher", false, "internal/webhooks/worker"},
	{"plans-svc", "affiliate commission approver", false, "internal/affiliates/command/commissions.go"},
	{"plans-svc", "trial reminder", false, "internal/trialnotices/command"},
	{"plans-svc", "sandbox reset runner and retention sweep", false, "internal/sandbox"},
	{"plans-svc", "SaaS account sweep", false, "internal/saas/command"},

	{"billing-svc", "tenant schema", false, "migrations"},
	{"billing-svc", "outbox relay", false, "cmd/billing-svc"},
	{"billing-svc", "billing scheduler tick", false, "internal/billingruns/command/scheduler.go"},
	{"billing-svc", "billing run gauges", false, "internal/billingruns/command/gauges.go"},
	{"billing-svc", "fee run", false, "internal/fees/command"},
	{"billing-svc", "credit note refund sweep", false, "internal/creditnotes/command/refund.go"},
	{"billing-svc", "payment link expiry", false, "internal/hosted/command/expiry.go"},
	{"billing-svc", "promotion reversals", false, "internal/hosted/command/promotions.go"},
	{"billing-svc", "re-rating resume", false, "internal/rerating/command/start.go"},
	{"billing-svc", "tenant event flusher", false, "internal/tenantevents/relay.go"},
	{"billing-svc", "saga sweeper", false, "internal/sagas/command/sweep.go"},
	{"billing-svc", "credit expiry", false, "internal/credits/command"},
	{"billing-svc", "e-invoice sweep", false, "internal/einvoices/command"},
	{"billing-svc", "worker reply consumer", false, "internal/replies"},

	{"aggregator", "tenant schema", true, "migrations"},
	{"aggregator", "usage consumer", true, "internal/apply"},
	{"aggregator", "maintenance", true, "internal/maintenance"},
	{"aggregator", "replay manager", true, "internal/replay"},
	{"aggregator", "sandbox purge", true, "internal/sandboxreset"},

	{"assets-svc", "tenant schema", true, "migrations"},
	{"assets-svc", "upload session sweeper", true, "internal/background"},
	{"assets-svc", "upload verifier", true, "internal/background"},
	{"assets-svc", "orphan reconciler", true, "internal/background"},

	{"payments-worker", "tenant schema", false, "migrations"},
	{"payments-worker", "command worker", false, "cmd/payments-worker"},
	{"payments-worker", "provider webhook server", false, "internal/webhook"},
	{"payments-worker", "transfers and sandbox reset", false, "internal/store"},
}

// Unconverted lists the jobs that are not converted yet, in the order of Jobs.
func Unconverted() []Job { return UnconvertedOf(Jobs) }

// UnconvertedOf is Unconverted over any inventory (tests).
func UnconvertedOf(jobs []Job) []Job {
	var out []Job
	for _, j := range jobs {
		if !j.Converted {
			out = append(out, j)
		}
	}
	return out
}

// CellsReady is nil when every job is converted, and otherwise an error naming what is left: the check behind the directory's refusal to place a tenant in a cell.
func CellsReady() error { return ReadyOf(Jobs) }

// ReadyOf is CellsReady over any inventory (tests).
func ReadyOf(jobs []Job) error {
	left := UnconvertedOf(jobs)
	if len(left) == 0 {
		return nil
	}
	per := map[string]int{}
	for _, j := range left {
		per[j.Service]++
	}
	services := make([]string, 0, len(per))
	for s, n := range per {
		services = append(services, fmt.Sprintf("%s (%d)", s, n))
	}
	sort.Strings(services)
	return fmt.Errorf("%d of %d jobs are not converted to run per tenant schema: %s", len(left), len(jobs), strings.Join(services, ", "))
}
