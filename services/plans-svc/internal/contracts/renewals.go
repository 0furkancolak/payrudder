package contracts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/events/v1"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/money"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	couponsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/coupons/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/placement"
	plansqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/plans/query"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
	subsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/subscriptions/query"
	tenantsqry "github.com/0furkancolak/payrudder/services/plans-svc/internal/tenants/query"
)

// RenewalTopic is the Kafka topic of the RenewalNotice fact, written to the outbox and relayed like the trial reminder's.
const RenewalTopic = "subscription.renewal-notice.v1"

// Renewer tells a customer who accepted the seller's agreements that their subscription renews soon: once per renewal, a number of days before it that the seller chose
// (7 unless it did; 0 switches the notice off), with what the next period costs, the date and the seller's own words on how to cancel (docs/plans/21-contracts.md).
//
// Only a subscription whose fixed fee is invoiced in advance has a renewal moment (fee_due_at); one billed in arrears has no date and a usage-dependent price, and a
// provider's subscription is the provider's to announce, so neither is noticed. A renewal is never held back by any of this: the notice is information, and the charge
// does not wait for an answer.
//
// Like the trial reminder, finding the candidates reads across tenants on the system role (and with cells on, in each tenant's schema), and each notice is recorded in its
// tenant's own transaction together with the fact the notifier mails, so the row, and the mail, exist together or not at all.
type Renewer struct {
	places *placement.Places
	app    pgxx.Beginner
	every  time.Duration
	batch  int
	now    func() time.Time
	log    *slog.Logger
}

// NewRenewer returns the job; app is the app pool (or its router when cells are on).
func NewRenewer(places *placement.Places, app pgxx.Beginner, log *slog.Logger) *Renewer {
	return &Renewer{places: places, app: app, every: 10 * time.Minute, batch: 200, now: time.Now, log: log}
}

// WithClock sets the clock the job reads (tests).
func (r *Renewer) WithClock(now func() time.Time) *Renewer {
	r.now = now
	return r
}

// Once notices the renewals that are due, at most one batch per place, and says how many it noticed and how many it looked at. A subscription that fails is logged and left
// for the next pass.
func (r *Renewer) Once(ctx context.Context) (noticed, looked int, err error) {
	now := r.now()
	list := func(db sqlc.DBTX) ([]sqlc.SystemListRenewalsDueRow, error) {
		return sqlc.New(db).SystemListRenewalsDue(ctx, sqlc.SystemListRenewalsDueParams{Owner: domain.OwnerPayrudder, FromTime: now, WindowTime: now, Batch: int32(r.batch)}) //nolint:gosec // a small constant
	}
	due, err := list(r.places.Pool())
	if err != nil {
		return 0, 0, err
	}
	noticed, looked = r.noticeAll(ctx, now, due)
	err = r.places.EachCell(ctx, func(ctx context.Context, t pgxx.PlacedTenant) error {
		var due []sqlc.SystemListRenewalsDueRow
		if err := r.places.CellTx(ctx, t, func(tx pgx.Tx) (err error) {
			due, err = list(tx)
			return err
		}); err != nil {
			return err
		}
		for _, d := range due {
			if d.TenantID != t.TenantID {
				return fmt.Errorf("renewal notice: subscription %s of tenant %s is in the schema of tenant %s", d.ID, d.TenantID, t.TenantID)
			}
		}
		n, l := r.noticeAll(ctx, now, due)
		noticed, looked = noticed+n, looked+l
		return nil
	})
	return noticed, looked, err
}

func (r *Renewer) noticeAll(ctx context.Context, now time.Time, due []sqlc.SystemListRenewalsDueRow) (noticed, looked int) {
	for _, d := range due {
		ok, err := r.notice(ctx, now, d)
		if err != nil {
			r.log.WarnContext(ctx, "renewal notice: could not notice", "tenant_id", d.TenantID, "subscription_id", d.ID, "error", err)
			continue
		}
		if ok {
			noticed++
		}
	}
	return noticed, len(due)
}

// RenewalEventID is the id of the notice of one renewal of one customer's subscription: the same renewal always gives the same id.
func RenewalEventID(tenantID, customerID uuid.UUID, renewsAt time.Time) uuid.UUID {
	return idgen.Deterministic("renewal_notice", tenantID.String(), customerID.String(), renewsAt.UTC().Format(time.RFC3339Nano))
}

// notice records the notice of one renewal and queues its fact, or does nothing when the renewal is not due any more or was noticed already. It says whether it noticed.
func (r *Renewer) notice(ctx context.Context, now time.Time, d sqlc.SystemListRenewalsDueRow) (bool, error) {
	done := false
	err := pgxx.InTenantTx(ctx, r.app, d.TenantID, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		// The row is locked first: a cancellation, a pause or a plan change waits for this transaction, and a second instance waits here, then finds the notice.
		row, err := subsqry.Lock(ctx, q, d.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			return err
		}
		feeDue, err := q.GetSubscriptionFeeDue(ctx, row.ID)
		if err != nil {
			return err
		}
		settings, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		// The subscription was changed since the list was read: another pass lists it again as it is now.
		if row.State != "active" || !row.OpenEnded || row.CancelAtPeriodEnd || row.Owner != domain.OwnerPayrudder || row.FeeTiming != "advance" || feeDue == nil || !feeDue.Equal(d.RenewsAt) ||
			settings.RenewalNoticeDays == 0 || !feeDue.After(now) || feeDue.After(now.Add(time.Duration(settings.RenewalNoticeDays)*24*time.Hour)) {
			return nil
		}
		cust, err := q.GetCustomerByID(ctx, row.CustomerID)
		if err != nil {
			return err
		}
		tn, err := tenantsqry.Current(ctx, q)
		if err != nil {
			return err
		}
		fee, err := plansqry.SubscriptionFee(ctx, q, tn.Currency, row)
		if err != nil {
			return err
		}
		discount, _, err := couponsqry.Discount(ctx, q, row.ID, row.Currency, fee)
		if err != nil {
			return err
		}
		price := fee - discount
		text, _ := domain.PickContractBody(bodiesOf(settings.RenewalCancelTexts), cust.Locale)
		n, err := q.InsertContractRenewalNotice(ctx, sqlc.InsertContractRenewalNoticeParams{CustomerID: row.CustomerID, RenewsAt: *feeDue, SubscriptionID: row.ID, PriceMinor: price,
			Currency: row.Currency, CancelText: text, NotifiedAt: now})
		if err != nil || n == 0 {
			return err
		}
		exp := 2
		if cur, ok := money.LookupCurrency(row.Currency); ok {
			exp = cur.Exponent
		}
		payload, err := proto.Marshal(&eventsv1.RenewalNotice{EventId: RenewalEventID(d.TenantID, row.CustomerID, *feeDue).String(), TenantId: d.TenantID.String(), CustomerId: row.CustomerID.String(),
			SubscriptionId: row.ID.String(), PlanCode: row.PlanCode, RenewsAt: timestamppb.New(*feeDue), PriceMinor: price, Currency: row.Currency, CurrencyExponent: int32(exp), //nolint:gosec // an exponent
			Interval: row.Interval, CancelText: text, DetectedAt: timestamppb.New(now)})
		if err != nil {
			return err
		}
		if err := q.InsertOutbox(ctx, sqlc.InsertOutboxParams{Topic: RenewalTopic, PartitionKey: d.TenantID.String() + "/" + row.CustomerID.String(), Payload: payload}); err != nil {
			return err
		}
		done = true
		return q.NotifyOutbox(ctx)
	})
	return done && err == nil, err
}

// Run looks now and then every interval until ctx ends. A pass that fills a whole batch runs again at once.
func (r *Renewer) Run(ctx context.Context) error {
	t := time.NewTicker(r.every)
	defer t.Stop()
	for {
		for {
			n, looked, err := r.Once(ctx)
			if err != nil {
				r.log.ErrorContext(ctx, "renewal notice: listing the renewals failed", "error", err)
			} else if n > 0 {
				r.log.InfoContext(ctx, "renewal notices recorded", "count", n)
			}
			// Another pass only when this one made progress through a full batch: subscriptions that keep failing must not make it spin.
			if err != nil || n == 0 || looked < r.batch || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
