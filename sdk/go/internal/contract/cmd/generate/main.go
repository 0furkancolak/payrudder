// Command generate writes types_gen.go and services_gen.go of the SDK from api/openapi/v1.yaml.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/0furkancolak/payrudder/sdk/go/internal/contract"
)

func main() {
	specPath := flag.String("spec", "../../../../api/openapi/v1.yaml", "path of the OpenAPI document")
	out := flag.String("out", "../..", "directory of the SDK package")
	flag.Parse()
	spec, err := contract.Load(*specPath)
	if err != nil {
		fail(err)
	}
	types, services, err := contract.Generate(spec)
	if err != nil {
		fail(err)
	}
	for name, b := range map[string][]byte{"types_gen.go": types, "services_gen.go": services} {
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil { //nolint:gosec // source files
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "generate:", err)
	os.Exit(1)
}
