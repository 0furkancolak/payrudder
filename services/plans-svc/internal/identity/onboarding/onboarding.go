package onboarding

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Service reads and writes the onboarding record of a person on the system pool.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

// New returns the onboarding service.
func New(systemPool *pgxpool.Pool) *Service { return &Service{pool: systemPool, now: time.Now} }

func shape(row sqlc.UserOnboarding) (*plansv1.Onboarding, error) {
	out := &plansv1.Onboarding{SeenTours: row.SeenTours}
	if len(row.Wizard) > 0 {
		out.Wizard = &plansv1.OnboardingWizard{}
		if err := protojson.Unmarshal(row.Wizard, out.Wizard); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get returns the person's record; a person who has none yet has an empty one.
func (s *Service) Get(ctx context.Context, userID string) (*plansv1.Onboarding, error) {
	uid, err := domain.ParseID("user_id", userID)
	if err != nil {
		return nil, err
	}
	row, err := sqlc.New(s.pool).GetUserOnboarding(ctx, uid)
	if errors.Is(err, pgx.ErrNoRows) {
		return &plansv1.Onboarding{SeenTours: []string{}}, nil
	}
	if err != nil {
		return nil, convert.Err(err, "onboarding")
	}
	out, err := shape(row)
	return out, convert.Err(err, "onboarding")
}

// Update merges the change into the person's record and returns the result. The row is locked while it is merged, so two writers take turns.
func (s *Service) Update(ctx context.Context, req *plansv1.UpdateMyOnboardingRequest) (*plansv1.Onboarding, error) {
	uid, err := domain.ParseID("user_id", req.GetUserId())
	if err != nil {
		return nil, err
	}
	var out *plansv1.Onboarding
	err = pgxx.InTx(ctx, s.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if err := q.EnsureUserOnboarding(ctx, uid); err != nil {
			return err
		}
		row, err := q.LockUserOnboarding(ctx, uid)
		if err != nil {
			return err
		}
		cur, err := shape(row)
		if err != nil {
			return err
		}
		tours, err := MergeTours(cur.GetSeenTours(), req.GetSeenTours(), req.GetForgetTours())
		if err != nil {
			return err
		}
		wizard := cur.GetWizard()
		if req.GetWizard() != nil {
			if wizard, err = MergeWizard(wizard, req.GetWizard()); err != nil {
				return err
			}
		}
		var doc []byte
		if wizard != nil {
			// The names of the API ("seen_tours" style), so what is stored reads like what is sent.
			if doc, err = (protojson.MarshalOptions{UseProtoNames: true}).Marshal(wizard); err != nil {
				return err
			}
		}
		if err := q.SaveUserOnboarding(ctx, sqlc.SaveUserOnboardingParams{UserID: uid, Wizard: doc, SeenTours: tours, UpdatedAt: s.now()}); err != nil {
			return err
		}
		out = &plansv1.Onboarding{Wizard: wizard, SeenTours: tours}
		return nil
	})
	if err != nil {
		return nil, convert.Err(err, "onboarding")
	}
	return out, nil
}
