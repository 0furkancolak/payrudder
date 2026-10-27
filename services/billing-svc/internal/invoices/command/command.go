// Package command writes invoices: it inserts the drafts the billing runs, the fee billing, the re-rating jobs and the payment links make, applies credit
// balances to them, and finalizes a draft (a gap-free number, an open invoice, a saga). Every other module that makes an invoice calls it.
package command

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/store/sqlc"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantdb"
)

// Config is what the commands run on.
type Config struct {
	DB     *tenantdb.DB // billing_system
	Engine *sagacommand.Engine
	Now    func() time.Time
}

// Commands are the invoice commands.
type Commands struct {
	cfg Config
	// q only supplies WithTx: the statements run in the transactions DB opens.
	q       *sqlc.Queries
	drafted metric.Int64Counter
}

// New returns the commands and registers the metric of drafted invoices; a missing clock is the wall clock.
func New(cfg Config) (*Commands, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	c := &Commands{cfg: cfg, q: sqlc.New(cfg.DB.Pool())}
	var err error
	c.drafted, err = otel.Meter("github.com/0furkancolak/payrudder/services/billing-svc/invoices").Int64Counter("billing_invoices_drafted", metric.WithDescription("Draft invoices created"))
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Drafted counts n draft invoices that were created.
func (c *Commands) Drafted(ctx context.Context, n int64, opts ...metric.AddOption) {
	c.drafted.Add(ctx, n, opts...)
}
