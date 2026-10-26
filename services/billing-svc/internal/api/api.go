// Package api assembles BillingService (tenant reads, app pool + RLS), SandboxService, BillingAdminService (system pool) and
// HostedInvoiceService from the modules of billing-svc (ADR 0020). Each module owns its handlers, commands and queries; this package
// only wires them together so that one value can be registered for the four gRPC services.
package api

import (
	"github.com/jackc/pgx/v5/pgxpool"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/accounting"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns"
	runcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/billingruns/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes"
	creditnotecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/creditnotes/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/credits"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/deadletters"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dlq"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/dunning"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices"
	einvoicecmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/einvoices/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted"
	hostedcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/hosted/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices"
	invoicequery "github.com/0furkancolak/payrudder/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/payments"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rating/inputs"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/reports"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating"
	reratingcmd "github.com/0furkancolak/payrudder/services/billing-svc/internal/rerating/command"
	"github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas"
	sagacommand "github.com/0furkancolak/payrudder/services/billing-svc/internal/sagas/command"
)

// PDFSigner presigns downloads of invoice PDFs (pkg/blob.Store).
type PDFSigner = invoicequery.PDFSigner

// The aliases give the embedded module handlers distinct field names.
type (
	invoiceHandler    = invoices.Handler
	creditNoteHandler = creditnotes.Handler
	dunningHandler    = dunning.Handler
	creditsHandler    = credits.Handler
	accountingHandler = accounting.Handler
	reportHandler     = reports.Handler
	hostedHandler     = hosted.Handler
	runHandler        = billingruns.Handler
	sagaHandler       = sagas.Handler
	reratingHandler   = rerating.Handler
	deadLetterHandler = deadletters.Handler
	einvoiceHandler   = einvoices.Handler
	paymentHandler    = payments.Handler
)

// unimplemented answers UNIMPLEMENTED for the methods no module serves. It is embedded one level deeper than the module handlers so
// that their methods win.
type unimplemented struct {
	billingv1.UnimplementedBillingServiceServer
	billingv1.UnimplementedSandboxServiceServer
	billingv1.UnimplementedBillingAdminServiceServer
	billingv1.UnimplementedHostedInvoiceServiceServer
}

// Server implements the billing services by promoting the methods of the module handlers.
type Server struct {
	unimplemented
	*invoiceHandler
	*creditNoteHandler
	*dunningHandler
	*creditsHandler
	*accountingHandler
	*reportHandler
	*hostedHandler
	*runHandler
	*sagaHandler
	*reratingHandler
	*deadLetterHandler
	*einvoiceHandler
	*paymentHandler
}

// Deps are the modules the API serves and the pools they read and write through.
type Deps struct {
	App, System *pgxpool.Pool // billing_app (RLS), billing_system
	Runs        *runcmd.Manager
	Engine      *sagacommand.Engine
	CreditNotes *creditnotecmd.Commands
	Rerating    *reratingcmd.Commands
	Inputs      *inputs.Inputs
	Hosted      *hostedcmd.Backend
}

// New returns the API.
func New(d Deps) *Server {
	app, system, mgr, eng := d.App, d.System, d.Runs, d.Engine
	einv := &einvoicecmd.Commands{Pool: system, Parties: d.Inputs}
	return &Server{
		invoiceHandler:    invoices.New(app),
		creditNoteHandler: creditnotes.New(app, system, d.CreditNotes),
		dunningHandler:    dunning.New(app, eng),
		creditsHandler:    credits.New(app),
		accountingHandler: accounting.New(app, system),
		reportHandler:     reports.New(app),
		hostedHandler:     hosted.New(app, system, d.Hosted, eng),
		runHandler:        billingruns.New(mgr),
		sagaHandler:       sagas.New(system, eng),
		reratingHandler:   rerating.New(d.Rerating, system),
		deadLetterHandler: deadletters.New(),
		einvoiceHandler:   einvoices.New(app, einv),
		paymentHandler:    payments.New(system, eng),
	}
}

// WithDeadLetters enables the dead-letter operations of BillingAdminService.
func (s *Server) WithDeadLetters(a *dlq.Admin) *Server {
	s.deadLetterHandler.WithDeadLetters(a)
	return s
}

// WithDemo allows the mock payment on hosted pages for live tenants too (PAYRUDDER_DEMO).
func (s *Server) WithDemo(demo bool) *Server {
	s.hostedHandler.WithDemo(demo)
	return s
}

// WithPDFs enables GetInvoicePdfUrl and the PDF link of the hosted invoice.
func (s *Server) WithPDFs(p PDFSigner) *Server {
	s.invoiceHandler.WithPDFs(p)
	s.hostedHandler.WithPDFs(p)
	s.WithFiles(p)
	return s
}
