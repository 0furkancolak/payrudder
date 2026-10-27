package payments

import (
	"context"
	"errors"
	"fmt"
)

// HealthChecker is implemented by an adapter that can ask its provider one cheap, authenticated, read-only question: do these credentials work? It moves no
// money and creates nothing. An adapter with no such call (a bank transfer account is only bank details) does not implement it.
type HealthChecker interface {
	CheckHealth(ctx context.Context) error
}

// ErrBadCredentials: the provider answered, and refused the credentials (or the request that tests them). Retrying the same settings gives the same answer.
var ErrBadCredentials = errors.New("payments: the provider refused the credentials")

// ErrUnreachable: the provider could not be asked (network, timeout, 5xx). Says nothing about the credentials.
var ErrUnreachable = errors.New("payments: the provider could not be reached")

// ErrUnknownEndpoint: the settings point at a host that is not one of the provider's documented live or test hosts, so which mode they are in cannot be told. A
// person connecting a live account themselves may not name one (the platform would send the secret keys to it).
var ErrUnknownEndpoint = errors.New("payments: the endpoint is not one of the provider's documented hosts")

// Health is what a health check established.
type Health struct {
	// Remote is true when the provider was actually asked; false for an adapter with no such call (the settings only built).
	Remote bool
}

// Verify is the check for a person who connects an account themselves: the adapter must tell its mode (a documented host) and it must be the environment's,
// and when the adapter can ask its provider, the provider must accept the credentials. A provider that cannot tell its mode (bank transfer) has no endpoint to
// point anywhere, so it passes that part.
func Verify(ctx context.Context, p Provider, environment string) (Health, error) {
	if lm, ok := p.(LiveModer); ok {
		if _, known := lm.Livemode(); !known {
			return Health{}, ErrUnknownEndpoint
		}
	}
	if err := CheckLivemode(p, environment); err != nil {
		return Health{}, err
	}
	return Check(ctx, p)
}

// Check asks the provider if it can be asked. It classifies a failure as ErrBadCredentials or ErrUnreachable (wrapping the cause, for the log).
func Check(ctx context.Context, p Provider) (Health, error) {
	hc, ok := p.(HealthChecker)
	if !ok {
		return Health{}, nil
	}
	err := hc.CheckHealth(ctx)
	if err == nil {
		return Health{Remote: true}, nil
	}
	var tr *TransientError
	switch {
	case errors.Is(err, ErrBadCredentials), errors.Is(err, ErrUnreachable):
		return Health{Remote: true}, err
	case errors.Is(err, ErrTimeout), errors.As(err, &tr), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return Health{Remote: true}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	return Health{Remote: true}, fmt.Errorf("%w: %w", ErrBadCredentials, err)
}
