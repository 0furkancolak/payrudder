package workerx

import (
	"errors"
	"fmt"

	"github.com/0furkancolak/quotify/pkg/env"
)

// RefuseEnvironment is how a worker answers the failure of an environment check (pkg/env): a command or event that names no environment, or one that
// disagrees with its tenant's, is poison. It is dead-lettered with the reason and never retried, because retrying cannot make it right and executing it could
// confuse sandbox and live. Any other error (a lookup of the tenant that failed) is returned as it is, so the queue retries it.
func RefuseEnvironment(err error) error {
	if errors.Is(err, env.ErrMissing) || errors.Is(err, env.ErrMismatch) {
		return fmt.Errorf("%w: %w", ErrPoison, err)
	}
	return err
}
