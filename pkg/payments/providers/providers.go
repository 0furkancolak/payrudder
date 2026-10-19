// Package providers is the catalogue of the payment providers Quotify has adapters for and what each can do, independent of any
// tenant's account. plans-svc checks routing rules against it; the workers build the adapters.
package providers

import (
	"sort"

	"github.com/0furkancolak/quotify/pkg/payments"
	"github.com/0furkancolak/quotify/pkg/payments/iyzico"
	"github.com/0furkancolak/quotify/pkg/payments/paddle"
)

var catalogue = map[string]payments.Capabilities{
	iyzico.Name: iyzico.Caps(),
	paddle.Name: paddle.Caps(),
}

// Lookup returns the capabilities of a provider by name.
func Lookup(name string) (payments.Capabilities, bool) {
	c, ok := catalogue[name]
	return c, ok
}

// Names returns the known provider names, sorted.
func Names() []string {
	out := make([]string, 0, len(catalogue))
	for n := range catalogue {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
