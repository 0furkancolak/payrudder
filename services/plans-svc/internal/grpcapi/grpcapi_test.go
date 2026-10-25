package grpcapi

import (
	"reflect"
	"strings"
	"testing"

	plansv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/plans/v1"
)

// Every method of PlansService is served by a module. A method no module defines would silently fall through to Unimplemented.
func TestEveryPlansMethodHasAModule(t *testing.T) {
	requireServedByModules(t, reflect.TypeOf((*plansv1.PlansServiceServer)(nil)).Elem(), reflect.TypeOf(&Server{}), reflect.TypeOf(unimplemented{}))
}

// Every method of AdminService is served by a module (accounts, tenants, apikeys).
func TestEveryAdminMethodHasAModule(t *testing.T) {
	requireServedByModules(t, reflect.TypeOf((*plansv1.AdminServiceServer)(nil)).Elem(), reflect.TypeOf(&AdminServer{}), reflect.TypeOf(unimplementedAdmin{}))
}

// requireServedByModules checks that every method of the service interface is promoted to srv from an embedded module handler, not only from unimpl.
func requireServedByModules(t *testing.T, iface, srv, unimpl reflect.Type) {
	t.Helper()
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		if strings.HasPrefix(name, "mustEmbed") || strings.HasPrefix(name, "testEmbedded") {
			continue
		}
		if _, ok := srv.MethodByName(name); !ok {
			t.Errorf("%s is not promoted to %s", name, srv.Elem().Name())
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
			t.Errorf("%s is served by no module: it answers Unimplemented", name)
		}
	}
}
