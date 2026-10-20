// Package api implements BillingService (tenant reads, app pool + RLS), SandboxService and BillingAdminService
// (system pool).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	billingv1 "github.com/0furkancolak/quotify/gen/go/quotify/billing/v1"
	"github.com/0furkancolak/quotify/pkg/period"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/dlq"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/runs"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/saga"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store"
	"github.com/0furkancolak/quotify/services/billing-svc/internal/store/sqlc"
)

// PDFSigner presigns downloads of invoice PDFs (pkg/blob.Store).
type PDFSigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// PDFLinkTTL is how long a presigned PDF link works.
const PDFLinkTTL = 5 * time.Minute

// Server implements the billing services.
type Server struct {
	billingv1.UnimplementedBillingServiceServer
	billingv1.UnimplementedSandboxServiceServer
	billingv1.UnimplementedBillingAdminServiceServer
	billingv1.UnimplementedHostedInvoiceServiceServer

	app    *pgxpool.Pool
	system *pgxpool.Pool
	runs   *runs.Manager
	eng    *saga.Engine
	pdfs   PDFSigner
	dlq    *dlq.Admin
}

// WithDeadLetters enables the dead-letter operations of BillingAdminService.
func (s *Server) WithDeadLetters(a *dlq.Admin) *Server {
	s.dlq = a
	return s
}

// New returns the API. app is the billing_app pool, system the billing_system pool.
func New(app, system *pgxpool.Pool, mgr *runs.Manager, eng *saga.Engine) *Server {
	return &Server{app: app, system: system, runs: mgr, eng: eng}
}

// WithPDFs enables GetInvoicePdfUrl.
func (s *Server) WithPDFs(p PDFSigner) *Server {
	s.pdfs = p
	return s
}

// GetInvoicePdfUrl implements billingv1.BillingServiceServer.
func (s *Server) GetInvoicePdfUrl(ctx context.Context, req *billingv1.GetInvoicePdfUrlRequest) (*billingv1.GetInvoicePdfUrlResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	if s.pdfs == nil {
		return nil, status.Error(codes.Unimplemented, "object storage is not configured")
	}
	var key string
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		k, err := sqlc.New(tx).GetInvoicePDFKey(ctx, id)
		if err != nil {
			return err
		}
		if k != nil {
			key = *k
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if key == "" {
		return nil, status.Error(codes.FailedPrecondition, "the invoice PDF is not rendered yet")
	}
	url, err := s.pdfs.PresignGet(ctx, key, PDFLinkTTL)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "could not sign the download link")
	}
	return &billingv1.GetInvoicePdfUrlResponse{Url: url, ExpireTime: timestamppb.New(time.Now().Add(PDFLinkTTL))}, nil
}

func invalid(msg string) error { return status.Error(codes.InvalidArgument, msg) }

func parseUUID(field, v string) (uuid.UUID, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return uuid.Nil, invalid(field + " must be a UUID")
	}
	return id, nil
}

func invoiceProto(r sqlc.GetInvoiceRow) *billingv1.Invoice {
	inv := &billingv1.Invoice{
		Id: r.ID.String(), CustomerId: r.CustomerID.String(), CustomerExternalId: r.CustomerExternalID, PeriodId: r.PeriodID, Kind: r.Kind, Number: r.Number,
		PlanVersionId: r.PlanVersionID.String(), Currency: r.Currency, SubtotalMinor: r.SubtotalMinor, TotalMinor: r.TotalMinor, Status: r.Status,
		SagaState: r.SagaState, PdfObjectKey: r.PdfObjectKey, CreateTime: timestamppb.New(r.CreatedAt),
	}
	if r.FinalizedAt != nil {
		inv.FinalizeTime = timestamppb.New(*r.FinalizedAt)
	}
	return inv
}

func lineProto(r sqlc.ListInvoiceLinesRow) *billingv1.InvoiceLine {
	l := &billingv1.InvoiceLine{LineNo: r.LineNo, Kind: r.Kind, MeterCode: r.MeterCode, OriginPeriodId: r.OriginPeriodID, AmountMinor: r.AmountMinor, Description: r.Description}
	if r.ServiceFrom != nil {
		l.ServiceFrom = timestamppb.New(*r.ServiceFrom)
	}
	if r.ServiceTo != nil {
		l.ServiceTo = timestamppb.New(*r.ServiceTo)
	}
	if r.Quantity != "" && r.QuantityScale != nil {
		var units int64
		if err := json.Unmarshal([]byte(r.Quantity), &units); err == nil {
			l.Quantity = quantity.FormatUnits(units, int(*r.QuantityScale))
		}
	}
	return l
}

// loadInvoice reads an invoice with its lines in tx (tenant-scoped, or the system's with the invoice id already known).
func loadInvoice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*billingv1.Invoice, error) {
	q := sqlc.New(tx)
	row, err := q.GetInvoice(ctx, id)
	if err != nil {
		return nil, err
	}
	inv := invoiceProto(row)
	lines, err := q.ListInvoiceLines(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		inv.Lines = append(inv.Lines, lineProto(l))
	}
	return inv, nil
}

// GetInvoice implements billingv1.BillingServiceServer.
func (s *Server) GetInvoice(ctx context.Context, req *billingv1.GetInvoiceRequest) (*billingv1.GetInvoiceResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	var inv *billingv1.Invoice
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		inv, err = loadInvoice(ctx, tx, id)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "invoice not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.GetInvoiceResponse{Invoice: inv}, nil
}

// ListInvoices implements billingv1.BillingServiceServer.
func (s *Server) ListInvoices(ctx context.Context, req *billingv1.ListInvoicesRequest) (*billingv1.ListInvoicesResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	size := int(req.GetPageSize())
	if size <= 0 {
		size = 100
	}
	if size > 500 {
		return nil, invalid("page_size must be 1..500")
	}
	var customer *uuid.UUID
	if c := req.GetCustomerId(); c != "" {
		id, err := parseUUID("customer_id", c)
		if err != nil {
			return nil, err
		}
		customer = &id
	}
	after := uuid.Nil
	if t := req.GetPageToken(); t != "" {
		if after, err = uuid.Parse(t); err != nil {
			return nil, invalid("page_token is invalid")
		}
	}
	var rows []sqlc.ListInvoicesRow
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		var err error
		rows, err = sqlc.New(tx).ListInvoices(ctx, sqlc.ListInvoicesParams{
			CustomerID: customer, PeriodID: store.NilIfEmpty(req.GetPeriodId()), Status: store.NilIfEmpty(req.GetStatus()),
			After: after, PageSize: int32(size + 1), //nolint:gosec // at most 501
		})
		return err
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	resp := &billingv1.ListInvoicesResponse{}
	if len(rows) > size {
		resp.NextPageToken = rows[size-1].ID.String()
		rows = rows[:size]
	}
	for _, r := range rows {
		resp.Invoices = append(resp.Invoices, invoiceProto(sqlc.GetInvoiceRow(r)))
	}
	return resp, nil
}

func runProto(r *runs.Run, invoices int) *billingv1.BillingRun {
	out := &billingv1.BillingRun{
		TenantId: r.TenantID.String(), PeriodId: r.PeriodID, CutoffTime: timestamppb.New(r.CutoffAt), InvoiceCount: int32(invoices), //nolint:gosec // small
		LastError: r.LastError,
	}
	switch r.Status {
	case runs.Scheduled:
		out.Status = billingv1.RunStatus_RUN_STATUS_SCHEDULED
	case runs.WaitingWatermark:
		out.Status = billingv1.RunStatus_RUN_STATUS_WAITING_WATERMARK
	case runs.Rating:
		out.Status = billingv1.RunStatus_RUN_STATUS_RATING
	case runs.Invoicing:
		out.Status = billingv1.RunStatus_RUN_STATUS_INVOICING
	case runs.Completed:
		out.Status = billingv1.RunStatus_RUN_STATUS_COMPLETED
	case runs.Failed:
		out.Status = billingv1.RunStatus_RUN_STATUS_FAILED
	}
	if r.Generation != nil {
		out.Generation = *r.Generation
	}
	if r.StartedAt != nil {
		out.StartTime = timestamppb.New(*r.StartedAt)
	}
	if r.CompletedAt != nil {
		out.CompleteTime = timestamppb.New(*r.CompletedAt)
	}
	return out
}

// ClosePeriodNow implements billingv1.SandboxServiceServer.
func (s *Server) ClosePeriodNow(ctx context.Context, req *billingv1.ClosePeriodNowRequest) (*billingv1.ClosePeriodNowResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	if ti.Environment != tenant.EnvSandbox {
		return nil, status.Error(codes.FailedPrecondition, "ClosePeriodNow is only available to sandbox tenants")
	}
	pid := req.GetPeriodId()
	if pid == "" {
		loc := time.UTC
		if ti.Timezone != "" {
			if l, err := time.LoadLocation(ti.Timezone); err == nil {
				loc = l
			}
		}
		pid = period.PeriodOf(s.runs.Now(), loc)
	}
	if _, _, err := period.Bounds(pid, time.UTC); err != nil {
		return nil, invalid("period_id must look like 2026-11")
	}
	run, err := s.runs.ClosePeriodNow(ctx, ti, pid)
	if err != nil {
		return nil, err
	}
	return &billingv1.ClosePeriodNowResponse{Run: runProto(run, run.InvoiceCount)}, nil
}

// ClosePeriod implements billingv1.BillingAdminServiceServer.
func (s *Server) ClosePeriod(ctx context.Context, req *billingv1.ClosePeriodRequest) (*billingv1.ClosePeriodResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := parseUUID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	run, err := s.runs.ClosePeriod(ctx, tid, req.GetPeriodId())
	if err != nil {
		return nil, err
	}
	if run.Status == runs.Failed {
		if err := s.runs.RetryRun(ctx, tid, req.GetPeriodId()); err != nil {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
	}
	if run, err = s.runs.Drive(ctx, tid, req.GetPeriodId()); err != nil {
		return nil, status.Error(codes.Unavailable, "run could not advance now; it will be retried: "+err.Error())
	}
	return &billingv1.ClosePeriodResponse{Run: runProto(run, run.InvoiceCount)}, nil
}

// GetBillingRun implements billingv1.BillingAdminServiceServer.
func (s *Server) GetBillingRun(ctx context.Context, req *billingv1.GetBillingRunRequest) (*billingv1.GetBillingRunResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := parseUUID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	run, err := s.runs.GetRun(ctx, tid, req.GetPeriodId())
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "no billing run for that period")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.GetBillingRunResponse{Run: runProto(run, run.InvoiceCount)}, nil
}

func (s *Server) sagaOf(ctx context.Context, tx pgx.Tx, tid, invoiceID uuid.UUID) (uuid.UUID, error) {
	id, err := sqlc.New(tx).GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tid, InvoiceID: invoiceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, status.Error(codes.NotFound, "invoice has no saga (it is not finalized)")
	}
	return id, err
}

func (s *Server) sagaProto(ctx context.Context, tid, sagaID uuid.UUID) (*billingv1.InvoiceSaga, error) {
	row, err := sqlc.New(s.system).GetSaga(ctx, sqlc.GetSagaParams{TenantID: tid, ID: sagaID})
	if err != nil {
		return nil, err
	}
	out := &billingv1.InvoiceSaga{Id: row.ID.String(), InvoiceId: row.InvoiceID.String(), State: row.State, Version: row.Version, Attempt: row.Attempt, DunningAttempt: row.DunningAttempt}
	if row.LastError != nil {
		out.LastError = *row.LastError
	}
	return out, nil
}

func (s *Server) drive(ctx context.Context, tidStr, invStr string, ev saga.Event, cause string) (*billingv1.InvoiceSaga, error) {
	tid, err := parseUUID("tenant_id", tidStr)
	if err != nil {
		return nil, err
	}
	invID, err := parseUUID("invoice_id", invStr)
	if err != nil {
		return nil, err
	}
	var sagaID uuid.UUID
	var applied bool
	var state saga.State
	err = pgxx.InTx(ctx, s.system, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		if sagaID, err = s.sagaOf(ctx, tx, tid, invID); err != nil {
			return err
		}
		out, err := s.eng.Handle(ctx, tx, tid, sagaID, ev, cause, nil)
		applied, state = out.Applied, out.From
		return err
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		return nil, status.Error(codes.Internal, "internal error")
	}
	if !applied {
		return nil, status.Errorf(codes.FailedPrecondition, "%s is not possible while the invoice saga is in %s", ev, state)
	}
	return s.sagaProto(ctx, tid, sagaID)
}

// RetryInvoiceSaga implements billingv1.BillingAdminServiceServer.
func (s *Server) RetryInvoiceSaga(ctx context.Context, req *billingv1.RetryInvoiceSagaRequest) (*billingv1.RetryInvoiceSagaResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	saga, err := s.drive(ctx, req.GetTenantId(), req.GetInvoiceId(), saga.EvRetry, "operator:retry")
	if err != nil {
		return nil, err
	}
	return &billingv1.RetryInvoiceSagaResponse{Saga: saga}, nil
}

// VoidInvoice implements billingv1.BillingAdminServiceServer.
func (s *Server) VoidInvoice(ctx context.Context, req *billingv1.VoidInvoiceRequest) (*billingv1.VoidInvoiceResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	out, err := s.drive(ctx, req.GetTenantId(), req.GetInvoiceId(), saga.EvVoid, "operator:void")
	if err != nil {
		return nil, err
	}
	return &billingv1.VoidInvoiceResponse{Saga: out}, nil
}

func reratingProto(j *runs.ReratingJob) *billingv1.ReratingJob {
	st := billingv1.ReratingStatus_RERATING_STATUS_UNSPECIFIED
	switch j.Status {
	case runs.ReratingRunning:
		st = billingv1.ReratingStatus_RERATING_STATUS_RUNNING
	case runs.ReratingCompleted:
		st = billingv1.ReratingStatus_RERATING_STATUS_COMPLETED
	case runs.ReratingFailed:
		st = billingv1.ReratingStatus_RERATING_STATUS_FAILED
	}
	out := &billingv1.ReratingJob{
		Id: j.ID.String(), TenantId: j.TenantID.String(), PeriodId: j.PeriodID, PlanVersionId: j.PlanVersionID.String(), Generation: j.Generation,
		Status: st, CustomersRated: int32(j.CustomersRated), Corrections: int32(j.Corrections), Credits: int32(j.Credits), //nolint:gosec // counts of one tenant's customers
		CorrectionTotalMinor: j.CorrectionMinor, CreditTotalMinor: j.CreditMinor, LastError: j.LastError, CreateTime: timestamppb.New(j.CreatedAt),
	}
	if j.CompletedAt != nil {
		out.CompleteTime = timestamppb.New(*j.CompletedAt)
	}
	return out
}

// StartRerating implements billingv1.BillingAdminServiceServer.
func (s *Server) StartRerating(ctx context.Context, req *billingv1.StartReratingRequest) (*billingv1.StartReratingResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := parseUUID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	pv, err := parseUUID("plan_version_id", req.GetPlanVersionId())
	if err != nil {
		return nil, err
	}
	job, created, err := s.runs.StartRerating(ctx, tid, req.GetPeriodId(), pv)
	switch {
	case errors.Is(err, runs.ErrNoRun):
		return nil, status.Error(codes.NotFound, "no billing run for that period")
	case errors.Is(err, runs.ErrRunNotCompleted):
		return nil, status.Error(codes.FailedPrecondition, "the billing run of that period is not COMPLETED")
	case errors.Is(err, runs.ErrUnknownPlanVer):
		return nil, status.Error(codes.InvalidArgument, "plan_version_id: no such plan version")
	case err != nil && job != nil:
		// The job exists and will be finished by the scheduler; say so instead of failing the request.
		return &billingv1.StartReratingResponse{Job: reratingProto(job), Created: created}, nil
	case err != nil:
		slog.ErrorContext(ctx, "start rerating", "error", err)
		return nil, status.Error(codes.Unavailable, "re-rating could not start; retry")
	}
	return &billingv1.StartReratingResponse{Job: reratingProto(job), Created: created}, nil
}

// GetReratingJob implements billingv1.BillingAdminServiceServer.
func (s *Server) GetReratingJob(ctx context.Context, req *billingv1.GetReratingJobRequest) (*billingv1.GetReratingJobResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := parseUUID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	id, err := parseUUID("job_id", req.GetJobId())
	if err != nil {
		return nil, err
	}
	job, err := s.runs.GetReratingJob(ctx, tid, id)
	if errors.Is(err, runs.ErrReratingNotFound) {
		return nil, status.Error(codes.NotFound, "no such re-rating job")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &billingv1.GetReratingJobResponse{Job: reratingProto(job)}, nil
}

// GetInvoiceSaga implements billingv1.BillingAdminServiceServer.
func (s *Server) GetInvoiceSaga(ctx context.Context, req *billingv1.GetInvoiceSagaRequest) (*billingv1.GetInvoiceSagaResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	tid, err := parseUUID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	inv, err := parseUUID("invoice_id", req.GetInvoiceId())
	if err != nil {
		return nil, err
	}
	q := sqlc.New(s.system)
	sagaID, err := q.GetSagaIDByInvoice(ctx, sqlc.GetSagaIDByInvoiceParams{TenantID: tid, InvoiceID: inv})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, status.Error(codes.NotFound, "invoice has no saga (it is not finalized)")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	saga, err := s.sagaProto(ctx, tid, sagaID)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	events, err := q.ListSagaEvents(ctx, sqlc.ListSagaEventsParams{TenantID: tid, SagaID: sagaID})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	resp := &billingv1.GetInvoiceSagaResponse{Saga: saga}
	for _, e := range events {
		ev := &billingv1.SagaEvent{Seq: e.Seq, FromState: e.FromState, ToState: e.ToState, Cause: e.Cause, At: timestamppb.New(e.At)}
		if e.CommandID != nil {
			ev.CommandId = e.CommandID.String()
		}
		resp.Events = append(resp.Events, ev)
	}
	return resp, nil
}

func dlqErr(err error) error {
	switch {
	case errors.Is(err, dlq.ErrUnknownQueue):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		slog.Error("dead letters", "error", err)
		return status.Error(codes.Unavailable, "the broker could not be reached; retry")
	}
}

// ListDeadLetters implements billingv1.BillingAdminServiceServer.
func (s *Server) ListDeadLetters(ctx context.Context, req *billingv1.ListDeadLettersRequest) (*billingv1.ListDeadLettersResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	if s.dlq == nil {
		return nil, status.Error(codes.Unimplemented, "dead-letter operations are not configured")
	}
	depths, msgs, err := s.dlq.List(ctx, req.GetQueue(), int(req.GetLimit()))
	if err != nil {
		return nil, dlqErr(err)
	}
	resp := &billingv1.ListDeadLettersResponse{}
	for _, d := range depths {
		resp.Queues = append(resp.Queues, &billingv1.DeadLetterQueue{Queue: d.Queue, Messages: d.Messages})
	}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, &billingv1.DeadLetter{MessageId: m.ID, DeliveryCount: m.Deliveries, Reason: m.Reason, BodyBytes: int32(m.BodyBytes)}) //nolint:gosec // message sizes are far below 2 GiB
	}
	return resp, nil
}

// RedriveDeadLetters implements billingv1.BillingAdminServiceServer.
func (s *Server) RedriveDeadLetters(ctx context.Context, req *billingv1.RedriveDeadLettersRequest) (*billingv1.RedriveDeadLettersResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	if s.dlq == nil {
		return nil, status.Error(codes.Unimplemented, "dead-letter operations are not configured")
	}
	n, err := s.dlq.Redrive(ctx, req.GetQueue(), int(req.GetLimit()))
	if err != nil {
		return nil, dlqErr(err)
	}
	return &billingv1.RedriveDeadLettersResponse{Redriven: int32(n)}, nil //nolint:gosec // at most 1000
}
