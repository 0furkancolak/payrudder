// Package blocklist keeps the two lists that stop a person the seller (or the operator) has turned away from coming back under another customer id (plan 13): the
// app's own, and the operator's platform-wide one. An entry is an e-mail address, a tax id or an external customer id. The lists are checked where a customer is made
// or changes the e-mail address or the tax id; customers that exist when an entry is added are not touched (block them from the customer list).
package blocklist

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/audit"
	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/idgen"
	"github.com/0furkancolak/payrudder/pkg/pgxx"
	"github.com/0furkancolak/payrudder/pkg/taxid"
	"github.com/0furkancolak/payrudder/pkg/tenant"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/convert"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/domain"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store"
	"github.com/0furkancolak/payrudder/services/plans-svc/internal/store/sqlc"
)

// MaxEntries is how many entries a list holds: a list is hand-kept, so it is never paged, and a list that outgrows this needs a different tool.
const MaxEntries = 1000

// Kinds are the kinds of entry. A card fingerprint is not one: Payrudder never holds one (UNVERIFIED for every provider; docs/plans/13-customer-ban.md).
var Kinds = []string{"email", "tax_id", "external_id"}

const maxValue = 320

// Normalize returns the stored form of a value: an e-mail address in lower case, a tax id as pkg/taxid reads it (no spaces or dashes, capitals), an external id as it is
// (trimmed). Only the form is checked: an e-mail address needs an @, the rest any text up to 320 characters.
func Normalize(kind, value string) (string, error) {
	v := strings.TrimSpace(value)
	switch kind {
	case "email":
		v = strings.ToLower(v)
		if !strings.Contains(v, "@") || strings.ContainsAny(v, " \t\r\n") {
			return "", domain.Invalid("value", "an e-mail address")
		}
	case "tax_id":
		v = taxid.Normalize(v)
	case "external_id":
	default:
		return "", domain.Invalid("kind", "email, tax_id or external_id")
	}
	if v == "" || len([]rune(v)) > maxValue {
		return "", domain.Invalid("value", "required, at most 320 characters")
	}
	return v, nil
}

// Subject is what a customer is checked by: any field may be empty (nothing to match).
type Subject struct{ Email, TaxID, ExternalID string }

func (s Subject) normalized() (email, tax, ext string) {
	email = strings.ToLower(strings.TrimSpace(s.Email))
	tax = taxid.Normalize(strings.TrimSpace(s.TaxID))
	return email, tax, strings.TrimSpace(s.ExternalID)
}

type skipPlatformKey struct{}

// WithoutPlatform makes the checks of ctx skip the operator's list: the platform's own tenant makes its SaaS customers (the organisations) with it, and a person the operator
// banned must not stop the billing of their organisation.
func WithoutPlatform(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipPlatformKey{}, true)
}

// Lists is the blocklists of plans-svc: the app's own, read in the caller's transaction, and the operator's, read on the system pool (the pooled database holds it for
// ever, and a tenant in a cell cannot see it from its own transaction).
type Lists struct {
	db     *store.DB
	system *pgxpool.Pool
}

// New returns the blocklists.
func New(db *store.DB, systemPool *pgxpool.Pool) *Lists { return &Lists{db: db, system: systemPool} }

// ErrBlocklisted is the refusal: it does not say which list, or which entry.
func ErrBlocklisted() error {
	return errkey.New(codes.FailedPrecondition, "plans.customer.blocklisted", "the customer matches a blocklist")
}

// Check refuses a subject that matches an entry of the app's list (in the caller's transaction) or of the platform's. Fields that are empty match nothing.
func (l *Lists) Check(ctx context.Context, q *sqlc.Queries, s Subject) error {
	email, tax, ext := s.normalized()
	if email == "" && tax == "" && ext == "" {
		return nil
	}
	own, err := q.BlocklistMatches(ctx, sqlc.BlocklistMatchesParams{Email: email, TaxID: tax, ExternalID: ext})
	if err != nil {
		return err
	}
	if len(own) > 0 {
		return ErrBlocklisted()
	}
	if skip, _ := ctx.Value(skipPlatformKey{}).(bool); skip || l.system == nil {
		return nil
	}
	global, err := sqlc.New(l.system).PlatformBlocklistMatches(ctx, sqlc.PlatformBlocklistMatchesParams{Email: email, TaxID: tax, ExternalID: ext})
	if err != nil {
		return err
	}
	if len(global) > 0 {
		return ErrBlocklisted()
	}
	return nil
}

func message(id uuid.UUID, kind, value, note, by string, at time.Time) *plansv1.BlocklistEntry {
	return &plansv1.BlocklistEntry{Id: id.String(), Kind: kind, Value: value, Note: note, CreatedBy: by, CreateTime: timestamppb.New(at)}
}

func actorOf(a string) string {
	a = strings.TrimSpace(a)
	if a == "" {
		a = "operator"
	}
	if r := []rune(a); len(r) > 120 {
		a = string(r[:120])
	}
	return a
}

func noteOf(n string) (string, error) {
	n = strings.TrimSpace(n)
	if utf8.RuneCountInString(n) > 500 {
		return "", domain.Invalid("note", "at most 500 characters")
	}
	return n, nil
}

// kindNames say what an entry is in the audit summary; the value itself is personal data and stays out of the log.
var kindNames = map[string]string{"email": "an e-mail address", "tax_id": "a tax id", "external_id": "an external customer id"}

// List returns the app's entries, newest first.
func (l *Lists) List(ctx context.Context) (*plansv1.ListBlocklistResponse, error) {
	var rows []sqlc.ListBlocklistRow
	err := l.db.Tx(ctx, func(q *sqlc.Queries) (err error) {
		rows, err = q.ListBlocklist(ctx)
		return err
	})
	if err != nil {
		return nil, convert.Err(err, "blocklist")
	}
	out := &plansv1.ListBlocklistResponse{Entries: make([]*plansv1.BlocklistEntry, 0, len(rows))}
	for _, r := range rows {
		out.Entries = append(out.Entries, message(r.ID, r.Kind, r.Value, r.Note, r.CreatedBy, r.CreatedAt))
	}
	return out, nil
}

// Add puts a value on the app's list. An entry that is there is returned as it is (and nothing is recorded again).
func (l *Lists) Add(ctx context.Context, req *plansv1.AddBlocklistEntryRequest) (*plansv1.BlocklistEntry, error) {
	value, err := Normalize(req.GetKind(), req.GetValue())
	if err != nil {
		return nil, err
	}
	note, err := noteOf(req.GetNote())
	if err != nil {
		return nil, err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	var out *plansv1.BlocklistEntry
	err = l.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		if n, err := q.CountBlocklist(ctx); err != nil {
			return err
		} else if n >= MaxEntries {
			return errkey.Newf(codes.ResourceExhausted, "plans.blocklist.full", "a blocklist holds at most %d entries", MaxEntries)
		}
		id := idgen.NewUUIDv7()
		r, err := q.InsertBlocklistEntry(ctx, sqlc.InsertBlocklistEntryParams{ID: id, Kind: req.GetKind(), Value: value, Note: note, CreatedBy: actorOf(req.GetActor())})
		if errors.Is(err, pgx.ErrNoRows) { // it is there already
			have, err := q.GetBlocklistEntryByValue(ctx, sqlc.GetBlocklistEntryByValueParams{Kind: req.GetKind(), Value: value})
			if err != nil {
				return err
			}
			out = message(have.ID, have.Kind, have.Value, have.Note, have.CreatedBy, have.CreatedAt)
			return nil
		}
		if err != nil {
			return err
		}
		out = message(r.ID, r.Kind, r.Value, r.Note, r.CreatedBy, r.CreatedAt)
		return audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "blocklist.entry_added", TargetType: "blocklist_entry", TargetID: id.String(),
			Summary: "added " + kindNames[req.GetKind()] + " to the blocklist", Detail: map[string]any{"kind": req.GetKind()}})
	})
	if err != nil {
		return nil, convert.Err(err, "blocklist")
	}
	return out, nil
}

// Remove takes an entry off the app's list.
func (l *Lists) Remove(ctx context.Context, req *plansv1.RemoveBlocklistEntryRequest) error {
	id, err := domain.ParseID("id", req.GetId())
	if err != nil {
		return err
	}
	ti, err := tenant.Require(ctx)
	if err != nil {
		return err
	}
	err = l.db.TxWith(ctx, func(q *sqlc.Queries, tx pgx.Tx) error {
		kind, err := q.DeleteBlocklistEntry(ctx, id)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{TenantID: ti.ID, Action: "blocklist.entry_removed", TargetType: "blocklist_entry", TargetID: id.String(),
			Summary: "removed " + kindNames[kind] + " from the blocklist", Detail: map[string]any{"kind": kind}})
	})
	return convert.Err(err, "blocklist entry")
}

// Platform is the operator's list, on the system pool.
type Platform struct{ pool *pgxpool.Pool }

// NewPlatform returns the operator's list.
func NewPlatform(systemPool *pgxpool.Pool) *Platform { return &Platform{pool: systemPool} }

// List returns the platform-wide entries, newest first.
func (p *Platform) List(ctx context.Context) (*plansv1.ListPlatformBlocklistResponse, error) {
	rows, err := sqlc.New(p.pool).ListPlatformBlocklist(ctx)
	if err != nil {
		return nil, convert.Err(err, "blocklist")
	}
	out := &plansv1.ListPlatformBlocklistResponse{Entries: make([]*plansv1.BlocklistEntry, 0, len(rows))}
	for _, r := range rows {
		out.Entries = append(out.Entries, message(r.ID, r.Kind, r.Value, r.Note, r.CreatedBy, r.CreatedAt))
	}
	return out, nil
}

// Add puts a value on the platform-wide list, recorded in the audit log without the value.
func (p *Platform) Add(ctx context.Context, req *plansv1.AddPlatformBlocklistEntryRequest) (*plansv1.BlocklistEntry, error) {
	value, err := Normalize(req.GetKind(), req.GetValue())
	if err != nil {
		return nil, err
	}
	note, err := noteOf(req.GetNote())
	if err != nil {
		return nil, err
	}
	var out *plansv1.BlocklistEntry
	err = pgxx.InTx(ctx, p.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := sqlc.New(tx)
		if n, err := q.CountPlatformBlocklist(ctx); err != nil {
			return err
		} else if n >= MaxEntries {
			return errkey.Newf(codes.ResourceExhausted, "plans.blocklist.full", "a blocklist holds at most %d entries", MaxEntries)
		}
		id := idgen.NewUUIDv7()
		r, err := q.InsertPlatformBlocklistEntry(ctx, sqlc.InsertPlatformBlocklistEntryParams{ID: id, Kind: req.GetKind(), Value: value, Note: note, CreatedBy: actorOf(req.GetActor())})
		if errors.Is(err, pgx.ErrNoRows) {
			have, err := q.GetPlatformBlocklistEntryByValue(ctx, sqlc.GetPlatformBlocklistEntryByValueParams{Kind: req.GetKind(), Value: value})
			if err != nil {
				return err
			}
			out = message(have.ID, have.Kind, have.Value, have.Note, have.CreatedBy, have.CreatedAt)
			return nil
		}
		if err != nil {
			return err
		}
		out = message(r.ID, r.Kind, r.Value, r.Note, r.CreatedBy, r.CreatedAt)
		return audit.Record(ctx, tx, audit.Event{Action: "platform_blocklist.entry_added", TargetType: "platform_blocklist_entry", TargetID: id.String(),
			Summary: "added " + kindNames[req.GetKind()] + " to the platform blocklist", Detail: map[string]any{"kind": req.GetKind()}})
	})
	if err != nil {
		return nil, convert.Err(err, "blocklist")
	}
	return out, nil
}

// Remove takes an entry off the platform-wide list.
func (p *Platform) Remove(ctx context.Context, req *plansv1.RemovePlatformBlocklistEntryRequest) error {
	id, err := domain.ParseID("id", req.GetId())
	if err != nil {
		return err
	}
	err = pgxx.InTx(ctx, p.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		kind, err := sqlc.New(tx).DeletePlatformBlocklistEntry(ctx, id)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "platform_blocklist.entry_removed", TargetType: "platform_blocklist_entry", TargetID: id.String(),
			Summary: "removed " + kindNames[kind] + " from the platform blocklist", Detail: map[string]any{"kind": kind}})
	})
	return convert.Err(err, "blocklist entry")
}
