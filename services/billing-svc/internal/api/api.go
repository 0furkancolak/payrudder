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
)

// PDFSigner presigns downloads of invoice PDFs (pkg/blob.Store).
type PDFSigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// PDFLinkTTL is how long a presigned PDF link works.
const PDFLinkTTL = 5 * time.Minute

// Server implements the three billing services.
type Server struct {
	billingv1.UnimplementedBillingServiceServer
	billingv1.UnimplementedSandboxServiceServer
	billingv1.UnimplementedBillingAdminServiceServer

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
		var k *string
		if err := tx.QueryRow(ctx, `SELECT pdf_object_key FROM invoices WHERE id = $1`, id).Scan(&k); err != nil {
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

const invoiceColumns = `i.id, i.customer_id, COALESCE(i.snapshot->>'customer_external_id', ''), i.period_id, i.kind, COALESCE(i.number, ''),
	i.plan_version_id, i.currency, i.subtotal_minor, i.total_minor, i.status, COALESCE(s.state, ''), COALESCE(i.pdf_object_key, ''),
	i.created_at, i.finalized_at`

func scanInvoice(row pgx.Row) (*billingv1.Invoice, error) {
	var inv billingv1.Invoice
	var created time.Time
	var finalized *time.Time
	var id, customer, pv uuid.UUID
	if err := row.Scan(&id, &customer, &inv.CustomerExternalId, &inv.PeriodId, &inv.Kind, &inv.Number, &pv, &inv.Currency,
		&inv.SubtotalMinor, &inv.TotalMinor, &inv.Status, &inv.SagaState, &inv.PdfObjectKey, &created, &finalized); err != nil {
		return nil, err
	}
	inv.Id, inv.CustomerId, inv.PlanVersionId = id.String(), customer.String(), pv.String()
	inv.CreateTime = timestamppb.New(created)
	if finalized != nil {
		inv.FinalizeTime = timestamppb.New(*finalized)
	}
	return &inv, nil
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
		inv, err = scanInvoice(tx.QueryRow(ctx, `SELECT `+invoiceColumns+` FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
			WHERE i.id = $1`, id))
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT line_no, kind, COALESCE(meter_code, ''), COALESCE(origin_period_id, ''), quantity::text, quantity_scale, amount_minor, description
			FROM invoice_lines WHERE invoice_id = $1 ORDER BY line_no`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l billingv1.InvoiceLine
			var qty *string
			var scale *int16
			if err := rows.Scan(&l.LineNo, &l.Kind, &l.MeterCode, &l.OriginPeriodId, &qty, &scale, &l.AmountMinor, &l.Description); err != nil {
				return err
			}
			if qty != nil && scale != nil {
				var units int64
				if err := json.Unmarshal([]byte(*qty), &units); err == nil {
					l.Quantity = quantity.FormatUnits(units, int(*scale))
				}
			}
			inv.Lines = append(inv.Lines, &l)
		}
		return rows.Err()
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
	resp := &billingv1.ListInvoicesResponse{}
	err = pgxx.InTenantTx(ctx, s.app, ti.ID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+invoiceColumns+` FROM invoices i LEFT JOIN invoice_sagas s ON s.tenant_id = i.tenant_id AND s.invoice_id = i.id
			WHERE ($1::uuid IS NULL OR i.customer_id = $1) AND ($2 = '' OR i.period_id = $2) AND ($3 = '' OR i.status = $3) AND i.id > $4
			ORDER BY i.id LIMIT $5`, customer, req.GetPeriodId(), req.GetStatus(), after, size+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			inv, err := scanInvoice(rows)
			if err != nil {
				return err
			}
			if len(resp.Invoices) == size {
				resp.NextPageToken = resp.Invoices[size-1].Id
				break
			}
			resp.Invoices = append(resp.Invoices, inv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
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
	var id uuid.UUID
	err := tx.QueryRow(ctx, `SELECT id FROM invoice_sagas WHERE tenant_id = $1 AND invoice_id = $2`, tid, invoiceID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, status.Error(codes.NotFound, "invoice has no saga (it is not finalized)")
	}
	return id, err
}

func (s *Server) sagaProto(ctx context.Context, tid, sagaID uuid.UUID) (*billingv1.InvoiceSaga, error) {
	var out billingv1.InvoiceSaga
	var id, inv uuid.UUID
	var lastErr *string
	err := s.system.QueryRow(ctx, `SELECT id, invoice_id, state, version, attempt, dunning_attempt, last_error FROM invoice_sagas WHERE tenant_id = $1 AND id = $2`,
		tid, sagaID).Scan(&id, &inv, &out.State, &out.Version, &out.Attempt, &out.DunningAttempt, &lastErr)
	if err != nil {
		return nil, err
	}
	out.Id, out.InvoiceId = id.String(), inv.String()
	if lastErr != nil {
		out.LastError = *lastErr
	}
	return &out, nil
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
	var sagaID uuid.UUID
	err = s.system.QueryRow(ctx, `SELECT id FROM invoice_sagas WHERE tenant_id = $1 AND invoice_id = $2`, tid, inv).Scan(&sagaID)
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
	rows, err := s.system.Query(ctx, `SELECT seq, from_state, to_state, cause, command_id, at FROM saga_events WHERE tenant_id = $1 AND saga_id = $2 ORDER BY seq`, tid, sagaID)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	defer rows.Close()
	resp := &billingv1.GetInvoiceSagaResponse{Saga: saga}
	for rows.Next() {
		var e billingv1.SagaEvent
		var cmd *uuid.UUID
		var at time.Time
		if err := rows.Scan(&e.Seq, &e.FromState, &e.ToState, &e.Cause, &cmd, &at); err != nil {
			return nil, status.Error(codes.Internal, "internal error")
		}
		if cmd != nil {
			e.CommandId = cmd.String()
		}
		e.At = timestamppb.New(at)
		resp.Events = append(resp.Events, &e)
	}
	return resp, rows.Err()
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
