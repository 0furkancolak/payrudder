// Package convert maps store rows to API messages and store errors to gRPC status codes.
package convert

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/subscription"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Err maps store errors to status errors. Status errors pass through unchanged; unknown errors become Internal
// without leaking SQL details. what names the entity for NotFound/AlreadyExists messages.
func Err(err error, what string) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return errkey.Newf(codes.NotFound, "plans.common.not_found", "%s not found", what)
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded")
	}
	switch pgxx.ErrCode(err) {
	case pgxx.CodeUniqueViolation:
		return errkey.Newf(codes.AlreadyExists, "plans.common.already_exists", "%s already exists", what)
	case pgxx.CodeExclusionViolation:
		return errkey.New(codes.AlreadyExists, "plans.subscription.period_overlap", "customer already has a subscription overlapping that period")
	case pgxx.CodeForeignKeyViolation:
		return errkey.Newf(codes.FailedPrecondition, "plans.common.broken_reference", "%s refers to something that doesn't exist", what)
	case pgxx.CodeRaiseException:
		return errkey.New(codes.FailedPrecondition, "plans.tenant.settings_immutable", "currency, timezone and environment of a tenant are immutable")
	case pgxx.CodeInsufficientPriv:
		return errkey.New(codes.PermissionDenied, "plans.common.forbidden", "not allowed")
	}
	return status.Error(codes.Internal, "internal error")
}

// Interval converts a duration to a Postgres interval (microseconds only, so it round-trips exactly).
func Interval(d time.Duration) pgtype.Interval {
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// Duration converts a Postgres interval back. Months are not used by Payrudder and count as 30 days.
func Duration(iv pgtype.Interval) time.Duration {
	return time.Duration(iv.Microseconds)*time.Microsecond +
		time.Duration(iv.Days)*24*time.Hour + time.Duration(iv.Months)*30*24*time.Hour
}

func ts(t time.Time) *timestamppb.Timestamp { return timestamppb.New(t) }

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// Tenant maps a tenant row.
func Tenant(t sqlc.Tenant) *plansv1.Tenant {
	return &plansv1.Tenant{
		Id: t.ID.String(), AccountId: t.AccountID.String(), Environment: domain.EnvironmentFromDB(t.Environment),
		Name: t.Name, Tier: domain.TierFromDB(t.Tier), Currency: t.Currency, Timezone: t.Timezone,
		GracePeriod: durationpb.New(Duration(t.GracePeriod)), CreateTime: ts(t.CreatedAt),
		InvoiceLogoAssetId: logoID(t.InvoiceLogoAssetID), AppId: t.AppID.String(), Address: AddressFromJSON(t.Address),
		Legal: LegalFromJSON(t.Legal), EInvoiceOn: t.EInvoiceOn, BankTransfer: BankTransferFromJSON(t.BankTransfer),
		Status: domain.StatusFromDB(t.Status), ActivateTime: tsp(t.ActivatedAt), TermsAcceptTime: tsp(t.TermsAcceptedAt),
	}
}

func tsp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// BankTransferJSON is the stored form of the bank transfer details: a JSON object without the empty parts.
func BankTransferJSON(d *plansv1.BankTransferDetails) []byte {
	if d == nil {
		return []byte("{}")
	}
	b, err := addressJSON.Marshal(d)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// BankTransferFromJSON reads stored bank transfer details; nil when there are none.
func BankTransferFromJSON(raw []byte) *plansv1.BankTransferDetails {
	var d plansv1.BankTransferDetails
	if len(raw) == 0 || protojson.Unmarshal(raw, &d) != nil || proto.Equal(&d, &plansv1.BankTransferDetails{}) {
		return nil
	}
	return &d
}

// App with its tenants (sandbox first).
func App(a sqlc.App, tenants []sqlc.Tenant) *plansv1.App {
	out := &plansv1.App{Id: a.ID.String(), AccountId: a.AccountID.String(), Name: a.Name, Slug: a.Slug, CreateTime: ts(a.CreatedAt)}
	for _, t := range tenants {
		out.Tenants = append(out.Tenants, Tenant(t))
	}
	return out
}

// Apps maps the apps of an account, each with the tenants that belong to it (in the order given).
func Apps(apps []sqlc.App, tenants []sqlc.Tenant) []*plansv1.App {
	byApp := make(map[uuid.UUID][]sqlc.Tenant, len(apps))
	for _, t := range tenants {
		byApp[t.AppID] = append(byApp[t.AppID], t)
	}
	out := make([]*plansv1.App, 0, len(apps))
	for _, a := range apps {
		out = append(out, App(a, byApp[a.ID]))
	}
	return out
}

// APIKeyEnvironment maps the key env ("test"/"live") to the tenant environment.
func APIKeyEnvironment(env string) plansv1.Environment {
	if env == "test" {
		return plansv1.Environment_ENVIRONMENT_SANDBOX
	}
	return plansv1.Environment_ENVIRONMENT_LIVE
}

// APIKeyStatus maps a stored key status.
func APIKeyStatus(s string) plansv1.ApiKey_Status {
	if s == "active" {
		return plansv1.ApiKey_STATUS_ACTIVE
	}
	return plansv1.ApiKey_STATUS_REVOKED
}

// APIKey maps a key row (never the hash).
func APIKey(k sqlc.ApiKey) *plansv1.ApiKey {
	out := &plansv1.ApiKey{
		Id: k.ID.String(), TenantId: k.TenantID.String(), Prefix: k.Prefix,
		Environment: APIKeyEnvironment(k.Env), Status: APIKeyStatus(k.Status), CreateTime: ts(k.CreatedAt),
		Scope: domain.ScopeFromDB(k.Scope), LastUsedTime: tsp(k.LastUsedAt), ExpireTime: tsp(k.ExpiresAt),
	}
	if k.RevokedAt != nil {
		out.RevokeTime = ts(*k.RevokedAt)
	}
	return out
}

// Customer maps a customer row.
func Customer(c sqlc.Customer) *plansv1.Customer {
	return &plansv1.Customer{
		Id: c.ID.String(), ExternalId: c.ExternalID, Name: c.Name, Email: c.Email,
		PaymentMethod: str(c.PaymentMethod), CreateTime: ts(c.CreatedAt),
		Country: str(c.Country), BillingCurrency: str(c.BillingCurrency), Address: AddressFromJSON(c.Address),
		TaxId: c.TaxID, TaxOffice: c.TaxOffice, EInvoiceRegistered: c.EInvoiceRegistered,
	}
}

var addressJSON = protojson.MarshalOptions{UseProtoNames: true}

// AddressJSON is the stored form of an address: a JSON object without the empty parts.
func AddressJSON(a *plansv1.Address) []byte {
	if a == nil {
		return []byte("{}")
	}
	b, err := addressJSON.Marshal(a)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// AddressFromJSON reads a stored address; nil when there is none.
func AddressFromJSON(raw []byte) *plansv1.Address {
	var a plansv1.Address
	if len(raw) == 0 || protojson.Unmarshal(raw, &a) != nil || proto.Equal(&a, &plansv1.Address{}) {
		return nil
	}
	return &a
}

// LegalJSON is the stored form of the seller's company data.
func LegalJSON(l *plansv1.LegalInfo) []byte {
	if l == nil {
		return []byte("{}")
	}
	b, err := addressJSON.Marshal(l)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// LegalFromJSON reads stored company data; nil when there is none.
func LegalFromJSON(raw []byte) *plansv1.LegalInfo {
	var l plansv1.LegalInfo
	if len(raw) == 0 || protojson.Unmarshal(raw, &l) != nil || proto.Equal(&l, &plansv1.LegalInfo{}) {
		return nil
	}
	return &l
}

// Meter maps a meter row.
func Meter(m sqlc.Meter) (*plansv1.Meter, error) {
	filter, err := domain.FilterFromDB(m.Filter)
	if err != nil {
		return nil, err
	}
	return &plansv1.Meter{
		Code: m.Code, Version: m.Version, Aggregation: domain.AggregationFromDB(m.Aggregation),
		ValueProperty: str(m.ValueProperty), Filter: filter, QuantityScale: uint32(max(m.QuantityScale, 0)), //nolint:gosec // 0..6 by CHECK
		UnitLabel: m.UnitLabel, CreateTime: ts(m.CreatedAt),
	}, nil
}

// Plan maps a plan row with its latest version number.
func Plan(id, code, name string, created time.Time, latest int32) *plansv1.Plan {
	return &plansv1.Plan{Id: id, Code: code, Name: name, LatestVersion: latest, CreateTime: ts(created)}
}

// SubscriptionFields is the common shape of the subscription query rows.
type SubscriptionFields struct {
	ID, CustomerID, PlanVersionID string
	CustomerExternalID            string
	Start, End, Created           time.Time
	OpenEnded                     bool
	State, Interval, Currency     string
	TrialEnds, PausedAt           *time.Time
	CancelAtPeriodEnd             bool
	ChangedFromID                 string
	Provider, ProviderRef, Owner  string
	PlanCode                      string
	PlanVersion                   int32
	FeeTiming                     string
	// Now decides the effective state: a trial that is over reads as active, an ended subscription as cancelled.
	Now time.Time
}

// SubscriptionState returns the state a subscription is in at now. Trials and scheduled ends run out by themselves, so
// the stored state is only brought up to date when something else happens to the subscription.
func SubscriptionState(stored string, trialEnds *time.Time, openEnded bool, end, now time.Time) subscription.State {
	st := subscription.State(stored)
	if !openEnded && !end.After(now) {
		return subscription.Cancelled
	}
	if st == subscription.Trialing && trialEnds != nil && !now.Before(*trialEnds) {
		return subscription.Active
	}
	return st
}

var stateToProto = map[subscription.State]plansv1.SubscriptionState{
	subscription.Trialing:  plansv1.SubscriptionState_SUBSCRIPTION_STATE_TRIALING,
	subscription.Active:    plansv1.SubscriptionState_SUBSCRIPTION_STATE_ACTIVE,
	subscription.PastDue:   plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAST_DUE,
	subscription.Paused:    plansv1.SubscriptionState_SUBSCRIPTION_STATE_PAUSED,
	subscription.Cancelled: plansv1.SubscriptionState_SUBSCRIPTION_STATE_CANCELLED,
}

func feeTimingToProto(t string) plansv1.FeeTiming {
	if t == "advance" {
		return plansv1.FeeTiming_FEE_TIMING_ADVANCE
	}
	return plansv1.FeeTiming_FEE_TIMING_ARREARS
}

// Subscription maps subscription fields.
func Subscription(s SubscriptionFields) *plansv1.Subscription {
	now := s.Now
	if now.IsZero() {
		now = time.Now()
	}
	out := &plansv1.Subscription{
		Id: s.ID, CustomerId: s.CustomerID, CustomerExternalId: s.CustomerExternalID, PlanVersionId: s.PlanVersionID,
		StartTime: ts(s.Start), CreateTime: ts(s.Created), Currency: s.Currency, CancelAtPeriodEnd: s.CancelAtPeriodEnd, ChangedFromId: s.ChangedFromID, Provider: s.Provider, ProviderRef: s.ProviderRef, Owner: ownerOrDefault(s.Owner), PlanCode: s.PlanCode, PlanVersion: s.PlanVersion,
		FeeTiming: feeTimingToProto(s.FeeTiming),
		State:     stateToProto[SubscriptionState(s.State, s.TrialEnds, s.OpenEnded, s.End, now)],
	}
	if s.Interval == string(subscription.Yearly) {
		out.Interval = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_YEAR
	} else {
		out.Interval = plansv1.SubscriptionInterval_SUBSCRIPTION_INTERVAL_MONTH
	}
	if s.TrialEnds != nil {
		out.TrialEndTime = ts(*s.TrialEnds)
	}
	if s.PausedAt != nil {
		out.PauseTime = ts(*s.PausedAt)
	}
	if !s.OpenEnded {
		out.EndTime = ts(s.End)
	}
	return out
}

func ownerOrDefault(o string) string {
	if o == "" {
		return "quotify"
	}
	return o
}

// SubscriptionOf maps a subscription row to its API message, with the state it has at now.
func SubscriptionOf(r sqlc.GetSubscriptionRow, now time.Time) *plansv1.Subscription {
	return Subscription(SubscriptionFields{
		ID: r.ID.String(), CustomerID: r.CustomerID.String(), CustomerExternalID: r.CustomerExternalID,
		PlanVersionID: r.PlanVersionID.String(), Start: r.StartTime, End: r.EndTime, OpenEnded: r.OpenEnded, Created: r.CreatedAt,
		State: r.State, Interval: r.Interval, Currency: r.Currency, TrialEnds: r.TrialEndsAt, PausedAt: r.PausedAt,
		CancelAtPeriodEnd: r.CancelAtPeriodEnd, ChangedFromID: UUIDStr(r.ChangedFromID), Provider: Str(r.Provider), ProviderRef: Str(r.ProviderRef), Owner: r.Owner, PlanCode: r.PlanCode, PlanVersion: r.PlanVersion, FeeTiming: r.FeeTiming, Now: now,
	})
}
