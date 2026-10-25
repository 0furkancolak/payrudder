package api

import (
	"reflect"
	"strings"
	"testing"

	billingv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/billing/v1"
)

// Every method of the four billing services is served by a module. A method no module defines would silently fall through to Unimplemented.
func TestEveryBillingMethodHasAModule(t *testing.T) {
	srv := reflect.TypeOf(&Server{})
	unimpl := reflect.TypeOf(unimplemented{})
	for service, iface := range map[string]reflect.Type{
		"BillingService":       reflect.TypeOf((*billingv1.BillingServiceServer)(nil)).Elem(),
		"SandboxService":       reflect.TypeOf((*billingv1.SandboxServiceServer)(nil)).Elem(),
		"BillingAdminService":  reflect.TypeOf((*billingv1.BillingAdminServiceServer)(nil)).Elem(),
		"HostedInvoiceService": reflect.TypeOf((*billingv1.HostedInvoiceServiceServer)(nil)).Elem(),
	} {
		for i := range iface.NumMethod() {
			name := iface.Method(i).Name
			if strings.HasPrefix(name, "mustEmbed") || strings.HasPrefix(name, "testEmbedded") {
				continue
			}
			if _, ok := srv.MethodByName(name); !ok {
				t.Errorf("%s.%s is not promoted to Server", service, name)
				continue
			}
			found := false
			for j := range srv.Elem().NumField() {
				f := srv.Elem().Field(j)
				if !f.Anonymous || f.Type == unimpl {
					continue
				}
				if _, ok := f.Type.MethodByName(name); ok {
					found = true
				}
			}
			if !found {
				t.Errorf("%s.%s is served by no module: it answers Unimplemented", service, name)
			}
		}
	}
}
