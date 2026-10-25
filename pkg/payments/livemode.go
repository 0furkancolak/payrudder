package payments

import (
	"errors"
	"fmt"

	"github.com/0furkancolak/quotify/pkg/env"
)

// LiveModer is implemented by a provider adapter that can tell, from the endpoint its settings point at, whether the account is a live-mode or a test-mode one
// at the provider (iyzico and Paddle have a separate sandbox host). known is false for an endpoint it does not recognise (a proxy, a test double).
type LiveModer interface {
	Livemode() (live, known bool)
}

// ErrLivemodeMismatch: an account connected in one mode points at the provider's other mode. A live account must not be a provider sandbox (its charges would
// never reach a customer), and a sandbox-environment account must not be live credentials (a test would move real money).
var ErrLivemodeMismatch = errors.New("payments: the account's mode at the provider does not match its environment")

// CheckLivemode returns ErrLivemodeMismatch when the adapter knows its mode and it is not the environment's ("live" or "sandbox"). A provider that cannot tell
// passes: the check refuses what it can see.
func CheckLivemode(p Provider, environment string) error {
	lm, ok := p.(LiveModer)
	if !ok {
		return nil
	}
	live, known := lm.Livemode()
	if !known {
		return nil
	}
	if want := environment == env.Live.String(); live != want {
		mode := map[bool]string{true: "live", false: "test"}
		return fmt.Errorf("%w: %s endpoint for a %s-environment account", ErrLivemodeMismatch, mode[live], environment)
	}
	return nil
}
