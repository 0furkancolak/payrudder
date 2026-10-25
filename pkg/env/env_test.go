package env_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	plansv1 "github.com/0furkancolak/quotify/gen/go/quotify/plans/v1"
	"github.com/0furkancolak/quotify/pkg/apikey"
	"github.com/0furkancolak/quotify/pkg/env"
	"github.com/0furkancolak/quotify/pkg/errkey"
)

func TestParseHasNoDefault(t *testing.T) {
	for _, s := range []string{"", "Live", "LIVE", "test", "staging", " live"} {
		if _, err := env.Parse(s); !errors.Is(err, env.ErrMissing) {
			t.Errorf("Parse(%q) = %v, want ErrMissing", s, err)
		}
	}
	for s, want := range map[string]env.Env{"sandbox": env.Sandbox, "live": env.Live} {
		if got, err := env.Parse(s); err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v", s, got, err)
		}
	}
}

func TestProtoRoundTripAndUnspecified(t *testing.T) {
	for _, e := range []env.Env{env.Sandbox, env.Live} {
		got, err := env.FromProto(e.Proto())
		if err != nil || got != e {
			t.Errorf("%s round trip = %q, %v", e, got, err)
		}
	}
	if _, err := env.FromProto(plansv1.Environment_ENVIRONMENT_UNSPECIFIED); !errors.Is(err, env.ErrMissing) {
		t.Errorf("unspecified = %v, want ErrMissing: there is no default environment", err)
	}
	if env.Env("x").Proto() != plansv1.Environment_ENVIRONMENT_UNSPECIFIED {
		t.Error("an invalid environment has an enum value")
	}
}

func TestKeyPrefixFollowsTheEnvironment(t *testing.T) {
	if e, err := env.KeyEnv(apikey.EnvTest); err != nil || e != env.Sandbox {
		t.Errorf("qk_test_ = %q, %v", e, err)
	}
	if e, err := env.KeyEnv(apikey.EnvLive); err != nil || e != env.Live {
		t.Errorf("qk_live_ = %q, %v", e, err)
	}
	if _, err := env.KeyEnv("prod"); !errors.Is(err, env.ErrMissing) {
		t.Errorf("a made-up prefix = %v", err)
	}
	if env.Sandbox.ForKey() != apikey.EnvTest || env.Live.ForKey() != apikey.EnvLive || env.Env("").ForKey() != "" {
		t.Error("ForKey")
	}
}

func TestSameFailsClosed(t *testing.T) {
	if err := env.Same(env.Live, env.Live, "command"); err != nil {
		t.Errorf("equal: %v", err)
	}
	if err := env.Same(env.Sandbox, env.Live, "command"); !errors.Is(err, env.ErrMismatch) {
		t.Errorf("sandbox vs live = %v", err)
	}
	// Both empty is equal, but not valid: an unset environment must never pass for a match.
	if err := env.Same("", "", "command"); !errors.Is(err, env.ErrMissing) {
		t.Errorf("empty vs empty = %v, want ErrMissing", err)
	}
	if err := env.SameProto(plansv1.Environment_ENVIRONMENT_UNSPECIFIED, env.Live, "command"); !errors.Is(err, env.ErrMissing) {
		t.Errorf("unspecified command = %v", err)
	}
	if err := env.SameProto(plansv1.Environment_ENVIRONMENT_SANDBOX, env.Live, "command"); !errors.Is(err, env.ErrMismatch) {
		t.Errorf("sandbox command for a live tenant = %v", err)
	}
}

func TestListAndAllowed(t *testing.T) {
	l, err := env.ParseList("sandbox, live,sandbox")
	if err != nil || len(l) != 2 {
		t.Fatalf("list = %v, %v", l, err)
	}
	if _, err := env.ParseList("sandbox,prod"); err == nil {
		t.Error("an unknown environment in the list was accepted")
	}
	if _, err := env.ParseList(""); err == nil {
		t.Error("an empty list was accepted")
	}
	if !env.Allowed([]string{"sandbox"}, env.Sandbox) || env.Allowed([]string{"sandbox"}, env.Live) {
		t.Error("Allowed")
	}
}

func TestRefuseCarriesAnErrorKey(t *testing.T) {
	err := env.Refuse(env.Same(env.Sandbox, env.Live, "x"))
	if status.Code(err) != codes.FailedPrecondition || errkey.Of(err) != "env.request.mismatch" {
		t.Errorf("mismatch = %v (%q)", err, errkey.Of(err))
	}
	err = env.Refuse(env.Same("", env.Live, "x"))
	if errkey.Of(err) != "env.request.missing" {
		t.Errorf("missing = %v (%q)", err, errkey.Of(err))
	}
	plain := errors.New("other")
	if !errors.Is(env.Refuse(plain), plain) {
		t.Error("an unrelated error was rewritten")
	}
}
