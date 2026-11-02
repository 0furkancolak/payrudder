package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/grpc/metadata"
)

// failuresReader reads audit_write_failed. The global provider is set when the package loads, before any test runs, so that the counter made at the package's own
// start-up is handed to it (OpenTelemetry's global delegation).
var failuresReader = func() *sdkmetric.ManualReader {
	r := sdkmetric.NewManualReader()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(r)))
	return r
}()

// failures is audit_write_failed so far, by "area/reason".
func failures(t *testing.T) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := failuresReader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Name != "audit_write_failed" {
				continue
			}
			for _, dp := range sum.DataPoints {
				area, _ := dp.Attributes.Value("area")
				reason, _ := dp.Attributes.Value("reason")
				out[area.AsString()+"/"+reason.AsString()] += dp.Value
			}
		}
	}
	return out
}

func TestMaskRemovesSecrets(t *testing.T) {
	secrets := []string{
		"qk_live_" + strings.Repeat("a1B2", 8),
		"qs_" + strings.Repeat("Zz9_", 11),
		"qc_" + strings.Repeat("Zz9-", 11),
		"whsec_" + strings.Repeat("xY3", 14),
		"sk_" + "live_" + strings.Repeat("51Hx", 6), // written in pieces: a scanner must not take a test value for a real key
		strings.Repeat("0123456789abcdef", 4),
	}
	for _, s := range secrets {
		got := Mask("revoked " + s + " for good")
		if strings.Contains(got, s) || strings.Contains(got, s[len(s)-10:]) {
			t.Errorf("Mask left %q in %q", s, got)
		}
		if !strings.HasPrefix(got, "revoked ") || !strings.HasSuffix(got, " for good") {
			t.Errorf("Mask changed the words around the secret: %q", got)
		}
	}
	for in, want := range map[string]string{
		"Authorization: Bearer abcdef0123456789xyz": "Authorization: Bearer " + redacted,
		"https://u:hunter2@example.com/hook":        "https://" + redacted + "@example.com/hook",
		"https://example.com/hook?token=abc123&x=1": "https://example.com/hook?token=" + redacted + "&x=1",
	} {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskKeepsWhatAReaderNeeds(t *testing.T) {
	for _, s := range []string{
		"changed the role of alice@example.com from viewer to admin",
		"credit note CN-2026-000042 for invoice 018f3a52-7c1e-7d3a-9a55-0f7c0d3a1b22",
		"https://example.com/webhooks/payrudder",
		"published version 3 of plan pro_monthly",
	} {
		if got := Mask(s); got != s {
			t.Errorf("Mask changed %q to %q", s, got)
		}
	}
}

func TestMaskDetailRedactsSecretKeysAndNestedStrings(t *testing.T) {
	got := MaskDetail(map[string]any{
		"role":           "admin",
		"secret":         "whsec_realvalue",
		"api_key":        "qk_live_x",
		"Password":       "hunter2",
		"recovery_codes": []string{"aaaaa-bbbbb"},
		"nested":         map[string]any{"token": "t", "url": "https://h.example/p?key=abc"},
		"list":           []any{"qk_test_" + strings.Repeat("q", 20)},
	})
	b, _ := json.Marshal(got)
	for _, leak := range []string{"whsec_realvalue", "qk_live_x", "hunter2", "aaaaa-bbbbb", "key=abc", "qqqqqqqq"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("detail still holds %q: %s", leak, b)
		}
	}
	if got["role"] != "admin" {
		t.Errorf("a plain value was changed: %v", got["role"])
	}
}

type fakeExec struct {
	sql  string
	args []any
	err  error
}

func (f *fakeExec) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.sql, f.args = sql, args
	return pgconn.CommandTag{}, f.err
}

func TestRecordWritesTheActorOfTheContextAndMasksTheSummary(t *testing.T) {
	tenantID := uuid.New()
	ctx := NewContext(context.Background(), Actor{Type: ActorUser, ID: "u1", Email: "a@example.com", IP: "203.0.113.9", RequestID: "req-1"})
	f := &fakeExec{}
	err := Record(ctx, f, Event{TenantID: tenantID, Action: "api_key.created", TargetType: "api_key", TargetID: "k1",
		Summary: "created qk_live_" + strings.Repeat("a", 30), Before: nil, After: map[string]string{"role": "admin"}, Detail: map[string]any{"role": "admin", "secret": "s"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.args[1].(*uuid.UUID); got == nil || *got != tenantID {
		t.Errorf("tenant = %v", got)
	}
	if got := f.args[2].(*uuid.UUID); got != nil {
		t.Errorf("an unset account must be NULL, got %v", got)
	}
	if f.args[3] != ActorUser || f.args[4] != "u1" || f.args[5] != "a@example.com" || f.args[13] != "203.0.113.9" || f.args[14] != "req-1" {
		t.Errorf("actor columns = %v", f.args[3:6])
	}
	if s := f.args[9].(string); strings.Contains(s, "aaaaaaaa") {
		t.Errorf("summary kept a key: %q", s)
	}
	if f.args[11] != "" || f.args[12] == "" {
		t.Errorf("hashes: before %q after %q", f.args[11], f.args[12])
	}
	if d := string(f.args[10].([]byte)); strings.Contains(d, `"s"`) || !strings.Contains(d, redacted) {
		t.Errorf("detail = %s", d)
	}
}

func TestRecordWithoutAnActorIsTheSystemAndByOverrides(t *testing.T) {
	f := &fakeExec{}
	if err := Record(context.Background(), f, Event{Action: "tenant.status_changed"}); err != nil {
		t.Fatal(err)
	}
	if f.args[3] != ActorSystem {
		t.Errorf("actor type = %v", f.args[3])
	}
	if f.args[9] != "tenant.status_changed" {
		t.Errorf("an empty summary should read as the action, got %v", f.args[9])
	}
	ctx := NewContext(context.Background(), Actor{IP: "198.51.100.1"})
	if err := Record(ctx, f, Event{Action: "two_factor.enabled", By: &Actor{Type: ActorUser, ID: "u9", Email: "x@example.com"}}); err != nil {
		t.Fatal(err)
	}
	if f.args[3] != ActorUser || f.args[4] != "u9" || f.args[13] != "198.51.100.1" {
		t.Errorf("By should replace who and keep where: %v", f.args)
	}
}

func TestRecordRefusesABadEventAndPassesTheDatabaseError(t *testing.T) {
	f := &fakeExec{}
	for _, a := range []string{"", "nodots", "Upper.case", "a.b c", strings.Repeat("a", 79) + ".b"} {
		if err := Record(context.Background(), f, Event{Action: a}); !errors.Is(err, ErrBadEvent) {
			t.Errorf("action %q: err = %v", a, err)
		}
	}
	big := map[string]any{"x": strings.Repeat("ab ", MaxDetail)}
	if err := Record(context.Background(), f, Event{Action: "a.b", Detail: big}); !errors.Is(err, ErrBadEvent) {
		t.Errorf("a detail over %d bytes: %v", MaxDetail, err)
	}
	boom := errors.New("boom")
	if err := Record(context.Background(), &fakeExec{err: boom}, Event{Action: "a.b"}); !errors.Is(err, boom) {
		t.Errorf("a failed insert must come back: %v", err)
	}
}

// An entry that cannot be written is counted (audit_write_failed_total{area,reason}, which AuditWriteFailed pages on), a written one is not, and a refused event does not
// invent an area: the area is the part of a valid action before the dot.
func TestRecordCountsEveryEntryItCouldNotWrite(t *testing.T) {
	before := failures(t)
	delta := func() map[string]int64 {
		out := map[string]int64{}
		for k, v := range failures(t) {
			if v != before[k] {
				out[k] = v - before[k]
			}
		}
		return out
	}
	if err := Record(context.Background(), &fakeExec{}, Event{Action: "api_key.created"}); err != nil {
		t.Fatal(err)
	}
	if d := delta(); len(d) != 0 {
		t.Fatalf("a written entry was counted as a failure: %v", d)
	}
	boom := errors.New("boom")
	_ = Record(context.Background(), &fakeExec{err: boom}, Event{Action: "api_key.created"})
	_ = Record(context.Background(), &fakeExec{err: boom}, Event{Action: "api_key.revoked"})
	_ = Record(context.Background(), &fakeExec{err: boom}, Event{Action: "member.role_changed"})
	_ = Record(context.Background(), &fakeExec{}, Event{Action: "Not A Valid Action"})
	_ = Record(context.Background(), &fakeExec{}, Event{Action: "a.b", Detail: map[string]any{"x": strings.Repeat("ab ", MaxDetail)}})
	want := map[string]int64{"api_key/insert": 2, "member/insert": 1, "invalid/bad_event": 1, "a/bad_event": 1}
	got := delta()
	if len(got) != len(want) {
		t.Fatalf("counted %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

func TestCursorTokenRoundTrips(t *testing.T) {
	c := Cursor{At: time.Date(2026, 11, 2, 10, 0, 0, 123456000, time.UTC), ID: uuid.New()}
	got, err := ParseToken(c.Token())
	if err != nil || !got.At.Equal(c.At) || got.ID != c.ID {
		t.Fatalf("got %v, %v", got, err)
	}
	if z, err := ParseToken(""); err != nil || !z.IsZero() || z.Token() != "" {
		t.Errorf("the first page: %v %v", z, err)
	}
	for _, bad := range []string{"%%%", "bm9kb3Q", "MTIz.bm90LWEtdXVpZA"} {
		if _, err := ParseToken(bad); !errors.Is(err, ErrBadToken) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestMergeKeepsTheNewestOnceEach(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 11, 2, 10, m, 0, 0, time.UTC) }
	a, b, c := Row{ID: uuid.New(), CreatedAt: at(5)}, Row{ID: uuid.New(), CreatedAt: at(9)}, Row{ID: uuid.New(), CreatedAt: at(7)}
	got := Merge(2, []Row{a, c}, []Row{b, c})
	if len(got) != 2 || got[0].ID != b.ID || got[1].ID != c.ID {
		t.Fatalf("got %v", got)
	}
}

func TestFilterBecomesTheParametersOfOneFixedStatement(t *testing.T) {
	tenantID, accountID, cursorID := uuid.New(), uuid.New(), uuid.New()
	args := Filter{TenantID: tenantID, AccountID: accountID, Actions: []string{"api_key.created", "member.*", "a_b.*"}, Actor: "Alice@Example.com", TargetType: "api_key",
		From: time.Unix(1, 0), To: time.Unix(2, 0), After: Cursor{At: time.Unix(3, 0), ID: cursorID}, Limit: 7}.args()
	if n := strings.Count(listSQL, "$"); n == 0 || len(args) != 12 {
		t.Fatalf("%d parameters for %d placeholders", len(args), n)
	}
	if got := args[0].(*uuid.UUID); *got != tenantID || *args[1].(*uuid.UUID) != accountID {
		t.Errorf("scope = %v %v", got, args[1])
	}
	if exact, like := args[2].([]string), args[3].([]string); len(exact) != 1 || exact[0] != "api_key.created" || len(like) != 2 || like[0] != "member.%" || like[1] != `a\_b.%` {
		t.Errorf("actions = %v %v", exact, like)
	}
	if args[4] != "Alice@Example.com" || args[5] != "api_key" || args[6] != "" || args[11] != 7 {
		t.Errorf("actor, target, limit = %v %v %v %v", args[4], args[5], args[6], args[11])
	}
	if *args[9].(*time.Time) != time.Unix(3, 0) || *args[10].(*uuid.UUID) != cursorID || args[7] == nil || args[8] == nil {
		t.Errorf("time bounds and cursor = %v %v %v %v", args[7], args[8], args[9], args[10])
	}
	// The empty filter selects everything: NULLs and empty lists, the default page.
	empty := Filter{}.args()
	if empty[0].(*uuid.UUID) != nil || empty[1].(*uuid.UUID) != nil || len(empty[2].([]string)) != 0 || empty[7].(*time.Time) != nil || empty[9].(*time.Time) != nil || empty[11] != DefaultLimit {
		t.Errorf("the empty filter = %v", empty)
	}
	if capped := (Filter{Limit: 100000}).args(); capped[11] != MaxLimit {
		t.Error("the page size is capped")
	}
}

func TestCSVStopsFormulas(t *testing.T) {
	r := Row{ID: uuid.New(), Summary: `=HYPERLINK("http://x")`, CreatedAt: time.Now()}
	if got := CSVRecord(r)[10]; !strings.HasPrefix(got, "'=") {
		t.Errorf("summary = %q", got)
	}
	if len(CSVRecord(r)) != len(CSVHeader) {
		t.Errorf("a record has %d cells, the header %d", len(CSVRecord(r)), len(CSVHeader))
	}
}

func TestActorTravelsThroughMetadataAndHeaders(t *testing.T) {
	want := Actor{Type: ActorAPIKey, ID: "key-1", Email: "", IP: "203.0.113.7", RequestID: "req-9"}
	out := outgoing(NewContext(context.Background(), want))
	md, _ := metadata.FromOutgoingContext(out)
	in := fromIncoming(metadata.NewIncomingContext(context.Background(), md))
	if got := FromContext(in); got != want {
		t.Errorf("gRPC: got %+v, want %+v", got, want)
	}
	// A caller that already set x-request-id keeps it (the gateway does), and the actor's is not added next to it.
	out = outgoing(metadata.AppendToOutgoingContext(NewContext(context.Background(), want), mdRequestID, "req-0"))
	if md, _ := metadata.FromOutgoingContext(out); len(md.Get(mdRequestID)) != 1 {
		t.Errorf("x-request-id = %v", md.Get(mdRequestID))
	}

	h := http.Header{}
	h.Set(headerActorID, "spoofed")
	SetHeaders(NewContext(context.Background(), want), h)
	var seen Actor
	Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = FromContext(r.Context()) })).ServeHTTP(httptest.NewRecorder(), &http.Request{Header: h})
	if seen != want {
		t.Errorf("HTTP: got %+v, want %+v", seen, want)
	}
}
