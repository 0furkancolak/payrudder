// Package audit is the platform's one audit mechanism (docs/plans/22, G4): an append-only table audit_events in the database of every service that changes
// money or access, written in the same transaction as the change it records, and read back by the console and the API through a fan-out over those services.
//
// A hook is a few lines at the command layer:
//
//	audit.Record(ctx, tx, audit.Event{TenantID: t, Action: "api_key.revoked", TargetType: "api_key", TargetID: id, Summary: "revoked the key " + prefix})
//
// Who did it, from where and under which request comes from the context (NewContext): the gateway puts it there for every request and forwards it to the
// services as gRPC metadata or HTTP headers (see transport.go). No secret is ever stored: Mask cleans the summary and the detail, and a hash stands in for a
// before/after state.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/0furkancolak/payrudder/pkg/idgen"
)

// Actor types.
const (
	ActorUser     = "user"     // a person signed in to the console
	ActorAPIKey   = "api_key"  // a tenant's API key
	ActorOperator = "operator" // the platform's operator (a signed-in operator or the machine token)
	ActorSystem   = "system"   // a job, or a call that carried no actor
)

// Actor is who did something, and from where.
type Actor struct {
	Type      string
	ID        string // the user id, the API key id, or the operator's address
	Email     string
	IP        string
	RequestID string
}

type actorKey struct{}

// NewContext returns ctx carrying the actor of the request.
func NewContext(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// FromContext returns the actor of ctx; the zero Actor (type "") when there is none.
func FromContext(ctx context.Context) Actor {
	a, _ := ctx.Value(actorKey{}).(Actor)
	return a
}

// Event is one entry of the log.
type Event struct {
	// TenantID is the tenant the event is about; uuid.Nil for an event that belongs to no tenant (a sign-in, a member of an account).
	TenantID uuid.UUID
	// AccountID is the account the event is about, for an event that is the account's and no tenant's (members). uuid.Nil otherwise.
	AccountID uuid.UUID
	// By overrides who did it (a sign-in knows its user before the request does); the address and request id still come from the context.
	By *Actor
	// Action is "<area>.<what>" in lower case: api_key.created, member.role_changed, two_factor.enabled.
	Action     string
	TargetType string
	TargetID   string
	// Summary is one line for a person to read. It is masked (Mask) and cut to MaxSummary.
	Summary string
	// Before and After are the states either side of a change. Only their hashes are stored, so a reader can tell that two entries saw the same state without the log
	// holding it. Put what the reader needs to see in Detail.
	Before, After any
	// Detail is a small object of what changed (field names, old and new values that are not secret). Values of keys that look secret are replaced.
	Detail map[string]any
}

// Limits of a stored entry; the table checks the same.
const (
	MaxSummary = 500
	MaxDetail  = 4096
)

// Execer is what Record needs of a pgx.Tx, a *pgxpool.Pool or a *pgx.Conn.
type Execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

var actionRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// ErrBadEvent is an event the log refuses: a programming error, not a runtime condition.
var ErrBadEvent = errors.New("audit: bad event")

// writeFailed counts every entry that could not be written (audit_write_failed_total{area,reason}): reason "insert" is the database refusing it (a missing partition or
// grant, a full disk, a lost connection), "bad_event" a programming error. Record's callers fail the change the entry belongs to, so a rising count is a rising count of
// refused sign-ins, key changes and price changes; AuditWriteFailed pages on it. area is the part of the action before the dot, which a developer chooses, so the
// number of series is the number of areas in the code.
var writeFailed = func() metric.Int64Counter {
	c, err := otel.Meter("github.com/0furkancolak/payrudder/pkg/audit").Int64Counter("audit_write_failed", metric.WithDescription("Audit entries that could not be written, by area of the action and reason"))
	if err != nil {
		panic(err)
	}
	return c
}()

func countFailure(ctx context.Context, action, reason string) {
	area := "invalid"
	if actionRe.MatchString(action) && len(action) <= 80 {
		area, _, _ = strings.Cut(action, ".")
	}
	writeFailed.Add(ctx, 1, metric.WithAttributes(attribute.String("area", area), attribute.String("reason", reason)))
}

// Record appends e to audit_events through db, in db's transaction. A failure must fail the change it records: the caller returns the error. Every failure is counted
// (writeFailed).
func Record(ctx context.Context, db Execer, e Event) (err error) {
	defer func() {
		if err != nil {
			reason := "insert"
			if errors.Is(err, ErrBadEvent) {
				reason = "bad_event"
			}
			countFailure(ctx, e.Action, reason)
		}
	}()
	if len(e.Action) > 80 || !actionRe.MatchString(e.Action) {
		return fmt.Errorf("%w: action %q", ErrBadEvent, e.Action)
	}
	a := FromContext(ctx)
	if e.By != nil {
		a.Type, a.ID, a.Email = e.By.Type, e.By.ID, e.By.Email
	}
	if a.Type == "" {
		a.Type = ActorSystem
	}
	summary := e.Summary
	if summary == "" {
		summary = e.Action
	}
	detail, err := json.Marshal(MaskDetail(e.Detail))
	if err != nil || len(detail) > MaxDetail {
		return fmt.Errorf("%w: detail is not a small JSON object", ErrBadEvent)
	}
	if e.Detail == nil {
		detail = []byte("{}")
	}
	_, err = db.Exec(ctx, `INSERT INTO audit_events
		(id, tenant_id, account_id, actor_type, actor_id, actor_email, action, target_type, target_id, summary, detail, before_hash, after_hash, source_ip, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		idgen.NewUUIDv7(), nilUUID(e.TenantID), nilUUID(e.AccountID), a.Type, clip(a.ID, 120), clip(a.Email, 254), e.Action, clip(e.TargetType, 60), clip(e.TargetID, 200),
		clip(Mask(summary), MaxSummary), detail, Hash(e.Before), Hash(e.After), clip(a.IP, 45), clip(a.RequestID, 120))
	return err
}

func nilUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// clip cuts s to n runes (the table checks bytes of a column that is plain text, so a multibyte name must not overrun).
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Hash is the SHA-256 of the JSON of v as hex, or "" for nil: the stored stand-in for a state.
func Hash(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

const redacted = "[redacted]"

// Things that look like a secret inside a line of text: the platform's own tokens (API keys qk_, sessions qs_, mailed links qv_ qr_ qi_, sign-in challenges qc_),
// webhook secrets and provider keys, bearer credentials, passwords in a URL or a query string, and any long opaque run (a key, a hash, a base64 blob).
var secretPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\bq[ksvric]_[A-Za-z0-9_\-]{8,}`), redacted},
	{regexp.MustCompile(`\b(?:whsec|sk|rk|pk)_[A-Za-z0-9_\-]{8,}`), redacted},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{8,}`), "$1 " + redacted},
	{regexp.MustCompile(`(?i)([?&](?:token|key|secret|signature|sig|password|apikey|api_key|access_token)=)[^&\s]+`), "${1}" + redacted},
	{regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@`), "://" + redacted + "@"},
	{regexp.MustCompile(`[A-Za-z0-9+/_]{32,}={0,2}`), redacted},
}

// Mask replaces everything in s that looks like a secret. It is a net, not a licence: a hook still must not pass a secret in.
func Mask(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

var secretKeyRe = regexp.MustCompile(`(?i)(secret|password|passwd|token|plaintext|api_?key|authorization|credential|private|recovery|otp|code_hash)`)

// MaskDetail returns a copy of d in which the value under a key that names a secret is replaced and every string is masked. It goes through nested objects and lists.
func MaskDetail(d map[string]any) map[string]any {
	if d == nil {
		return nil
	}
	out := make(map[string]any, len(d))
	for k, v := range d {
		if secretKeyRe.MatchString(k) {
			out[k] = redacted
			continue
		}
		out[k] = maskValue(v)
	}
	return out
}

func maskValue(v any) any {
	switch x := v.(type) {
	case string:
		return Mask(x)
	case map[string]any:
		return MaskDetail(x)
	case []string:
		out := make([]string, len(x))
		for i, s := range x {
			out[i] = Mask(s)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = maskValue(e)
		}
		return out
	}
	return v
}
