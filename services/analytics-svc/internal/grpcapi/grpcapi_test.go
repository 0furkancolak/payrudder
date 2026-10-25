package grpcapi

import (
	"reflect"
	"strings"
	"testing"

	analyticsv1 "github.com/0furkancolak/payrudder/gen/go/payrudder/analytics/v1"
)

// Every method of AnalyticsService is served by a module. A method no module defines would silently fall through to Unimplemented.
func TestEveryAnalyticsMethodHasAModule(t *testing.T) {
	iface := reflect.TypeOf((*analyticsv1.AnalyticsServiceServer)(nil)).Elem()
	srv := reflect.TypeOf(&Server{})
	unimpl := reflect.TypeOf(unimplemented{})
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		if strings.HasPrefix(name, "mustEmbed") || strings.HasPrefix(name, "testEmbedded") {
			continue
		}
		if _, ok := srv.MethodByName(name); !ok {
			t.Errorf("%s is not promoted to Server", name)
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
