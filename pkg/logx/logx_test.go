package logx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/0furkancolak/payrudder/pkg/logx"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, buf.String())
	}
	return m
}

func TestServiceAndTraceFields(t *testing.T) {
	var buf bytes.Buffer
	log, err := logx.New(logx.Options{Service: "hello", Version: "v1", Level: "info", Writer: &buf})
	if err != nil {
		t.Fatal(err)
	}
	tid, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	sid, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled}))

	log.InfoContext(ctx, "hi")
	m := decode(t, &buf)
	for k, want := range map[string]string{
		"service": "hello", "version": "v1", "trace_id": tid.String(), "span_id": sid.String(),
	} {
		if m[k] != want {
			t.Errorf("%s = %v, want %s", k, m[k], want)
		}
	}
}

func TestNoTraceFieldsWithoutSpan(t *testing.T) {
	var buf bytes.Buffer
	log, _ := logx.New(logx.Options{Service: "s", Writer: &buf})
	log.Info("hi")
	if _, ok := decode(t, &buf)["trace_id"]; ok {
		t.Fatal("trace_id present without a span")
	}
}

func TestRedacted(t *testing.T) {
	var buf bytes.Buffer
	log, _ := logx.New(logx.Options{Service: "s", Writer: &buf})
	secret := logx.Redacted("qk_live_abcdefgh_supersecret")
	log.Info("key", "api_key", secret)
	if got := decode(t, &buf)["api_key"]; got != "[REDACTED]" {
		t.Fatalf("api_key = %v", got)
	}
	if s := fmt.Sprint(secret); s != "[REDACTED]" {
		t.Fatalf("fmt printed %q", s)
	}
}

func TestLevel(t *testing.T) {
	var buf bytes.Buffer
	log, _ := logx.New(logx.Options{Service: "s", Level: "warn", Writer: &buf})
	log.Info("dropped")
	if buf.Len() != 0 {
		t.Fatalf("info logged at warn level: %q", buf.String())
	}
	if _, err := logx.ParseLevel("loud"); err == nil {
		t.Fatal("ParseLevel accepted an unknown level")
	}
}

func TestScrub(t *testing.T) {
	for in, want := range map[string]string{
		"key qk_live_ab12cd34_s3cr3tS3cr3tS3cr3t was refused": "key [REDACTED] was refused",
		"session qs_abcdefghijklmnop":                         "session [REDACTED]",
		"whsec_abcdefghijklmnopqrstuv failed":                 "[REDACTED] failed",
		"Authorization: Bearer abcdef1234567890":              "Authorization: [REDACTED]",
		"dial postgres://payrudder:hunter2@db:5432/x":         "dial postgres://payrudder:[REDACTED]@db:5432/x",
		`{"password": "hunter2", "ok": 1}`:                    `{"password": "[REDACTED]", "ok": 1}`,
		"token=abc123&page=2":                                 "token=[REDACTED]&page=2",
		"customer cus_42 not found":                           "customer cus_42 not found",
		"task_identifier_something is fine":                   "task_identifier_something is fine",
	} {
		if got := logx.Scrub(in); got != want {
			t.Errorf("Scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

// The shapes of a secret in text the code did not build: every mailed or issued token of the platform (the prefixes pkg/audit.Mask knows), a key that merely ends in
// a secret word (client_secret, access_token, id_token, secretKey), in the four layouts they come in (key=value, key: value, JSON, a query string), in any case,
// quoted or not, also inside JSON that was quoted again.
func TestScrubShapes(t *testing.T) {
	const r = "[REDACTED]"
	for name, tc := range map[string]struct{ in, want string }{
		"client secret":                  {"client_secret=Zm9vYmFyYmF6", "client_secret=" + r},
		"client secret upper case":       {"CLIENT_SECRET=Zm9vYmFyYmF6", "CLIENT_SECRET=" + r},
		"access token":                   {"access_token=ya29.a0AfH6SMB", "access_token=" + r},
		"refresh token with a colon":     {"refresh_token: 1//0gAbCdEf", "refresh_token: " + r},
		"id token":                       {"id_token=eyJhbGciOi.eyJzdWIi.c2ln", "id_token=" + r},
		"secretKey":                      {"secretKey: abcdef", "secretKey: " + r},
		"secret_key":                     {"secret_key=abcdef", "secret_key=" + r},
		"api key header":                 {"X-Api-Key: abcdef123456", "X-Api-Key: " + r},
		"apikey":                         {"apikey=abcdef123456", "apikey=" + r},
		"access key":                     {"aws_access_key=AKIAABCDEFGH", "aws_access_key=" + r},
		"private key":                    {"private_key: MIIEvQIBADANB", "private_key: " + r},
		"password hash":                  {"password_hash=$argon2id$v=19$m=65536", "password_hash=" + r},
		"authorization with a scheme":    {"authorization=Bearer abcdef1234567890 ok", "authorization=" + r + " ok"},
		"basic authorization":            {"Authorization: Basic dXNlcjpwYXNz", "Authorization: " + r},
		"credentials":                    {"credentials=user:pass", "credentials=" + r},
		"dsn":                            {"dsn=postgres://u:p@h:5432/db?sslmode=disable", "dsn=" + r},
		"mixed case":                     {"Access_Token=abc ClientSecret: def", "Access_Token=" + r + " ClientSecret: " + r},
		"json":                           {`{"client_secret":"abc def","grant":"x"}`, `{"client_secret":"` + r + `","grant":"x"}`},
		"json with spaces":               {`{ "id_token" : "abc" , "n": 1 }`, `{ "id_token" : "` + r + `" , "n": 1 }`},
		"json with single quotes":        {`{'secretKey': 'abc def'}`, `{'secretKey': '` + r + `'}`},
		"json quoted again (a %q of it)": {`provider said "{\"refresh_token\":\"abc def\",\"ok\":true}"`, `provider said "{\"refresh_token\":\"` + r + `\",\"ok\":true}"`},
		"query string":                   {"GET /cb?code=1&access_token=abc123&state=x", "GET /cb?code=1&access_token=" + r + "&state=x"},
		"query string, last":             {"https://x.example/p?id_token=abc.def", "https://x.example/p?id_token=" + r},
		"form body":                      {"grant_type=refresh_token&refresh_token=abc&client_secret=def", "grant_type=refresh_token&refresh_token=" + r + "&client_secret=" + r},
		"a quoted value with spaces":     {`password="two words" next`, `password="` + r + `" next`},
		"a value that ends at a comma":   {"secret=abc, retrying", "secret=" + r + ", retrying"},
		"token qk":                       {"key qk_test_ab12cd34_s3cr3tS3cr3tS3cr3t refused", "key " + r + " refused"},
		"token qs":                       {"qs_abcdefghijklmnop", r},
		"token qv (verify mail)":         {"link /verify?t=qv_abcdefghijklmnopqrstuv", "link /verify?t=" + r},
		"token qr (reset mail)":          {"qr_abcdefghijklmnopqrstuv", r},
		"token qi (invite mail)":         {"accept qi_abcdefghijklmnopqrstuv now", "accept " + r + " now"},
		"token qc (sign-in challenge)":   {"challenge qc_abcdefghijklmnopqrstuv expired", "challenge " + r + " expired"},
		"sk, rk and whsec":               {"sk_live_abcdefgh rk_test_abcdefgh whsec_abcdefghij", r + " " + r + " " + r},
		"bearer in free text":            {"sent Bearer abcdefgh12345678 to the server", "sent Bearer " + r + " to the server"},
		"password of a URL":              {"postgres://app:hunter2@db/x", "postgres://app:" + r + "@db/x"},
		"already scrubbed text stays":    {"password=" + r, "password=" + r},
		"several in one line":            {"qk_live_abcdefghijkl and password=x and ?token=y", r + " and password=" + r + " and ?token=" + r},
	} {
		t.Run(name, func(t *testing.T) {
			if got := logx.Scrub(tc.in); got != tc.want {
				t.Errorf("Scrub(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
			if again := logx.Scrub(logx.Scrub(tc.in)); again != logx.Scrub(tc.in) {
				t.Errorf("Scrub is not idempotent on %q: %q", tc.in, again)
			}
		})
	}
}

// Text that is not a secret stays as it is: words that contain a secret word, keys that only start with one, counters, ids, messages.
func TestScrubLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"customer cus_42 not found",
		"task_identifier_something is fine",
		"secretary: Ann Lovelace",
		"token_count=5 tokens=3 tokens: 12",
		"the token was not found",
		"missing authorization header",
		"password policy: at least 12 characters",
		"passwordless=true",
		"sign-in with a password failed",
		"basic configuration is needed here",
		"design=3 resign=yes",
		"qx_abcdefghijkl qk_short pk_customers_index",
		"id=7 name=token-bucket limit=100",
		"key=customer_id value=7",
		"status 429: rate limited, retry in 30s",
		"dial tcp 10.0.0.5:5432: connection refused",
		"Content-Type: application/json",
		"",
	} {
		if got := logx.Scrub(in); got != in {
			t.Errorf("Scrub(%q) = %q, want it unchanged", in, got)
		}
	}
}

func BenchmarkScrub(b *testing.B) {
	for name, line := range map[string]string{
		"no secret": `level=error msg="provider call failed" request_id=01HX customer=cus_42 status=502 body="{\"error\":\"upstream timeout\",\"detail\":\"retry later\"}" elapsed=2.5s`,
		"a secret":  `level=error msg="token refresh failed" request_id=01HX status=401 body="{\"error\":\"invalid_grant\",\"refresh_token\":\"1//0gAbCdEf\"}" key=qk_live_ab12cd34_s3cr3tS3cr3t`,
	} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				logx.Scrub(line)
			}
		})
	}
}
