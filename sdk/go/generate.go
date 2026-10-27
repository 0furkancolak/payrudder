package payrudder

// types_gen.go and services_gen.go are written from api/openapi/v1.yaml by the generator in internal/contract, a
// separate module (it needs a YAML parser; the SDK itself uses only the standard library).
// A test in that module fails when these files are out of date.
//
//go:generate sh -c "cd internal/contract && GOWORK=off go run ./cmd/generate"
