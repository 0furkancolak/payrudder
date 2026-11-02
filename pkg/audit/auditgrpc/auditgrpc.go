// Package auditgrpc serves auditv1.AuditService over a service's own audit_events table. A service says how to read its table (Source); this package checks who
// asks, turns the request into a pkg/audit Filter and the rows into messages.
package auditgrpc

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	auditv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/audit/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Source reads one service's audit log. Both methods return at most f.Limit rows, newest first, after f.After.
type Source interface {
	// Tenant reads the entries of one tenant wherever its data lives (and, where the service keeps them, the account's own entries): f.TenantID is set.
	Tenant(ctx context.Context, f audit.Filter) ([]audit.Row, error)
	// All reads the entries of every tenant that the pooled database holds, and those that belong to none: the operator's view.
	All(ctx context.Context, f audit.Filter) ([]audit.Row, error)
}

// Server is AuditService on a Source.
type Server struct {
	auditv1.UnimplementedAuditServiceServer
	Src Source
}

// Register adds the service to srv.
func Register(srv grpc.ServiceRegistrar, src Source) {
	auditv1.RegisterAuditServiceServer(srv, &Server{Src: src})
}

// ListAuditEvents implements auditv1.AuditServiceServer. A call for a tenant sees that tenant; otherwise only an operator may ask.
func (s *Server) ListAuditEvents(ctx context.Context, req *auditv1.ListAuditEventsRequest) (*auditv1.ListAuditEventsResponse, error) {
	f, err := FilterOf(req)
	if err != nil {
		return nil, err
	}
	var rows []audit.Row
	if t, ok := tenant.FromContext(ctx); ok {
		f.TenantID = t.ID
		rows, err = s.Src.Tenant(ctx, f)
	} else {
		if err := tenant.RequireOperator(ctx); err != nil {
			return nil, err
		}
		if id := req.GetTenantId(); id != "" {
			if f.TenantID, err = uuid.Parse(id); err != nil {
				return nil, errkey.New(codes.InvalidArgument, "audit.filter.bad_tenant", "tenant_id must be a UUID")
			}
			rows, err = s.Src.Tenant(ctx, f)
		} else {
			rows, err = s.Src.All(ctx, f)
		}
	}
	if err != nil {
		return nil, err
	}
	out := &auditv1.ListAuditEventsResponse{Events: make([]*auditv1.AuditEvent, len(rows))}
	for i, r := range rows {
		out.Events[i] = Message(r)
	}
	return out, nil
}

// FilterOf is the filter a request describes (the scope is the caller's to set).
func FilterOf(req *auditv1.ListAuditEventsRequest) (audit.Filter, error) {
	after, err := audit.ParseToken(req.GetPageToken())
	if err != nil {
		return audit.Filter{}, errkey.New(codes.InvalidArgument, "audit.page_token.invalid", "page_token is not one this API returned")
	}
	f := audit.Filter{Actions: req.GetActions(), Actor: req.GetActor(), TargetType: req.GetTargetType(), TargetID: req.GetTargetId(), After: after, Limit: int(req.GetPageSize())}
	if id := req.GetAccountId(); id != "" {
		if f.AccountID, err = uuid.Parse(id); err != nil {
			return audit.Filter{}, errkey.New(codes.InvalidArgument, "audit.filter.bad_account", "account_id must be a UUID")
		}
	}
	if t := req.GetFromTime(); t != nil {
		f.From = t.AsTime()
	}
	if t := req.GetToTime(); t != nil {
		f.To = t.AsTime()
	}
	return f, nil
}

// Message is the wire form of a stored entry.
func Message(r audit.Row) *auditv1.AuditEvent {
	m := &auditv1.AuditEvent{Id: r.ID.String(), ActorType: r.ActorType, ActorId: r.ActorID, ActorEmail: r.ActorEmail, Action: r.Action, TargetType: r.TargetType,
		TargetId: r.TargetID, Summary: r.Summary, DetailJson: string(r.Detail), BeforeHash: r.BeforeHash, AfterHash: r.AfterHash, SourceIp: r.SourceIP, RequestId: r.RequestID,
		CreateTime: timestamppb.New(r.CreatedAt)}
	if r.TenantID != nil {
		m.TenantId = r.TenantID.String()
	}
	if r.AccountID != nil {
		m.AccountId = r.AccountID.String()
	}
	if !json.Valid(r.Detail) {
		m.DetailJson = "{}"
	}
	return m
}

// Row is the stored form of a message (the gateway turns the pages it collected into the export).
func Row(m *auditv1.AuditEvent) audit.Row {
	r := audit.Row{ActorType: m.GetActorType(), ActorID: m.GetActorId(), ActorEmail: m.GetActorEmail(), Action: m.GetAction(), TargetType: m.GetTargetType(), TargetID: m.GetTargetId(),
		Summary: m.GetSummary(), Detail: json.RawMessage(m.GetDetailJson()), BeforeHash: m.GetBeforeHash(), AfterHash: m.GetAfterHash(), SourceIP: m.GetSourceIp(),
		RequestID: m.GetRequestId(), CreatedAt: m.GetCreateTime().AsTime()}
	r.ID, _ = uuid.Parse(m.GetId())
	if id, err := uuid.Parse(m.GetTenantId()); err == nil {
		r.TenantID = &id
	}
	if id, err := uuid.Parse(m.GetAccountId()); err == nil {
		r.AccountID = &id
	}
	return r
}
