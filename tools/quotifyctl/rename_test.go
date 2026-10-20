package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRewrite(t *testing.T) {
	s := renameSpec{Display: "Acme Billing", Slug: "acmebilling", Module: "github.com/acme/acmebilling"}
	for in, want := range map[string]string{
		`import "github.com/0furkancolak/quotify/pkg/config"`: `import "github.com/acme/acmebilling/pkg/config"`,
		`QUOTIFY_GRPC_ADDR`:           `ACMEBILLING_GRPC_ADDR`,
		`Quotify-Signature`:           `Acme Billing-Signature`,
		`requireQuotifyOwned(x)`:      `requireAcmebillingOwned(x)`,
		`quotify_subscription_id`:     `acmebilling_subscription_id`,
		`Quotify's copy`:              `Acme Billing's copy`,
		`image: quotify/gateway:dev`:  `image: acmebilling/gateway:dev`,
		`nothing to change here`:      `nothing to change here`,
		`services/quotifyctl/main.go`: `services/acmebillingctl/main.go`,
	} {
		if got := s.rewrite(in); got != want {
			t.Errorf("rewrite(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, bad := range []renameSpec{{Display: "A", Slug: "Bad Slug"}, {Display: "A", Slug: "-x"}, {Display: "A", Slug: "two-words"}, {Display: `A"B`, Slug: "ab"}, {Display: " ", Slug: "ab"}} {
		if bad.validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if err := (renameSpec{Display: "Acme Billing", Slug: "acmebilling"}).validate(); err != nil {
		t.Error(err)
	}
}

func TestPlanAndApply(t *testing.T) {
	root := t.TempDir()
	write := func(p, text string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("deploy/quotify/Chart.yaml", "name: quotify\n")
	write("LICENSE", "Quotify licensor\n")
	write("go.sum", "github.com/x/quotify v1 h1:abc=\n")
	write("logo.png", "quotify\x00\x01")
	write("main.go", "// Quotify\n")
	spec := renameSpec{Display: "Acme", Slug: "acme"}
	plan, err := planRename(root, []string{"deploy/quotify/Chart.yaml", "LICENSE", "go.sum", "logo.png", "main.go"}, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Edits) != 2 || plan.Edits["main.go"] != "// Acme\n" || plan.Edits["deploy/quotify/Chart.yaml"] != "name: acme\n" {
		t.Fatalf("edits = %v", plan.Edits)
	}
	if len(plan.Renames) != 1 || plan.Renames[0][1] != "deploy/acme/Chart.yaml" {
		t.Fatalf("renames = %v", plan.Renames)
	}
	if err := plan.apply(root, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "deploy/acme/Chart.yaml")); string(b) != "name: acme\n" {
		t.Fatalf("moved file = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "deploy/quotify")); !os.IsNotExist(err) {
		t.Error("the emptied directory was left behind")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "LICENSE")); string(b) != "Quotify licensor\n" {
		t.Fatal("the licence was rewritten")
	}
}
