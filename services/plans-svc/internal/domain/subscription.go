package domain

import (
	"regexp"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var providerNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// ValidProviderName reports whether s looks like a payment provider's name, such as paddle.
func ValidProviderName(s string) bool { return providerNameRe.MatchString(s) }

// RequireQuotifyOwned refuses to change locally a subscription a provider runs: the change would not reach the provider.
func RequireQuotifyOwned(owner string, provider *string) error {
	if owner == "provider" {
		name := ""
		if provider != nil {
			name = *provider
		}
		return status.Errorf(codes.FailedPrecondition, "this subscription is run by %s; change it there", name)
	}
	return nil
}
