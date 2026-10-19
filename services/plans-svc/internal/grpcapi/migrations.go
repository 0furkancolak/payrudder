package grpcapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/idgen"
	"github.com/0furkancolak/quotify/pkg/payments/providers"
	"github.com/0furkancolak/quotify/pkg/subscription"
	"github.com/0furkancolak/quotify/pkg/tenant"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/convert"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/domain"
	"github.com/0furkancolak/quotify/services/plans-svc/internal/store/sqlc"
)

var migrationStates = map[string]plansv1.ProviderMigration_State{
	"started":   plansv1.ProviderMigration_STATE_STARTED,
	"completed": plansv1.ProviderMigration_STATE_COMPLETED,
	"aborted":   plansv1.ProviderMigration_STATE_ABORTED,
}

func migrationMessage(m sqlc.ProviderMigration) *plansv1.ProviderMigration {
	out := &plansv1.ProviderMigration{
		Id: m.ID.String(), SubscriptionId: m.SubscriptionID.String(), NewSubscriptionId: uuidString(m.NewSubscriptionID),
		FromProvider: m.FromProvider, ToProvider: m.ToProvider, State: migrationStates[m.State], CreateTime: timestamppb.New(m.CreatedAt),
	}
	if m.CutoverAt != nil {
		out.CutoverTime = timestamppb.New(*m.CutoverAt)
	}
	return out
}

// cutover ends the old subscription at when and starts the one that replaces it from then, in one transaction: the chain that billing splits
// the month's fixed fee along. The new subscription is the old one with another provider (and, when the provider runs it, its id there).
func (s *Server) cutover(ctx context.Context, q *sqlc.Queries, tenantID uuid.UUID, old sqlc.GetSubscriptionRow, when time.Time,
	toProvider string, providerRef *string, owner string) (sqlc.InsertSubscriptionRow, error) {
	if !when.After(old.StartTime) {
		return sqlc.InsertSubscriptionRow{}, domain.Invalid("cutover_time", "must be after the subscription start")
	}
	now := s.now()
	if n, err := q.EndSubscription(ctx, sqlc.EndSubscriptionParams{ID: old.ID, EndTime: when}); err != nil {
		return sqlc.InsertSubscriptionRow{}, err
	} else if n != 1 {
		return sqlc.InsertSubscriptionRow{}, status.Error(codes.Aborted, "subscription changed concurrently; retry")
	}
	eff := convert.SubscriptionState(old.State, old.TrialEndsAt, old.OpenEnded, old.EndTime, now)
	if !when.After(now) {
		if _, err := q.SetSubscriptionState(ctx, sqlc.SetSubscriptionStateParams{ID: old.ID, State: string(subscription.Cancelled), PausedAt: old.PausedAt}); err != nil {
			return sqlc.InsertSubscriptionRow{}, err
		}
	}
	oldID := old.ID
	nr, err := q.InsertSubscription(ctx, sqlc.InsertSubscriptionParams{
		ID: idgen.NewUUIDv7(), CustomerID: old.CustomerID, PlanVersionID: old.PlanVersionID, StartTime: when,
		State: string(eff), Interval: old.Interval, Currency: old.Currency, TrialEndsAt: old.TrialEndsAt,
		BillingAnchor: old.BillingAnchor, ChangedFromID: &oldID, Provider: &toProvider, ProviderRef: providerRef, Owner: owner,
	})
	if err != nil {
		return nr, err
	}
	if err := publishEntitlements(ctx, q, tenantID, old.CustomerID, old.PlanVersionID, old.StartTime, &when, now); err != nil {
		return nr, err
	}
	return nr, publishEntitlements(ctx, q, tenantID, nr.CustomerID, nr.PlanVersionID, when, nil, now)
}

func (s *Server) StartProviderMigration(ctx context.Context, req *plansv1.StartProviderMigrationRequest) (*plansv1.StartProviderMigrationResponse, error) {
	id, err := parseSubscriptionID(req.GetSubscriptionId())
	if err != nil {
		return nil, err
	}
	target, ok := providers.Lookup(req.GetToProvider())
	if !ok {
		return nil, domain.Invalid("to_provider", "unknown provider "+req.GetToProvider())
	}
	var cutoverAt *time.Time
	if req.GetCutoverTime() != nil {
		if err := req.GetCutoverTime().CheckValid(); err != nil {
			return nil, domain.Invalid("cutover_time", "invalid timestamp")
		}
		t := req.GetCutoverTime().AsTime()
		cutoverAt = &t
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.ProviderMigration
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		cur, err := q.GetSubscriptionForUpdate(ctx, id)
		if err != nil {
			return err
		}
		row := sqlc.GetSubscriptionRow(cur)
		now := s.now()
		eff := convert.SubscriptionState(row.State, row.TrialEndsAt, row.OpenEnded, row.EndTime, now)
		switch {
		case !row.OpenEnded || row.CancelAtPeriodEnd:
			return status.Error(codes.FailedPrecondition, "an ended or ending subscription cannot be moved")
		case eff != subscription.Active && eff != subscription.Trialing:
			return status.Errorf(codes.FailedPrecondition, "a %s subscription cannot be moved", eff)
		case strString(row.Provider) == req.GetToProvider():
			return domain.Invalid("to_provider", "the subscription is already with "+req.GetToProvider())
		case !target.CanCharge(row.Currency):
			return status.Errorf(codes.FailedPrecondition, "%s cannot charge in %s", req.GetToProvider(), row.Currency)
		}
		mig, err := q.InsertProviderMigration(ctx, sqlc.InsertProviderMigrationParams{
			ID: idgen.NewUUIDv7(), SubscriptionID: id, FromProvider: strString(row.Provider), ToProvider: req.GetToProvider(), State: "started", CutoverAt: cutoverAt,
		})
		if err != nil {
			return err
		}
		if target.Owner() == "provider" {
			// The target provider runs it: wait for the customer to subscribe there. Nothing about the old subscription changes yet.
			out = migrationMessage(mig)
			return nil
		}
		// Quotify runs it at the target: take over now, or at the end of the paid period the old provider has been paid for.
		when := now
		if cutoverAt != nil {
			when = *cutoverAt
		} else if row.Owner == "provider" {
			return domain.Invalid("cutover_time", "required when the subscription is run by its provider: its paid period must not be cut short")
		}
		nr, err := s.cutover(ctx, q, ti.ID, row, when, req.GetToProvider(), nil, "quotify")
		if err != nil {
			return err
		}
		done, err := q.FinishProviderMigration(ctx, sqlc.FinishProviderMigrationParams{ID: mig.ID, State: "completed", NewSubscriptionID: &nr.ID, CutoverAt: &when})
		if err != nil {
			return err
		}
		out = migrationMessage(done)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return &plansv1.StartProviderMigrationResponse{Migration: out}, nil
}

func (s *Server) CompleteProviderMigration(ctx context.Context, req *plansv1.CompleteProviderMigrationRequest) (*plansv1.CompleteProviderMigrationResponse, error) {
	id, err := uuid.Parse(req.GetMigrationId())
	if err != nil {
		return nil, domain.Invalid("migration_id", "must be a UUID")
	}
	if req.GetProviderRef() == "" {
		return nil, domain.Invalid("provider_ref", "required")
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var resp plansv1.CompleteProviderMigrationResponse
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		mig, err := q.GetProviderMigrationForUpdate(ctx, id)
		if err != nil {
			return err
		}
		old, err := q.GetSubscription(ctx, mig.SubscriptionID)
		if err != nil {
			return err
		}
		resp.SourceProvider = strString(old.Provider)
		if old.Owner == "provider" {
			resp.SourceProviderRef = strString(old.ProviderRef)
		}
		switch mig.State {
		case "completed":
			// Idempotent: the provider's notifications are delivered more than once.
			if mig.NewSubscriptionID == nil {
				return status.Error(codes.Internal, "completed migration without a subscription")
			}
			nr, err := q.GetSubscription(ctx, *mig.NewSubscriptionID)
			if err != nil {
				return err
			}
			if strString(nr.ProviderRef) != req.GetProviderRef() {
				return status.Errorf(codes.FailedPrecondition, "migration already completed with %s", strString(nr.ProviderRef))
			}
			resp.Migration, resp.Subscription = migrationMessage(mig), subscriptionMessage(nr, s.now())
			return nil
		case "aborted":
			return status.Error(codes.FailedPrecondition, "migration was aborted")
		}
		when := s.now()
		if req.GetCutoverTime() != nil {
			when = req.GetCutoverTime().AsTime()
		}
		if !old.OpenEnded {
			return status.Error(codes.FailedPrecondition, "the subscription ended while the migration was waiting")
		}
		nr, err := s.cutover(ctx, q, ti.ID, old, when, mig.ToProvider, ptr(req.GetProviderRef()), "provider")
		if err != nil {
			return err
		}
		done, err := q.FinishProviderMigration(ctx, sqlc.FinishProviderMigrationParams{ID: id, State: "completed", NewSubscriptionID: &nr.ID, CutoverAt: &when})
		if err != nil {
			return err
		}
		nrow, err := q.GetSubscription(ctx, nr.ID)
		if err != nil {
			return err
		}
		resp.Migration, resp.Subscription = migrationMessage(done), subscriptionMessage(nrow, s.now())
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return &resp, nil
}

func (s *Server) AbortProviderMigration(ctx context.Context, req *plansv1.AbortProviderMigrationRequest) (*plansv1.AbortProviderMigrationResponse, error) {
	id, err := uuid.Parse(req.GetMigrationId())
	if err != nil {
		return nil, domain.Invalid("migration_id", "must be a UUID")
	}
	var out *plansv1.ProviderMigration
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		mig, err := q.GetProviderMigrationForUpdate(ctx, id)
		if err != nil {
			return err
		}
		switch mig.State {
		case "aborted":
			out = migrationMessage(mig)
			return nil
		case "completed":
			return status.Error(codes.FailedPrecondition, "the migration has completed; it cannot be aborted")
		}
		done, err := q.FinishProviderMigration(ctx, sqlc.FinishProviderMigrationParams{ID: id, State: "aborted", NewSubscriptionID: nil, CutoverAt: mig.CutoverAt})
		if err != nil {
			return err
		}
		out = migrationMessage(done)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return &plansv1.AbortProviderMigrationResponse{Migration: out}, nil
}

func (s *Server) GetProviderMigration(ctx context.Context, req *plansv1.GetProviderMigrationRequest) (*plansv1.GetProviderMigrationResponse, error) {
	id, err := uuid.Parse(req.GetMigrationId())
	if err != nil {
		return nil, domain.Invalid("migration_id", "must be a UUID")
	}
	var out *plansv1.ProviderMigration
	err = s.tx(ctx, func(q *sqlc.Queries) error {
		mig, err := q.GetProviderMigration(ctx, id)
		if err != nil {
			return err
		}
		out = migrationMessage(mig)
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "migration")
	}
	return &plansv1.GetProviderMigrationResponse{Migration: out}, nil
}
