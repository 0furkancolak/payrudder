// Package grpcapi implements PlansService. Every handler runs in pgxx.InTenantTx on the app pool (role plans_app),
// so RLS scopes every statement to the caller's tenant.
package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/pgxx"
	"github.com/0furkancolak/quotify/pkg/quantity"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

// Server implements plansv1.PlansServiceServer.
type Server struct {
	plansv1.UnimplementedPlansServiceServer
	pool *pgxpool.Pool
	log  *slog.Logger
	now  func() time.Time
}

// New returns a Server on the app pool.
func New(appPool *pgxpool.Pool, log *slog.Logger) *Server {
	return &Server{pool: appPool, log: log, now: time.Now}
}

// tx runs fn in the caller's tenant transaction.
func (s *Server) tx(ctx context.Context, fn func(q *sqlc.Queries) error) error {
	t, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	return pgxx.InTenantTx(ctx, s.pool, t.ID, func(tx pgx.Tx) error { return fn(sqlc.New(tx)) })
}

// current returns the caller's tenant row. The tenant must exist (the gateway resolved it from an API key).
func current(ctx context.Context, q *sqlc.Queries) (sqlc.CurrentTenantRow, error) {
	t, err := q.CurrentTenant(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, status.Error(codes.Unauthenticated, "unknown tenant")
	}
	return t, err
}

func (s *Server) CreateCustomer(ctx context.Context, req *plansv1.CreateCustomerRequest) (*plansv1.CreateCustomerResponse, error) {
	var out sqlc.Customer
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		if err := domain.ValidateCustomer(t.Environment, req); err != nil {
			return err
		}
		if quota := domain.QuotasFor(t.Tier).MaxCustomers; quota > 0 {
			n, err := q.CountCustomers(ctx)
			if err != nil {
				return err
			}
			if n >= quota {
				return status.Errorf(codes.ResourceExhausted, "customer quota of %d reached for tier %s", quota, t.Tier)
			}
		}
		var pm *string
		if req.GetPaymentMethod() != "" {
			pm = &req.PaymentMethod
		}
		var country, billing *string
		if req.GetCountry() != "" {
			country = &req.Country
		}
		if req.GetBillingCurrency() != "" {
			billing = &req.BillingCurrency
		}
		out, err = q.CreateCustomer(ctx, sqlc.CreateCustomerParams{
			ID: idgen.NewUUIDv7(), ExternalID: req.GetExternalId(), Name: req.GetName(), Email: req.GetEmail(), PaymentMethod: pm,
			Country: country, BillingCurrency: billing,
		})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return &plansv1.CreateCustomerResponse{Customer: convert.Customer(out)}, nil
}

func (s *Server) getCustomer(ctx context.Context, externalID string) (*plansv1.Customer, error) {
	if externalID == "" {
		return nil, domain.Invalid("external_id", "required")
	}
	var out sqlc.Customer
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.GetCustomerByExternalID(ctx, externalID)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

func (s *Server) getCustomerByID(ctx context.Context, id string) (*plansv1.Customer, error) {
	uid, err := uuid.Parse(id)
	if err != nil {
		return nil, domain.Invalid("id", "must be a UUID")
	}
	var out sqlc.Customer
	err = s.tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.GetCustomerByID(ctx, uid)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return convert.Customer(out), nil
}

func (s *Server) GetCustomer(ctx context.Context, req *plansv1.GetCustomerRequest) (*plansv1.GetCustomerResponse, error) {
	if req.GetId() != "" {
		c, err := s.getCustomerByID(ctx, req.GetId())
		if err != nil {
			return nil, err
		}
		return &plansv1.GetCustomerResponse{Customer: c}, nil
	}
	c, err := s.getCustomer(ctx, req.GetExternalId())
	if err != nil {
		return nil, err
	}
	return &plansv1.GetCustomerResponse{Customer: c}, nil
}

func (s *Server) ResolveCustomer(ctx context.Context, req *plansv1.ResolveCustomerRequest) (*plansv1.ResolveCustomerResponse, error) {
	c, err := s.getCustomer(ctx, req.GetExternalId())
	if err != nil {
		return nil, err
	}
	return &plansv1.ResolveCustomerResponse{Customer: c}, nil
}

func (s *Server) ListCustomers(ctx context.Context, req *plansv1.ListCustomersRequest) (*plansv1.ListCustomersResponse, error) {
	size := req.GetPageSize()
	switch {
	case size == 0:
		size = 100
	case size < 0 || size > 500:
		return nil, domain.Invalid("page_size", "between 1 and 500")
	}
	after, err := decodePageToken(req.GetPageToken())
	if err != nil {
		return nil, domain.Invalid("page_token", "malformed")
	}
	var rows []sqlc.Customer
	err = s.tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListCustomers(ctx, sqlc.ListCustomersParams{After: after, PageSize: size + 1})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	resp := &plansv1.ListCustomersResponse{}
	if len(rows) > int(size) {
		rows = rows[:size]
		resp.NextPageToken = encodePageToken(rows[len(rows)-1].ExternalID)
	}
	for _, c := range rows {
		resp.Customers = append(resp.Customers, convert.Customer(c))
	}
	return resp, nil
}

func (s *Server) SetPaymentMethod(ctx context.Context, req *plansv1.SetPaymentMethodRequest) (*plansv1.SetPaymentMethodResponse, error) {
	var out sqlc.Customer
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		if req.GetPaymentMethod() == "" {
			return domain.Invalid("payment_method", "required")
		}
		if err := domain.ValidatePaymentMethod(t.Environment, req.GetPaymentMethod()); err != nil {
			return domain.Invalid("payment_method", err.Error())
		}
		out, err = q.SetPaymentMethod(ctx, sqlc.SetPaymentMethodParams{ExternalID: req.GetExternalId(), PaymentMethod: &req.PaymentMethod})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "customer")
	}
	return &plansv1.SetPaymentMethodResponse{Customer: convert.Customer(out)}, nil
}

func (s *Server) CreateMeter(ctx context.Context, req *plansv1.CreateMeterRequest) (*plansv1.CreateMeterResponse, error) {
	var out sqlc.Meter
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		prevScale, nextVersion := -1, int32(1)
		latest, err := q.LatestMeter(ctx, req.GetCode())
		switch {
		case err == nil:
			prevScale, nextVersion = int(latest.QuantityScale), latest.Version+1
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		in, err := domain.ValidateMeter(req, prevScale)
		if err != nil {
			return err
		}
		if quota := domain.QuotasFor(t.Tier).MaxMeterCodes; prevScale < 0 && quota > 0 {
			n, err := q.CountMeterCodes(ctx)
			if err != nil {
				return err
			}
			if n >= quota {
				return status.Errorf(codes.ResourceExhausted, "meter quota of %d reached for tier %s", quota, t.Tier)
			}
		}
		out, err = q.InsertMeter(ctx, sqlc.InsertMeterParams{
			Code: in.Code, Version: nextVersion, Aggregation: in.Aggregation, ValueProperty: in.ValueProperty,
			Filter: in.Filter, QuantityScale: in.Scale, UnitLabel: in.UnitLabel,
		})
		if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation {
			return status.Error(codes.Aborted, "concurrent meter version created; retry")
		}
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	m, err := convert.Meter(out)
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	return &plansv1.CreateMeterResponse{Meter: m}, nil
}

func (s *Server) GetMeter(ctx context.Context, req *plansv1.GetMeterRequest) (*plansv1.GetMeterResponse, error) {
	var out sqlc.Meter
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.LatestMeter(ctx, req.GetCode())
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	m, err := convert.Meter(out)
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	return &plansv1.GetMeterResponse{Meter: m}, nil
}

func (s *Server) ListMeters(ctx context.Context, _ *plansv1.ListMetersRequest) (*plansv1.ListMetersResponse, error) {
	var rows []sqlc.Meter
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListLatestMeters(ctx)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "meter")
	}
	resp := &plansv1.ListMetersResponse{}
	for _, r := range rows {
		m, err := convert.Meter(r)
		if err != nil {
			return nil, convert.Err(err, "meter")
		}
		resp.Meters = append(resp.Meters, m)
	}
	return resp, nil
}

func (s *Server) CreatePlan(ctx context.Context, req *plansv1.CreatePlanRequest) (*plansv1.CreatePlanResponse, error) {
	if err := domain.ValidatePlan(req); err != nil {
		return nil, err
	}
	var out sqlc.Plan
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		out, err = q.InsertPlan(ctx, sqlc.InsertPlanParams{ID: idgen.NewUUIDv7(), Code: req.GetCode(), Name: req.GetName()})
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "plan")
	}
	return &plansv1.CreatePlanResponse{Plan: convert.Plan(out.ID.String(), out.Code, out.Name, out.CreatedAt, 0)}, nil
}

func (s *Server) ListPlans(ctx context.Context, _ *plansv1.ListPlansRequest) (*plansv1.ListPlansResponse, error) {
	var rows []sqlc.ListPlansRow
	err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListPlans(ctx)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "plan")
	}
	resp := &plansv1.ListPlansResponse{}
	for _, p := range rows {
		resp.Plans = append(resp.Plans, convert.Plan(p.ID.String(), p.Code, p.Name, p.CreatedAt, p.LatestVersion))
	}
	return resp, nil
}

// meterScales maps each meter code of the tenant to its quantity scale.
func meterScales(ctx context.Context, q *sqlc.Queries) (map[string]int, error) {
	meters, err := q.ListLatestMeters(ctx)
	if err != nil {
		return nil, err
	}
	scales := make(map[string]int, len(meters))
	for _, m := range meters {
		scales[m.Code] = int(m.QuantityScale)
	}
	return scales, nil
}

func (s *Server) PublishPlanVersion(ctx context.Context, req *plansv1.PublishPlanVersionRequest) (*plansv1.PublishPlanVersionResponse, error) {
	var out *plansv1.PlanVersion
	publish := func(q *sqlc.Queries) error {
		plan, err := q.GetPlanByCode(ctx, req.GetPlanCode())
		if err != nil {
			return convert.Err(err, "plan")
		}
		scales, err := meterScales(ctx, q)
		if err != nil {
			return err
		}
		in, err := domain.ValidatePlanVersion(req, scales)
		if err != nil {
			return err
		}
		t, err := current(ctx, q)
		if err != nil {
			return err
		}
		fees, err := domain.ValidateFees(t.Currency, req.GetFees())
		if err != nil {
			return err
		}
		currencyPrices, err := domain.ValidateCurrencyPrices(req, scales, fees)
		if err != nil {
			return err
		}
		pv, err := q.InsertPlanVersion(ctx, sqlc.InsertPlanVersionParams{ID: idgen.NewUUIDv7(), PlanID: plan.ID, BaseFeeMinor: in.BaseFeeMinor})
		if err != nil {
			return err
		}
		for _, f := range fees {
			if err := q.InsertPlanVersionFee(ctx, sqlc.InsertPlanVersionFeeParams{PlanVersionID: pv.ID, Currency: f.Currency, Interval: f.Interval, BaseFeeMinor: f.BaseFeeMinor}); err != nil {
				return err
			}
		}
		for cur, prices := range currencyPrices {
			for _, p := range prices {
				if err := q.InsertPlanCurrencyPrice(ctx, sqlc.InsertPlanCurrencyPriceParams{PlanVersionID: pv.ID, Currency: cur, MeterCode: p.MeterCode, Model: p.Model, Config: p.Config}); err != nil {
					return err
				}
			}
		}
		for _, p := range in.Prices {
			if err := q.InsertPlanPrice(ctx, sqlc.InsertPlanPriceParams{PlanVersionID: pv.ID, MeterCode: p.MeterCode, Model: p.Model, Config: p.Config}); err != nil {
				return err
			}
		}
		for _, e := range in.Entitlements {
			if err := q.InsertPlanEntitlement(ctx, sqlc.InsertPlanEntitlementParams{
				PlanVersionID: pv.ID, MeterCode: e.MeterCode, LimitUnits: e.LimitUnits, AlertThresholdsPct: e.Thresholds,
			}); err != nil {
				return err
			}
		}
		out, err = assemble(ctx, q, pv.ID, pv.PlanID, plan.Code, pv.Version, pv.BaseFeeMinor, pv.PublishedAt, scales)
		return err
	}
	// Two concurrent publishes of the same plan collide on UNIQUE (tenant, plan, version); retry once.
	err := s.tx(ctx, publish)
	if pgxx.ErrCode(err) == pgxx.CodeUniqueViolation && pgxx.ConstraintName(err) == "plan_versions_tenant_id_plan_id_version_key" {
		err = s.tx(ctx, publish)
	}
	if err != nil {
		return nil, convert.Err(err, "plan version")
	}
	return &plansv1.PublishPlanVersionResponse{PlanVersion: out}, nil
}

func assemble(ctx context.Context, q *sqlc.Queries, id, planID uuid.UUID, planCode string, version int32, baseFee int64,
	published time.Time, scales map[string]int) (*plansv1.PlanVersion, error) {
	pv := &plansv1.PlanVersion{
		Id: id.String(), PlanId: planID.String(), PlanCode: planCode, Version: version, BaseFeeMinor: baseFee,
		PublishTime: convertTS(published),
	}
	fees, err := q.ListPlanVersionFees(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, f := range fees {
		iv := plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
		if f.Interval == string(subscription.Yearly) {
			iv = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
		}
		pv.Fees = append(pv.Fees, &plansv1.CurrencyFee{Currency: f.Currency, BaseFeeMinor: f.BaseFeeMinor, Interval: iv})
	}
	cps, err := q.ListPlanCurrencyPrices(ctx, id)
	if err != nil {
		return nil, err
	}
	byCurrency := map[string]*plansv1.CurrencyPrices{}
	for _, p := range cps {
		price, err := domain.PriceFromStored(p.MeterCode, p.Model, p.Config, scales[p.MeterCode])
		if err != nil {
			return nil, err
		}
		cp, ok := byCurrency[p.Currency]
		if !ok {
			cp = &plansv1.CurrencyPrices{Currency: p.Currency}
			byCurrency[p.Currency] = cp
			pv.CurrencyPrices = append(pv.CurrencyPrices, cp)
		}
		cp.Prices = append(cp.Prices, price)
	}
	prices, err := q.ListPlanPrices(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, p := range prices {
		price, err := domain.PriceFromStored(p.MeterCode, p.Model, p.Config, scales[p.MeterCode])
		if err != nil {
			return nil, err
		}
		pv.Prices = append(pv.Prices, price)
	}
	ents, err := q.ListPlanEntitlements(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		pv.Entitlements = append(pv.Entitlements, &plansv1.Entitlement{
			MeterCode: e.MeterCode, Limit: quantity.FormatUnits(e.LimitUnits, scales[e.MeterCode]), AlertThresholdsPct: e.AlertThresholdsPct,
		})
	}
	return pv, nil
}

func (s *Server) GetPlanVersion(ctx context.Context, req *plansv1.GetPlanVersionRequest) (*plansv1.GetPlanVersionResponse, error) {
	var out *plansv1.PlanVersion
	err := s.tx(ctx, func(q *sqlc.Queries) error {
		scales, err := meterScales(ctx, q)
		if err != nil {
			return err
		}
		if req.GetPlanVersionId() != "" {
			id, err := uuid.Parse(req.GetPlanVersionId())
			if err != nil {
				return domain.Invalid("plan_version_id", "must be a UUID")
			}
			r, err := q.GetPlanVersionByID(ctx, id)
			if err != nil {
				return err
			}
			out, err = assemble(ctx, q, r.ID, r.PlanID, r.PlanCode, r.Version, r.BaseFeeMinor, r.PublishedAt, scales)
			return err
		}
		if req.GetPlanCode() == "" {
			return domain.Invalid("plan_code", "plan_version_id or plan_code is required")
		}
		r, err := q.GetPlanVersionByCode(ctx, sqlc.GetPlanVersionByCodeParams{PlanCode: req.GetPlanCode(), Version: req.GetVersion()})
		if err != nil {
			return err
		}
		out, err = assemble(ctx, q, r.ID, r.PlanID, r.PlanCode, r.Version, r.BaseFeeMinor, r.PublishedAt, scales)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "plan version")
	}
	return &plansv1.GetPlanVersionResponse{PlanVersion: out}, nil
}

func (s *Server) ListSubscriptionsForPeriod(req *plansv1.ListSubscriptionsForPeriodRequest, stream grpc.ServerStreamingServer[plansv1.ListSubscriptionsForPeriodResponse]) error {
	ctx := stream.Context()
	if req.GetPeriodStart() == nil || req.GetPeriodEnd() == nil || !req.GetPeriodEnd().AsTime().After(req.GetPeriodStart().AsTime()) {
		return domain.Invalid("period_end", "period_start and period_end are required, end after start")
	}
	after := uuid.Nil
	for {
		var page []sqlc.ListSubscriptionsOverlappingRow
		err := s.tx(ctx, func(q *sqlc.Queries) (err error) {
			page, err = q.ListSubscriptionsOverlapping(ctx, sqlc.ListSubscriptionsOverlappingParams{
				PeriodStart: req.GetPeriodStart().AsTime(), PeriodEnd: req.GetPeriodEnd().AsTime(), After: after,
			})
			return err
		})
		if err != nil {
			return convert.Err(err, "subscription")
		}
		for _, r := range page {
			if err := stream.Send(&plansv1.ListSubscriptionsForPeriodResponse{Subscription: subscriptionMessage(sqlc.GetSubscriptionRow(r), s.now())}); err != nil {
				return err
			}
		}
		if len(page) < 500 {
			return nil
		}
		after = page[len(page)-1].ID
	}
}
