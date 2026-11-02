package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/audit/auditgrpc"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/services/gateway/internal/ratelimit"
)

// The audit log (pkg/audit, docs/plans/22 G4) lives in the database of each service that records an action: plans-svc (keys, members, plans, webhook endpoints,
// customers, tenants, sign-in security), billing-svc (credit notes and refunds, the operator's invoice actions) and payments-worker (provider accounts). The gateway asks
// each for the same page and puts the answers together, newest first; a service that does not answer is named, not hidden.

func (s *Server) auditRoutes() []Route {
	return []Route{
		{"GET", "/v1/audit-events", AccessTenant, ratelimit.ClassRead, s.listAuditEvents},
		{"GET", "/v1/audit-events/export", AccessTenant, ratelimit.ClassRead, s.exportAuditEvents},
		{"GET", "/admin/v1/audit-events", AccessOperator, "", s.adminListAuditEvents},
		{"GET", "/admin/v1/audit-events/export", AccessOperator, "", s.adminExportAuditEvents},
	}
}

// exportMaxRows bounds an export: the newest rows of the filter.
const exportMaxRows = 5000

// auditAsker is whose entries a request reads: the tenant of the caller, or (an operator) every tenant or the one named.
type auditAsker struct {
	tenantID string // the tenant whose entries these are; empty for an operator's view across tenants
	account  string
}

func (s *Server) listAuditEvents(w http.ResponseWriter, r *http.Request) {
	s.auditList(w, r, s.tenantAsker(r))
}

func (s *Server) adminListAuditEvents(w http.ResponseWriter, r *http.Request) {
	s.auditList(w, r, operatorAsker(r))
}

func (s *Server) exportAuditEvents(w http.ResponseWriter, r *http.Request) {
	s.auditExport(w, r, s.tenantAsker(r))
}

func (s *Server) adminExportAuditEvents(w http.ResponseWriter, r *http.Request) {
	s.auditExport(w, r, operatorAsker(r))
}

func (s *Server) tenantAsker(r *http.Request) auditAsker {
	return auditAsker{tenantID: principalFrom(r.Context()).Tenant.ID.String()}
}

func operatorAsker(r *http.Request) auditAsker {
	q := r.URL.Query()
	return auditAsker{tenantID: q.Get("tenant_id"), account: q.Get("account_id")}
}

// auditRequest reads the filter of a request. A person who is only a viewer of the account sees the tenant's data but not who did what: owners and administrators do.
func (s *Server) auditRequest(r *http.Request) (*auditv1.ListAuditEventsRequest, error) {
	if principalFrom(r.Context()).MemberRole == "viewer" {
		return nil, errkey.New(codes.PermissionDenied, "identity.account.forbidden", "your role in this account may not read the audit log")
	}
	return auditgrpc.RequestFromQuery(r.URL.Query())
}

func (s *Server) auditList(w http.ResponseWriter, r *http.Request, who auditAsker) {
	req, err := s.auditRequest(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := s.collectAudit(r.Context(), req, who)
	respond(w, r, http.StatusOK, resp, err)
}

// collectAudit asks every service for size+1 entries (one more than the page shows, to know whether another page exists) and merges them.
func (s *Server) collectAudit(ctx context.Context, req *auditv1.ListAuditEventsRequest, who auditAsker) (*auditv1.ListAuditEventsResponse, error) {
	size := int(req.GetPageSize())
	if size <= 0 {
		size = audit.DefaultLimit
	}
	ask := proto.Clone(req).(*auditv1.ListAuditEventsRequest)
	ask.PageSize = int32(size + 1) //nolint:gosec // at most MaxLimit+1
	ask.TenantId, ask.AccountId = who.tenantID, who.account

	type source struct {
		name string
		read func() ([]*auditv1.AuditEvent, error)
	}
	var sources []source
	if s.d.AuditPlans != nil {
		sources = append(sources, source{"plans", func() ([]*auditv1.AuditEvent, error) {
			resp, err := s.d.AuditPlans.ListAuditEvents(ctx, ask)
			return resp.GetEvents(), err
		}})
	}
	if s.d.AuditBilling != nil {
		sources = append(sources, source{"billing", func() ([]*auditv1.AuditEvent, error) {
			resp, err := s.d.AuditBilling.ListAuditEvents(ctx, ask)
			return resp.GetEvents(), err
		}})
	}
	if s.d.PaymentsURL != nil {
		sources = append(sources, source{"payments", func() ([]*auditv1.AuditEvent, error) {
			path, q := "/admin/audit-events", auditgrpc.Query(ask)
			if who.tenantID != "" {
				path = "/admin/tenants/" + url.PathEscape(who.tenantID) + "/audit-events"
			}
			var raw json.RawMessage
			code, err := s.paymentsDo(ctx, http.MethodGet, path, q, nil, &raw)
			if err != nil {
				return nil, err
			}
			if code/100 != 2 {
				return nil, errkey.New(codes.Unavailable, "audit.unavailable", "payments-worker did not answer the audit request")
			}
			return auditgrpc.EventsFromJSON(raw)
		}})
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var all []*auditv1.AuditEvent
	var failed []string
	var firstErr error
	for _, src := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := src.read()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, src.name)
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			all = append(all, events...)
		}()
	}
	wg.Wait()
	if len(failed) > 0 && len(failed) == len(sources) {
		return nil, firstErr
	}
	slices.Sort(failed)
	slices.SortStableFunc(all, func(a, b *auditv1.AuditEvent) int {
		if c := b.GetCreateTime().AsTime().Compare(a.GetCreateTime().AsTime()); c != 0 {
			return c
		}
		return strings.Compare(b.GetId(), a.GetId())
	})
	all = slices.CompactFunc(all, func(a, b *auditv1.AuditEvent) bool { return a.GetId() == b.GetId() })
	resp := &auditv1.ListAuditEventsResponse{Incomplete: len(failed) > 0, UnavailableSources: failed}
	if len(all) > size {
		all = all[:size]
		last := auditgrpc.Row(all[size-1])
		resp.NextPageToken = audit.CursorOf(last).Token()
	}
	resp.Events = all
	return resp, nil
}

// auditExport writes the newest rows of the filter as CSV. An export that could not read every service fails rather than leave rows out.
func (s *Server) auditExport(w http.ResponseWriter, r *http.Request, who auditAsker) {
	req, err := s.auditRequest(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	req.PageSize = audit.MaxLimit
	var rows []audit.Row
	for {
		page, err := s.collectAudit(r.Context(), req, who)
		if err == nil && page.GetIncomplete() {
			err = errkey.New(codes.Unavailable, "audit.unavailable", "the export could not read every service ("+strings.Join(page.GetUnavailableSources(), ", ")+"): try again")
		}
		if err != nil {
			writeError(w, r, err)
			return
		}
		for _, e := range page.GetEvents() {
			rows = append(rows, auditgrpc.Row(e))
		}
		if page.GetNextPageToken() == "" || len(rows) >= exportMaxRows {
			break
		}
		req.PageToken = page.GetNextPageToken()
	}
	rows = rows[:min(len(rows), exportMaxRows)]
	noStore(w)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit-log.csv"`)
	out := csv.NewWriter(w)
	_ = out.Write(audit.CSVHeader)
	for _, row := range rows {
		_ = out.Write(audit.CSVRecord(row))
	}
	out.Flush()
}
