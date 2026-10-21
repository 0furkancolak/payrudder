// Package api assembles BillingService (tenant reads, app pool + RLS), SandboxService, BillingAdminService (system pool) and
// HostedInvoiceService from the modules of billing-svc (ADR 0020). Each module owns its handlers, commands and queries; this package
// only wires them together so that one value can be registered for the four gRPC services.
package api

import (
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/jackc/pgx/v5/pgxpool"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/billingruns"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/creditnotes"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/credits"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/deadletters"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/dlq"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/dunning"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/hosted"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/invoices"
	invoicequery "github.com/0furkancolak/quotify/services/billing-svc/internal/invoices/query"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/reports"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/rerating"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/sagas"
)

// PDFSigner presigns downloads of invoice PDFs (pkg/blob.Store).
type PDFSigner = invoicequery.PDFSigner

// The aliases give the embedded module handlers distinct field names.
type (
	invoiceHandler    = invoices.Handler
	creditNoteHandler = creditnotes.Handler
	dunningHandler    = dunning.Handler
	creditsHandler    = credits.Handler
	reportHandler     = reports.Handler
	hostedHandler     = hosted.Handler
	runHandler        = billingruns.Handler
	sagaHandler       = sagas.Handler
	reratingHandler   = rerating.Handler
	deadLetterHandler = deadletters.Handler
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
	*reportHandler
	*hostedHandler
	*runHandler
	*sagaHandler
	*reratingHandler
	*deadLetterHandler
}

// New returns the API. app is the billing_app pool, system the billing_system pool.
func New(app, system *pgxpool.Pool, mgr *runs.Manager, eng *saga.Engine) *Server {
	return &Server{
		invoiceHandler:    invoices.New(app),
		creditNoteHandler: creditnotes.New(app, system, mgr),
		dunningHandler:    dunning.New(app),
		creditsHandler:    credits.New(app),
		reportHandler:     reports.New(app),
		hostedHandler:     hosted.New(app, system),
		runHandler:        billingruns.New(mgr),
		sagaHandler:       sagas.New(system, eng),
		reratingHandler:   rerating.New(mgr),
		deadLetterHandler: deadletters.New(),
	}
}

// WithDeadLetters enables the dead-letter operations of BillingAdminService.
func (s *Server) WithDeadLetters(a *dlq.Admin) *Server {
	s.deadLetterHandler.WithDeadLetters(a)
	return s
}

// WithPDFs enables GetInvoicePdfUrl and the PDF link of the hosted invoice.
func (s *Server) WithPDFs(p PDFSigner) *Server {
	s.invoiceHandler.WithPDFs(p)
	s.hostedHandler.WithPDFs(p)
	return s
}
