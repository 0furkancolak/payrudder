package env_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
	"github.com/0furkancolak/payrudder/pkg/env"
)

func TestDirectoryKeepsAnswersAndNeverGuesses(t *testing.T) {
	var calls atomic.Int32
	live, sandbox := uuid.New(), uuid.New()
	d := env.NewDirectory(func(_ context.Context, id uuid.UUID) (env.Env, error) {
		calls.Add(1)
		switch id {
		case live:
			return env.Live, nil
		case sandbox:
			return env.Sandbox, nil
		}
		return "", errors.New("plans-svc is down")
	}, 0)
	ctx := context.Background()

	for range 3 {
		if got, err := d.Of(ctx, live); err != nil || got != env.Live {
			t.Fatalf("Of(live) = %q, %v", got, err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("lookups = %d, want 1: a tenant's environment never changes", calls.Load())
	}
	// A failed lookup is an error, not "live" and not "sandbox"; it is not cached.
	if _, err := d.Of(ctx, uuid.New()); err == nil {
		t.Error("an unknown tenant got an environment")
	}
	if _, err := d.Of(ctx, uuid.New()); err == nil || calls.Load() != 3 {
		t.Errorf("a failed lookup was remembered (calls %d)", calls.Load())
	}
}

func TestDirectoryCheckFailsClosed(t *testing.T) {
	live, sandbox := uuid.New(), uuid.New()
	d := env.NewDirectory(func(_ context.Context, id uuid.UUID) (env.Env, error) {
		if id == live {
			return env.Live, nil
		}
		return env.Sandbox, nil
	}, 0)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		tenant  uuid.UUID
		claimed plansv1.Environment
		want    error
	}{
		"the same":                      {live, plansv1.Environment_ENVIRONMENT_LIVE, nil},
		"sandbox command, live tenant":  {live, plansv1.Environment_ENVIRONMENT_SANDBOX, env.ErrMismatch},
		"live command, sandbox tenant":  {sandbox, plansv1.Environment_ENVIRONMENT_LIVE, env.ErrMismatch},
		"no environment on the command": {live, plansv1.Environment_ENVIRONMENT_UNSPECIFIED, env.ErrMissing},
	} {
		err := d.Check(ctx, tc.tenant, tc.claimed, "command")
		if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
}

func TestDirectoryRefusesALookupThatAnswersNothing(t *testing.T) {
	d := env.NewDirectory(func(context.Context, uuid.UUID) (env.Env, error) { return "", nil }, 0)
	if _, err := d.Of(context.Background(), uuid.New()); !errors.Is(err, env.ErrMissing) {
		t.Errorf("an empty answer = %v, want ErrMissing", err)
	}
}

func TestDirectoryStaysBounded(t *testing.T) {
	d := env.NewDirectory(func(context.Context, uuid.UUID) (env.Env, error) { return env.Live, nil }, 4)
	for range 50 {
		if _, err := d.Of(context.Background(), uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
}
