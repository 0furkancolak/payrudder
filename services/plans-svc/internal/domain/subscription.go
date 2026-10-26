package domain

import (
	"regexp"

	"google.golang.org/grpc/codes"

	"github.com/0furkancolak/payrudder/pkg/errkey"
	"github.com/0furkancolak/payrudder/pkg/payments"
)

// Who runs a subscription (subscriptions.owner): Payrudder itself, or the payment provider the customer pays through. The stored value of the first
// predates the rename.
const (
	OwnerPayrudder = string(payments.OwnerPayrudder)
	OwnerProvider  = string(payments.OwnerProvider)
)

var providerNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidProviderName reports whether s looks like a payment provider's name, such as paddle.
func ValidProviderName(s string) bool { return providerNameRe.MatchString(s) }

// RequirePayrudderOwned refuses to change locally a subscription a provider runs: the change would not reach the provider.
func RequirePayrudderOwned(owner string, provider *string) error {
	if owner == OwnerProvider {
		name := ""
		if provider != nil {
			name = *provider
		}
		return errkey.Newf(codes.FailedPrecondition, "plans.subscription.run_by_provider", "this subscription is run by %s; change it there", name)
	}
	return nil
}
