package contract

import (
	"bytes"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	payrudder "github.com/0furkancolak/payrudder/sdk/go"
)

const specPath = "../../../../api/openapi/v1.yaml"

func loadSpec(t *testing.T) *Spec {
	t.Helper()
	s, err := Load(specPath)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// clientMethods returns "Service.Method" for every exported method of every resource namespace of the client.
func clientMethods(t *testing.T) map[string]bool {
	t.Helper()
	c, err := payrudder.New(payrudder.WithAPIKey("qk_test_x"))
	if err != nil {
		t.Fatal(err)
	}
	svcs, ok := reflect.TypeOf(c).Elem().FieldByName("services")
	if !ok {
		t.Fatal("Client has no services field")
	}
	got := map[string]bool{}
	for i := 0; i < svcs.Type.NumField(); i++ {
		f := svcs.Type.Field(i)
		pt := f.Type // *XService
		for j := 0; j < pt.NumMethod(); j++ {
			got[f.Name+"."+pt.Method(j).Name] = true
		}
	}
	return got
}

// helperMethods are the methods that are not one operation each.
func isHelper(method string, expected map[string]bool) bool {
	svc, name, _ := strings.Cut(method, ".")
	if method == "Events.Ingest" {
		return true
	}
	// The iterator over every page of a list operation.
	return strings.HasSuffix(name, "All") && expected[svc+"."+strings.TrimSuffix(name, "All")]
}

// TestEveryTenantOperationHasAClientMethod fails when the SDK and api/openapi/v1.yaml drift apart: an operationId
// under /v1 without a method, or a method without an operation.
func TestEveryTenantOperationHasAClientMethod(t *testing.T) {
	ops, err := loadSpec(t).TenantOps()
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) < 100 {
		t.Fatalf("only %d /v1 operations found: the parser is broken", len(ops))
	}
	expected := map[string]bool{}
	seenID := map[string]string{}
	for _, op := range ops {
		if prev, dup := seenID[op.OperationID]; dup {
			t.Errorf("operationId %s is used twice (%s and %s %s)", op.OperationID, prev, op.Method, op.Path)
		}
		seenID[op.OperationID] = op.Method + " " + op.Path
		expected[ServiceName(op.Path)+"."+MethodName(op.OperationID)] = true
	}
	got := clientMethods(t)

	var missing, extra []string
	for e := range expected {
		if !got[e] {
			missing = append(missing, e)
		}
	}
	for g := range got {
		if !expected[g] && !isHelper(g, expected) {
			extra = append(extra, g)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("operations in the spec without a client method (run go generate): %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("client methods that are not in the spec: %v", extra)
	}
}

// TestGeneratedCodeIsUpToDate regenerates the typed part and compares it with the files on disk.
func TestGeneratedCodeIsUpToDate(t *testing.T) {
	types, services, err := Generate(loadSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{"types_gen.go": types, "services_gen.go": services} {
		have, err := os.ReadFile("../../" + name)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(have, want) {
			t.Errorf("%s is out of date with api/openapi/v1.yaml: run `go generate ./...` in sdk/go", name)
		}
	}
}

func TestHandWrittenOperationsAreInTheSpec(t *testing.T) {
	ops, err := loadSpec(t).TenantOps()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, op := range ops {
		ids[op.OperationID] = true
	}
	for id := range handwritten {
		if !ids[id] {
			t.Errorf("handwritten operation %s is not in the spec any more", id)
		}
	}
	for id := range responseOverride {
		if !ids[id] {
			t.Errorf("response override %s is not in the spec any more", id)
		}
	}
	for id := range pageItemsOverride {
		if !ids[id] {
			t.Errorf("page items override %s is not in the spec any more", id)
		}
	}
}

func TestNaming(t *testing.T) {
	cases := map[string]string{
		"external_id":  "ExternalID",
		"base_url":     "BaseURL",
		"e-invoices":   "EInvoices",
		"credit-packs": "CreditPacks",
		"total_minor":  "TotalMinor",
		"page_token":   "PageToken",
	}
	for in, want := range cases {
		if got := Pascal(in); got != want {
			t.Errorf("Pascal(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ServiceName("/v1/expense-inbox/items/{id}/file"); got != "ExpenseInbox" {
		t.Errorf("ServiceName = %q", got)
	}
}
