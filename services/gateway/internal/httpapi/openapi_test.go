package httpapi

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOpenAPIMatchesRouteTable is the contract test: every route the gateway serves is documented in
// api/openapi/v1.yaml, and every documented operation is served.
func TestOpenAPIMatchesRouteTable(t *testing.T) {
	raw, err := os.ReadFile("../../../../api/openapi/v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	var documented []string
	for path, ops := range spec.Paths {
		for method := range ops {
			documented = append(documented, strings.ToUpper(method)+" "+path)
		}
	}
	var served []string
	for _, rt := range (&Server{}).routes() {
		served = append(served, rt.Method+" "+rt.Path)
	}
	slices.Sort(documented)
	slices.Sort(served)
	for _, r := range served {
		if !slices.Contains(documented, r) {
			t.Errorf("route %s is served but missing from api/openapi/v1.yaml", r)
		}
	}
	for _, r := range documented {
		if !slices.Contains(served, r) {
			t.Errorf("operation %s is documented but not served", r)
		}
	}
}
