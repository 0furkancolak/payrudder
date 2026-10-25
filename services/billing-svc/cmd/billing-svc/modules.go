package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	aggregatorv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/aggregator/v1"
	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	creditnotecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/command"
	feecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/fees/command"
	hostedcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted/command"
	invoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	reratecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/command"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/tenantevents"
)

// modules are the command sides of the billing modules over one set of pools and backends. The scheduler (billing runs) and the API are built from them.
type modules struct {
	inputs       *inputs.Inputs
	invoices     *invoicecmd.Commands
	creditNotes  *creditnotecmd.Commands
	fees         *feecmd.Commands
	rerating     *reratecmd.Commands
	hosted       *hostedcmd.Backend
	tenantEvents *tenantevents.Relay
}

func newModules(cfg *Config, log *slog.Logger, system *pgxpool.Pool, eng *sagacommand.Engine, be *backends) (*modules, error) {
	in := &inputs.Inputs{
		Plans: plansv1.NewPlansServiceClient(be.plans), Admin: plansv1.NewAdminServiceClient(be.plans), Aggregator: aggregatorv1.NewUsageQueryServiceClient(be.aggregator),
	}
	invoices, err := invoicecmd.New(invoicecmd.Config{Pool: system, Engine: eng})
	if err != nil {
		return nil, err
	}
	return &modules{
		inputs: in, invoices: invoices,
		creditNotes:  creditnotecmd.New(creditnotecmd.Config{Pool: system, Engine: eng, Log: log}),
		fees:         feecmd.New(feecmd.Config{Pool: system, Inputs: in, Invoices: invoices, Log: log}),
		rerating:     reratecmd.New(reratecmd.Config{Pool: system, Inputs: in, Invoices: invoices, Log: log}),
		hosted:       hostedcmd.NewBackend(hostedcmd.BackendConfig{Pool: system, Inputs: in, Invoices: invoices, Engine: eng, Affiliates: plansv1.NewAffiliateServiceClient(be.plans), Log: log}),
		tenantEvents: tenantevents.NewRelay(system, plansv1.NewWebhookServiceClient(be.plans), log),
	}, nil
}
