package query

import (
	"context"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/quantity"
	"github.com/0furkancolak/payrudder/pkg/tenant"
)

// Longest ranges per granularity (points stay in the hundreds).
const (
	maxHourRange = 31 * 24 * time.Hour
	maxDayRange  = 400 * 24 * time.Hour
)

// Defaults of the requests that leave a field out.
const (
	defaultTopLimit    = 10
	defaultStatsWindow = 5 * time.Minute
)

// What the answers say about their own precision.
const (
	precisionEstimated = "estimated"
	precisionExact     = "exact"
)

// Queries answers analytics requests from ClickHouse.
type Queries struct {
	conn driver.Conn
	now  func() time.Time
}

// New returns the usage queries.
func New(conn driver.Conn) *Queries { return &Queries{conn: conn, now: time.Now} }

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

// forCaller is the builder of the calling tenant.
func forCaller(ctx context.Context) (tenant.Info, Builder, error) {
	ti, err := tenant.Require(ctx)
	if err != nil {
		return tenant.Info{}, Builder{}, err
	}
	b, err := ForTenant(ti.ID)
	if err != nil {
		return tenant.Info{}, Builder{}, errkey.New(codes.Unauthenticated, "analytics.tenant.unknown", "unknown tenant")
	}
	return ti, b, nil
}

// usageFilter checks a series request and turns it into the filter of the builder. The ranges it accepts depend on the granularity.
func usageFilter(req *analyticsv1.QueryUsageRequest, ti tenant.Info) (UsageFilter, error) {
	f := UsageFilter{Meter: req.GetMeterCode(), Timezone: ti.Timezone, Exact: req.GetExact()}
	if c := req.GetCustomerId(); c != "" {
		var err error
		if f.Customer, err = errkey.ParseUUID("analytics.customer.bad_id", "customer_id", c); err != nil {
			return f, err
		}
	}
	if req.GetFrom() == nil || req.GetTo() == nil || req.GetFrom().CheckValid() != nil || req.GetTo().CheckValid() != nil {
		return f, invalid("analytics.range.required", "from and to are required")
	}
	f.From, f.To = req.GetFrom().AsTime(), req.GetTo().AsTime()
	span := f.To.Sub(f.From)
	switch req.GetGranularity() {
	case analyticsv1.Granularity_GRANULARITY_HOUR, analyticsv1.Granularity_GRANULARITY_UNSPECIFIED:
		f.Granularity = Hour
		if span > maxHourRange {
			return f, invalid("analytics.range.hourly_too_long", "an hourly series covers at most 31 days")
		}
	case analyticsv1.Granularity_GRANULARITY_DAY:
		f.Granularity = Day
		if span > maxDayRange {
			return f, invalid("analytics.range.daily_too_long", "a daily series covers at most 400 days")
		}
	default:
		return f, invalid("analytics.granularity.unknown", "unknown granularity")
	}
	if f.Exact && span > MaxExactRange {
		return f, invalid("analytics.query.exact_too_long", "exact queries cover at most one day; use the estimated series for longer ranges")
	}
	return f, nil
}

// Series is the usage of a meter over a range, bucketed by hour or day.
func (s *Queries) Series(ctx context.Context, req *analyticsv1.QueryUsageRequest) (*analyticsv1.QueryUsageResponse, error) {
	ti, b, err := forCaller(ctx)
	if err != nil {
		return nil, err
	}
	f, err := usageFilter(req, ti)
	if err != nil {
		return nil, err
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
	resp := &analyticsv1.QueryUsageResponse{Points: []*analyticsv1.UsagePoint{}, Precision: precisionEstimated}
	if f.Exact {
		resp.Precision = precisionExact
	}
	for rows.Next() {
		var (
			start  time.Time
			units  int64
			scale  uint8
			events uint64
		)
		if err := rows.Scan(&start, &units, &scale, &events); err != nil {
			return nil, unavailable(err)
		}
		resp.QuantityScale = uint32(scale)
		resp.Points = append(resp.Points, &analyticsv1.UsagePoint{Start: timestamppb.New(start.UTC()), Quantity: quantity.FormatUnits(units, int(scale)), EventCount: int64(events)}) //nolint:gosec // event counts fit
	}
	if err := rows.Err(); err != nil {
		return nil, unavailable(err)
	}
	return resp, nil
}

// TopCustomers ranks customers by their usage of a meter in a billing period.
func (s *Queries) TopCustomers(ctx context.Context, req *analyticsv1.TopCustomersRequest) (*analyticsv1.TopCustomersResponse, error) {
	_, b, err := forCaller(ctx)
	if err != nil {
		return nil, err
	}
	limit := int(req.GetLimit())
	if limit == 0 {
		limit = defaultTopLimit
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
	resp := &analyticsv1.TopCustomersResponse{Customers: []*analyticsv1.CustomerUsage{}, Precision: precisionExact}
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

// statsBuilder is the builder of an operator's ingest statistics: one tenant when the request names it, every tenant otherwise.
func statsBuilder(tenantID string) (Builder, error) {
	if tenantID == "" {
		b, err := AllTenants(true)
		if err != nil {
			return b, badQuery(err)
		}
		return b, nil
	}
	id, err := errkey.ParseUUID("analytics.tenant.bad_id", "tenant_id", tenantID)
	if err != nil {
		return Builder{}, err
	}
	b, err := ForTenant(id)
	if err != nil {
		return b, badQuery(err)
	}
	return b, nil
}

// IngestStats is what each tenant ingested over a recent window. Only an operator may ask.
func (s *Queries) IngestStats(ctx context.Context, req *analyticsv1.GetIngestStatsRequest) (*analyticsv1.GetIngestStatsResponse, error) {
	if err := tenant.RequireOperator(ctx); err != nil {
		return nil, err
	}
	window := time.Duration(req.GetWindowSeconds()) * time.Second
	if req.GetWindowSeconds() == 0 {
		window = defaultStatsWindow
	}
	b, err := statsBuilder(req.GetTenantId())
	if err != nil {
		return nil, err
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
