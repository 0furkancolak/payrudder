// Package command changes a tenant on an operator's word: its settings (tier, grace period, invoice logo, seller identity, bank transfer) and its status.
// It runs on the system pool (role plans_system, BYPASSRLS) because an operator reaches any tenant.
package command

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/env"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	apikeyscmd "github.com/0furkancolak/payrudder/services/plans-svc/internal/apikeys/command"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/identity/usermail"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// Commands changes tenants.
type Commands struct {
	pool        *pgxpool.Pool
	keys        *apikeyscmd.Commands
	log         *slog.Logger
	banApproval bool
	mail        usermail.Sender // tells the owners of a status change; nil: nobody
	appURL      string
	now         func() time.Time
}

// New returns the tenant commands on the system pool. keys drops a suspended tenant's keys from the gateway cache.
func New(systemPool *pgxpool.Pool, keys *apikeyscmd.Commands, log *slog.Logger) *Commands {
	return &Commands{pool: systemPool, keys: keys, log: log, now: time.Now}
}

// WithBanApproval makes a ban need a second operator (PAYRUDDER_TENANT_BAN_APPROVAL; default off).
func (c *Commands) WithBanApproval(on bool) *Commands {
	c.banApproval = on
	return c
}

// UpdateSettings changes the settings the request names and leaves the others as they are.
func (c *Commands) UpdateSettings(ctx context.Context, req *plansv1.UpdateTenantSettingsRequest) (*plansv1.Tenant, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	params := sqlc.UpdateTenantSettingsParams{ID: id}
	for _, set := range []func(*plansv1.UpdateTenantSettingsRequest, *sqlc.UpdateTenantSettingsParams) error{
		setGrace, setTier, setLogo, setSeller, setEInvoice, setDefaultLanguage, setBankTransfer,
	} {
		if err := set(req, &params); err != nil {
			return nil, err
		}
	}
	var t sqlc.Tenant
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		if t, err = sqlc.New(tx).UpdateTenantSettings(ctx, params); err != nil {
			return err
		}
		fields := changedFields(req)
		return audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "tenant.settings_updated", TargetType: "tenant", TargetID: id.String(),
			Summary: "changed the tenant settings: " + strings.Join(fields, ", "), Detail: map[string]any{"fields": fields}})
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	return convert.Tenant(t), nil
}

// changedFields names the settings a request carries; their values (seller identity, bank details) stay out of the audit log.
func changedFields(req *plansv1.UpdateTenantSettingsRequest) []string {
	var names []string
	req.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if fd.Name() != "tenant_id" {
			names = append(names, string(fd.Name()))
		}
		return true
	})
	slices.Sort(names)
	return names
}

func setGrace(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	if req.GetGracePeriod() == nil {
		p.GracePeriod = pgtype.Interval{}
		return nil
	}
	g := req.GetGracePeriod().AsDuration()
	if err := domain.ValidateGrace(g); err != nil {
		return domain.Invalid("grace_period", err.Error())
	}
	p.GracePeriod = convert.Interval(g)
	return nil
}

func setTier(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	if req.GetTier() == plansv1.TenantTier_TENANT_TIER_UNSPECIFIED {
		return nil
	}
	name, ok := domain.TierToDB(req.GetTier())
	if !ok {
		return domain.Invalid("tier", "unknown tier")
	}
	p.Tier = &name
	return nil
}

func setLogo(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	p.ClearLogo = req.GetClearInvoiceLogo()
	logo := req.GetInvoiceLogoAssetId()
	if logo == "" {
		return nil
	}
	if req.GetClearInvoiceLogo() {
		return domain.Invalid("clear_invoice_logo", "cannot be combined with invoice_logo_asset_id")
	}
	lid, err := domain.ParseID("invoice_logo_asset_id", logo)
	if err != nil {
		return err
	}
	p.InvoiceLogoAssetID = &lid
	return nil
}

func setSeller(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	if req.GetAddress() != nil {
		if err := domain.ValidateAddress("address", req.GetAddress()); err != nil {
			return err
		}
		p.Address = convert.AddressJSON(req.GetAddress())
	}
	if req.GetLegal() != nil {
		if err := domain.ValidateLegal(req.GetLegal()); err != nil {
			return err
		}
		p.Legal = convert.LegalJSON(req.GetLegal())
	}
	return nil
}

func setDefaultLanguage(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	v := req.GetDefaultLanguage()
	if err := domain.ValidateDefaultLanguage(v); err != nil || v == "" {
		return err
	}
	p.DefaultLanguage = &v
	return nil
}

func setEInvoice(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	v := req.GetEInvoiceOn()
	if v == "" {
		return nil
	}
	if err := domain.ValidateEInvoiceOn(v); err != nil {
		return err
	}
	p.EInvoiceOn = &v
	return nil
}

func setBankTransfer(req *plansv1.UpdateTenantSettingsRequest, p *sqlc.UpdateTenantSettingsParams) error {
	p.ClearBankTransfer = req.GetClearBankTransfer()
	d := req.GetBankTransfer()
	if d == nil {
		return nil
	}
	if req.GetClearBankTransfer() {
		return domain.Invalid("clear_bank_transfer", "cannot be combined with bank_transfer")
	}
	if err := domain.ValidateBankTransfer("bank_transfer", d); err != nil {
		return err
	}
	p.BankTransfer = convert.BankTransferJSON(d)
	return nil
}

// StatusCategories are the reasons a change of status can name. Empty is allowed: none given.
var StatusCategories = []string{"nonpayment", "fraud", "abuse", "legal", "request", "other"}

// approvalWindow is how long a request for a ban waits for a second operator.
const approvalWindow = 24 * time.Hour

// maxActorRunes bounds the actor of a change, as the table does.
const maxActorRunes = 120

// held lists the statuses an operator puts a live tenant in to take it out of service.
func held(status string) bool {
	return status == domain.StatusSuspended || status == domain.StatusFrozen || status == domain.StatusBanned
}

// SetStatus is the operator's hand on a live tenant's life: suspend, freeze or ban an active tenant, reinstate one that was, move between those, or activate an
// inactive one without its checklist. A ban, and leaving a ban, needs a reason; so does activating an inactive tenant. Every change is recorded with its category,
// its reason and who made it. Suspending or banning evicts every key of the tenant from the gateway cache at once; a frozen tenant's keys stay, read-only. Where the
// ban approval is on, asking for a ban only records the request (plans.tenant.approval_pending); the same request from another operator within a day applies it.
func (c *Commands) SetStatus(ctx context.Context, req *plansv1.SetTenantStatusRequest) (*plansv1.Tenant, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	want, ok := domain.StatusToDB(req.GetStatus())
	if !ok || want == domain.StatusInactive {
		return nil, errkey.New(codes.InvalidArgument, "plans.tenant.bad_status", "status: active, suspended, frozen or banned")
	}
	reason := strings.TrimSpace(req.GetReason())
	if utf8.RuneCountInString(reason) > 500 {
		return nil, domain.Invalid("reason", "at most 500 characters")
	}
	if cat := req.GetCategory(); cat != "" && !slices.Contains(StatusCategories, cat) {
		return nil, domain.Invalid("category", "nonpayment, fraud, abuse, legal, request or other")
	}
	actor := strings.TrimSpace(req.GetActor())
	if actor == "" {
		actor = "operator"
	}
	if r := []rune(actor); len(r) > maxActorRunes {
		actor = string(r[:maxActorRunes])
	}
	var (
		out      sqlc.Tenant
		from     string
		pending  bool
		approver string
	)
	err = pgxx.InTx(ctx, c.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		cur, err := q.LockTenantForGoLive(ctx, id)
		if err != nil {
			return err
		}
		from = cur.Status
		switch {
		case cur.Environment != env.Live.String():
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "a sandbox tenant is always active")
		case cur.Status == want:
			out = cur
			return nil
		case held(want) && cur.Status == domain.StatusInactive:
			return errkey.New(codes.FailedPrecondition, "plans.tenant.bad_transition", "only a live tenant that is in service can be suspended, frozen or banned")
		case reason == "" && (want == domain.StatusBanned || cur.Status == domain.StatusBanned || (want == domain.StatusActive && cur.Status == domain.StatusInactive)):
			return errkey.New(codes.InvalidArgument, "plans.tenant.reason_required", "a ban, lifting a ban and activating a tenant without its checklist need a reason")
		}
		if want == domain.StatusBanned && c.banApproval {
			approver, pending, err = c.approval(ctx, q, id, actor)
			if err != nil {
				return err
			}
			if pending {
				if err := q.InsertTenantStatusEvent(ctx, sqlc.InsertTenantStatusEventParams{ID: idgen.NewUUIDv7(), LiveTenantID: id, Kind: "requested", FromStatus: cur.Status, ToStatus: want,
					Category: req.GetCategory(), Reason: reason, Actor: actor}); err != nil {
					return err
				}
				return audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "tenant.status_requested", TargetType: "tenant", TargetID: id.String(),
					Summary: "asked for the status " + want + " (waits for a second operator)", Detail: map[string]any{"from": cur.Status, "to": want, "category": req.GetCategory(), "reason": reason}})
			}
		}
		if out, err = q.SetTenantStatus(ctx, sqlc.SetTenantStatusParams{ID: id, Status: want}); err != nil {
			return err
		}
		if err := q.InsertTenantStatusEvent(ctx, sqlc.InsertTenantStatusEventParams{ID: idgen.NewUUIDv7(), LiveTenantID: id, Kind: "changed", FromStatus: cur.Status, ToStatus: want,
			Category: req.GetCategory(), Reason: reason, Actor: actor, ApprovedBy: approver}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: id, Action: "tenant.status_changed", TargetType: "tenant", TargetID: id.String(),
			Summary: "changed the status from " + cur.Status + " to " + want, Detail: map[string]any{"from": cur.Status, "to": want, "category": req.GetCategory(), "reason": reason}})
	})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	if pending {
		return nil, errkey.New(codes.FailedPrecondition, "plans.tenant.approval_pending", "a ban needs a second operator: the request is recorded, ask another operator to make the same request")
	}
	c.log.WarnContext(ctx, "tenant status set by an operator", "tenant_id", id, "from", from, "status", want, "category", req.GetCategory(), "reason", reason, "actor", actor)
	if out.Status != from {
		// Whatever the change, the gateway resolves the keys of the tenant again: a status it had cached is not the status any more.
		if err := c.keys.EvictTenant(ctx, id); err != nil {
			return nil, err
		}
		c.notifyOwners(ctx, id, from, out.Status)
	}
	return convert.Tenant(out), nil
}

// approval decides whether a ban the actor asks for is the second request for it: the latest event of the tenant is another operator's request for a ban, made
// within the approval window. It returns that operator, or says that this request waits.
func (c *Commands) approval(ctx context.Context, q *sqlc.Queries, id uuid.UUID, actor string) (approver string, pending bool, err error) {
	last, err := q.LatestTenantStatusEvent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", true, nil
	}
	if err != nil {
		return "", false, err
	}
	if last.Kind == "requested" && last.ToStatus == domain.StatusBanned && last.Actor != actor && c.now().Sub(last.CreatedAt) < approvalWindow {
		return last.Actor, false, nil
	}
	return "", true, nil
}

// StatusEvents returns the changes of a tenant's status and the requests that wait for a second operator, newest first, the latest 100.
func (c *Commands) StatusEvents(ctx context.Context, req *plansv1.ListTenantStatusEventsRequest) (*plansv1.ListTenantStatusEventsResponse, error) {
	id, err := domain.ParseID("tenant_id", req.GetTenantId())
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(c.pool).ListTenantStatusEvents(ctx, sqlc.ListTenantStatusEventsParams{LiveTenantID: id, PageSize: 100})
	if err != nil {
		return nil, convert.Err(err, "tenant")
	}
	out := &plansv1.ListTenantStatusEventsResponse{Events: make([]*plansv1.TenantStatusEvent, 0, len(rows))}
	for _, e := range rows {
		out.Events = append(out.Events, &plansv1.TenantStatusEvent{Id: e.ID.String(), Kind: e.Kind, FromStatus: domain.StatusFromDB(e.FromStatus), ToStatus: domain.StatusFromDB(e.ToStatus),
			Category: e.Category, Reason: e.Reason, Actor: e.Actor, ApprovedBy: e.ApprovedBy, CreateTime: timestamppb.New(e.CreatedAt)})
	}
	return out, nil
}
