package payments

import (
	"errors"
	"fmt"

	"github.com/0furkancolak/payrudder/pkg/env"
)

// LiveModer is implemented by a provider adapter that can tell, from the endpoint its settings point at, whether the account is a live-mode or a test-mode one
// at the provider (iyzico and Paddle have a separate sandbox host). known is false for an endpoint it does not recognise (a proxy, a test double).
type LiveModer interface {
	Livemode() (live, known bool)
}

// ErrLivemodeMismatch: an account connected in one mode points at the provider's other mode. A live account must not be a provider sandbox (its charges would
// never reach a customer), and a sandbox-environment account must not be live credentials (a test would move real money).
var ErrLivemodeMismatch = errors.New("payments: the account's mode at the provider does not match its environment")

// KeyModer is implemented by a provider adapter whose credentials show their mode in their format (a Paddle key starts pdl_live_ or pdl_sdbx_, an iyzico
// sandbox key starts sandbox-). known is false for a key that does not say; that key is then judged by the endpoint alone and by the provider itself.
type KeyModer interface {
	KeyMode() (live, known bool)
}

// CheckLivemode returns ErrLivemodeMismatch when the adapter knows its mode, from its endpoint or from the format of its key, and it is not the environment's
// ("live" or "sandbox"). A provider that cannot tell passes: the check refuses what it can see. A live key in a sandbox account (or a test key in a live one) is
// refused before it is sent anywhere.
func CheckLivemode(p Provider, environment string) error {
	want := environment == env.Live.String()
	mode := map[bool]string{true: "live", false: "test"}
	if lm, ok := p.(LiveModer); ok {
		if live, known := lm.Livemode(); known && live != want {
			return fmt.Errorf("%w: %s endpoint for a %s-environment account", ErrLivemodeMismatch, mode[live], environment)
		}
	}
	if km, ok := p.(KeyModer); ok {
		if live, known := km.KeyMode(); known && live != want {
			return fmt.Errorf("%w: %s key for a %s-environment account", ErrLivemodeMismatch, mode[live], environment)
		}
	}
	return nil
}
