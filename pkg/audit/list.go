package audit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Row is a stored entry.
type Row struct {
	ID         uuid.UUID
	TenantID   *uuid.UUID
	AccountID  *uuid.UUID
	ActorType  string
	ActorID    string
	ActorEmail string
	Action     string
	TargetType string
	TargetID   string
	Summary    string
	Detail     json.RawMessage
	BeforeHash string
	AfterHash  string
	SourceIP   string
	RequestID  string
	CreatedAt  time.Time
}

// Cursor is a place in the log, newest first: the entries after it are older. It is the position of the last entry of a page.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// IsZero reports whether there is no cursor (the first page).
func (c Cursor) IsZero() bool { return c.ID == uuid.Nil }

// Token is the cursor as an opaque string for a page_token.
func (c Cursor) Token() string {
	if c.IsZero() {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.At.UnixMicro(), 10) + "." + c.ID.String()))
}

// ErrBadToken is a page token that is not one this package made.
var ErrBadToken = errors.New("audit: bad page token")

// ParseToken reads a page token; "" is the first page.
func ParseToken(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrBadToken
	}
	micro, id, ok := strings.Cut(string(raw), ".")
	if !ok {
		return Cursor{}, ErrBadToken
	}
	us, err := strconv.ParseInt(micro, 10, 64)
	u, err2 := uuid.Parse(id)
	if err != nil || err2 != nil {
		return Cursor{}, ErrBadToken
	}
	return Cursor{At: time.UnixMicro(us).UTC(), ID: u}, nil
}

// CursorOf is the position of a row.
func CursorOf(r Row) Cursor { return Cursor{At: r.CreatedAt, ID: r.ID} }

// Page limits.
const (
	DefaultLimit = 50
	MaxLimit     = 200
)

// Filter selects entries. The zero Filter selects everything (the operator's view).
type Filter struct {
	// TenantID and AccountID scope the read: the entries of that tenant, and those of that account that belong to no tenant. With both zero nothing is scoped.
	TenantID  uuid.UUID
	AccountID uuid.UUID
	// Actions are exact action names, or an area as "api_key.*".
	Actions []string
	// Actor is an actor id or an e-mail address (compared without regard to case).
	Actor      string
	TargetType string
	TargetID   string
	// From is inclusive, To exclusive; the zero time is no bound.
	From, To time.Time
	// After is the cursor of the last entry already seen.
	After Cursor
	// Limit is the page size (DefaultLimit when 0, at most MaxLimit).
	Limit int
}

// Querier is what List needs of a pgx.Tx or a pool.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// The statement is fixed (ADR 0020 §5: SQL is never assembled): every filter is a parameter that matches everything when it is empty.
const listSQL = `SELECT id, tenant_id, account_id, actor_type, actor_id, actor_email, action, target_type, target_id, summary, detail, before_hash, after_hash, source_ip, request_id, created_at
FROM audit_events
WHERE (($1::uuid IS NULL AND $2::uuid IS NULL) OR tenant_id = $1 OR (account_id = $2 AND tenant_id IS NULL))
  AND (cardinality($3::text[]) + cardinality($4::text[]) = 0 OR action = ANY($3) OR action LIKE ANY($4))
  AND ($5::text = '' OR actor_id = $5 OR lower(actor_email) = lower($5))
  AND ($6::text = '' OR target_type = $6)
  AND ($7::text = '' OR target_id = $7)
  AND ($8::timestamptz IS NULL OR created_at >= $8)
  AND ($9::timestamptz IS NULL OR created_at < $9)
  AND ($10::timestamptz IS NULL OR (created_at, id) < ($10, $11::uuid))
ORDER BY created_at DESC, id DESC
LIMIT $12`

// List reads one page, newest first, from one database. Entries of services and of cells are put together by Merge.
func List(ctx context.Context, db Querier, f Filter) ([]Row, error) {
	rows, err := db.Query(ctx, listSQL, f.args()...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
		var x Row
		err := r.Scan(&x.ID, &x.TenantID, &x.AccountID, &x.ActorType, &x.ActorID, &x.ActorEmail, &x.Action, &x.TargetType, &x.TargetID, &x.Summary, &x.Detail,
			&x.BeforeHash, &x.AfterHash, &x.SourceIP, &x.RequestID, &x.CreatedAt)
		return x, err
	})
}

// args are the parameters of listSQL, in order.
func (f Filter) args() []any {
	exact, prefixes := []string{}, []string{}
	for _, a := range f.Actions {
		if p, ok := strings.CutSuffix(a, ".*"); ok {
			prefixes = append(prefixes, likeEscape(p)+".%")
		} else if a != "" {
			exact = append(exact, a)
		}
	}
	at := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		return &t
	}
	var cursorAt *time.Time
	var cursorID *uuid.UUID
	if !f.After.IsZero() {
		cursorAt, cursorID = &f.After.At, &f.After.ID
	}
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	return []any{nilUUID(f.TenantID), nilUUID(f.AccountID), exact, prefixes, f.Actor, f.TargetType, f.TargetID, at(f.From), at(f.To), cursorAt, cursorID, min(limit, MaxLimit)}
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Merge puts lists that were each read newest-first with the same filter, cursor and limit into one, newest first, without repeats, cut to limit. The cursor of
// the last row of the result is where the next page starts: a row of any list that was cut away is older than it, and is read again.
func Merge(limit int, lists ...[]Row) []Row {
	var all []Row
	for _, l := range lists {
		all = append(all, l...)
	}
	slices.SortStableFunc(all, func(a, b Row) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	all = slices.CompactFunc(all, func(a, b Row) bool { return a.ID == b.ID })
	if limit <= 0 {
		limit = DefaultLimit
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

// CSVHeader and CSVRecord are the export format: one line per entry, the same columns the API returns (detail as JSON).
var CSVHeader = []string{"id", "create_time", "tenant_id", "account_id", "actor_type", "actor_id", "actor_email", "action", "target_type", "target_id", "summary", "detail",
	"before_hash", "after_hash", "source_ip", "request_id"}

// CSVRecord is one line of the export.
func CSVRecord(r Row) []string {
	ptr := func(u *uuid.UUID) string {
		if u == nil {
			return ""
		}
		return u.String()
	}
	rec := []string{r.ID.String(), r.CreatedAt.UTC().Format(time.RFC3339Nano), ptr(r.TenantID), ptr(r.AccountID), r.ActorType, r.ActorID, r.ActorEmail, r.Action, r.TargetType,
		r.TargetID, r.Summary, string(r.Detail), r.BeforeHash, r.AfterHash, r.SourceIP, r.RequestID}
	for i := range rec {
		rec[i] = csvSafe(rec[i])
	}
	return rec
}

// csvSafe stops a cell from being read as a formula by a spreadsheet: a person controls much of a line (the names in a summary, the address they signed up with, the id a
// request names, a request id header), and the export is meant to be opened in one. Every cell goes through it; ids, times and JSON never start with one of these.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
