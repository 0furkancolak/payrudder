// Package api implements AnalyticsService on ClickHouse.
package api

import (
	"context"
	"log/slog"
	"time"
	_ "time/tzdata" // tenant time zones must load in distroless images

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticsv1 "github.com/0furkancolak/quotify/gen/go/quotify/analytics/v1"
	"github.com/0furkancolak/quotify/pkg/errkey"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/analytics-svc/internal/query"
)

// Longest ranges per granularity (points stay in the hundreds).
const (
	maxHourRange = 31 * 24 * time.Hour
	maxDayRange  = 400 * 24 * time.Hour
)

// Server implements analyticsv1.AnalyticsServiceServer.
type Server struct {
	analyticsv1.UnimplementedAnalyticsServiceServer
	conn driver.Conn
	now  func() time.Time
}

// New returns the service.
func New(conn driver.Conn) *Server { return &Server{conn: conn, now: time.Now} }

// invalid is an InvalidArgument status with the error key key.
func invalid(key, msg string) error { return errkey.New(codes.InvalidArgument, key, msg) }

// badQuery answers for a query the builder refused: the builder's text names internals, so it goes to the log and the client gets a fixed message.
func badQuery(err error) error {
	slog.Warn("analytics query refused", "error", err)
	return invalid("analytics.query.invalid", "the query is not valid")
}

func unavailable(err error) error {
	slog.Warn("clickhouse query failed", "error", err)
	return errkey.New(codes.Unavailable, "analytics.store.unavailable", "analytics store unavailable")
}

// QueryUsage implements analyticsv1.AnalyticsServiceServer.
func (s *Server) QueryUsage(ctx context.Context, req *analyticsv1.QueryUsageRequest) (*analyticsv1.QueryUsageResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	b, err := query.ForTenant(ti.ID)
	if err != nil {
		return nil, errkey.New(codes.Unauthenticated, "analytics.tenant.unknown", "unknown tenant")
	}
	f := query.UsageFilter{Meter: req.GetMeterCode(), Timezone: ti.Timezone, Exact: req.GetExact()}
	if c := req.GetCustomerId(); c != "" {
		if f.Customer, err = uuid.Parse(c); err != nil {
			return nil, invalid("analytics.customer.bad_id", "customer_id must be a UUID")
		}
	}
	if req.GetFrom() == nil || req.GetTo() == nil || req.GetFrom().CheckValid() != nil || req.GetTo().CheckValid() != nil {
		return nil, invalid("analytics.range.required", "from and to are required")
	}
	f.From, f.To = req.GetFrom().AsTime(), req.GetTo().AsTime()
	span := f.To.Sub(f.From)
	switch req.GetGranularity() {
	case analyticsv1.Granularity_GRANULARITY_HOUR, analyticsv1.Granularity_GRANULARITY_UNSPECIFIED:
		f.Granularity = query.Hour
		if span > maxHourRange {
			return nil, invalid("analytics.range.hourly_too_long", "an hourly series covers at most 31 days")
		}
	case analyticsv1.Granularity_GRANULARITY_DAY:
		f.Granularity = query.Day
		if span > maxDayRange {
			return nil, invalid("analytics.range.daily_too_long", "a daily series covers at most 400 days")
		}
	default:
		return nil, invalid("analytics.granularity.unknown", "unknown granularity")
	}
	if f.Exact && span > query.MaxExactRange {
		return nil, invalid("analytics.query.exact_too_long", "exact queries cover at most one day; use the estimated series for longer ranges")
	}
	q, err := b.Usage(f)
	if err != nil {
		return nil, badQuery(err)
	}
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	resp := &analyticsv1.QueryUsageResponse{Points: []*analyticsv1.UsagePoint{}, Precision: "estimated"}
	if f.Exact {
		resp.Precision = "exact"
	}
	type point struct {
		start  time.Time
		units  int64
		scale  uint8
		events uint64
	}
	var pts []point
	for rows.Next() {
		var p point
		if err := rows.Scan(&p.start, &p.units, &p.scale, &p.events); err != nil {
			return nil, unavailable(err)
		}
		pts = append(pts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	for _, p := range pts {
		resp.QuantityScale = uint32(p.scale)
		resp.Points = append(resp.Points, &analyticsv1.UsagePoint{Start: timestamppb.New(p.start.UTC()), Quantity: quantity.FormatUnits(p.units, int(p.scale)), EventCount: int64(p.events)}) //nolint:gosec // event counts fit
	}
	return resp, nil
}

// TopCustomers implements analyticsv1.AnalyticsServiceServer.
func (s *Server) TopCustomers(ctx context.Context, req *analyticsv1.TopCustomersRequest) (*analyticsv1.TopCustomersResponse, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	b, err := query.ForTenant(ti.ID)
	if err != nil {
		return nil, errkey.New(codes.Unauthenticated, "analytics.tenant.unknown", "unknown tenant")
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = 10
	}
	q, err := b.TopCustomers(req.GetMeterCode(), req.GetPeriodId(), limit)
	if err != nil {
		return nil, badQuery(err)
	}
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	resp := &analyticsv1.TopCustomersResponse{Customers: []*analyticsv1.CustomerUsage{}, Precision: "exact"}
	for rows.Next() {
		var id uuid.UUID
		var ext string
		var units int64
		var scale uint8
		var events uint64
		if err := rows.Scan(&id, &ext, &units, &scale, &events); err != nil {
			return nil, unavailable(err)
		}
		resp.QuantityScale = uint32(scale)
		resp.Customers = append(resp.Customers, &analyticsv1.CustomerUsage{CustomerId: id.String(), CustomerExternalId: ext,
			Quantity: quantity.FormatUnits(units, int(scale)), EventCount: int64(events)}) //nolint:gosec // event counts fit
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return resp, nil
}

// GetIngestStats implements analyticsv1.AnalyticsServiceServer.
func (s *Server) GetIngestStats(ctx context.Context, req *analyticsv1.GetIngestStatsRequest) (*analyticsv1.GetIngestStatsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	window := time.Duration(req.GetWindowSeconds()) * time.Second
	if req.GetWindowSeconds() == 0 {
		window = 5 * time.Minute
	}
	var b query.Builder
	var err error
	if t := req.GetTenantId(); t != "" {
		id, perr := uuid.Parse(t)
		if perr != nil {
			return nil, invalid("analytics.tenant.bad_id", "tenant_id must be a UUID")
		}
		b, err = query.ForTenant(id)
	} else {
		b, err = query.AllTenants(true)
	}
	if err != nil {
		return nil, badQuery(err)
	}
	q, err := b.IngestStats(window, s.now())
	if err != nil {
		return nil, badQuery(err)
	}
	rows, err := s.conn.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, unavailable(err)
	}
	defer rows.Close()
	resp := &analyticsv1.GetIngestStatsResponse{Tenants: []*analyticsv1.TenantIngestStats{}}
	for rows.Next() {
		var id uuid.UUID
		var events, late uint64
		var dups int64
		if err := rows.Scan(&id, &events, &late, &dups); err != nil {
			return nil, unavailable(err)
		}
		st := &analyticsv1.TenantIngestStats{TenantId: id.String(), Events: int64(events), EventsPerSecond: float64(events) / window.Seconds()} //nolint:gosec // counts fit
		if events > 0 {
			st.LatePercent = 100 * float64(late) / float64(events)
			st.DuplicatePercent = 100 * float64(max(dups, 0)) / float64(events)
		}
		resp.Tenants = append(resp.Tenants, st)
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return resp, nil
}
