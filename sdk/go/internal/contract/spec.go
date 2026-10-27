// Package contract reads api/openapi/v1.yaml and generates the typed part of the Go SDK from it. It is a separate
// module so that the SDK itself keeps no dependency besides the standard library.
package contract

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Spec is the part of an OpenAPI 3.1 document the generator needs.
type Spec struct {
	Paths      map[string]PathItem `yaml:"paths"`
	Components struct {
		Schemas    map[string]*Schema   `yaml:"schemas"`
		Parameters map[string]*Param    `yaml:"parameters"`
		Responses  map[string]*Response `yaml:"responses"`
	} `yaml:"components"`
}

// PathItem maps a lower-case HTTP method to its operation.
type PathItem map[string]yaml.Node

// Operation is one HTTP operation.
type Operation struct {
	OperationID string              `yaml:"operationId"`
	Summary     string              `yaml:"summary"`
	Description string              `yaml:"description"`
	Parameters  []*Param            `yaml:"parameters"`
	RequestBody *RequestBody        `yaml:"requestBody"`
	Responses   map[string]Response `yaml:"responses"`
}

// Param is a path, query or header parameter.
type Param struct {
	Ref         string  `yaml:"$ref"`
	Name        string  `yaml:"name"`
	In          string  `yaml:"in"`
	Required    bool    `yaml:"required"`
	Description string  `yaml:"description"`
	Schema      *Schema `yaml:"schema"`
}

// RequestBody is the body of an operation.
type RequestBody struct {
	Required bool                 `yaml:"required"`
	Content  map[string]MediaType `yaml:"content"`
}

// MediaType holds the schema of one content type.
type MediaType struct {
	Schema *Schema `yaml:"schema"`
}

// Response is one response of an operation.
type Response struct {
	Ref         string               `yaml:"$ref"`
	Description string               `yaml:"description"`
	Content     map[string]MediaType `yaml:"content"`
}

// Schema is a JSON schema as the spec uses it.
type Schema struct {
	Ref                  string      `yaml:"$ref"`
	Type                 TypeSet     `yaml:"type"`
	Format               string      `yaml:"format"`
	Description          string      `yaml:"description"`
	Enum                 []any       `yaml:"enum"`
	Properties           Properties  `yaml:"properties"`
	Required             []string    `yaml:"required"`
	Items                *Schema     `yaml:"items"`
	AdditionalProperties *Additional `yaml:"additionalProperties"`
	AllOf                []*Schema   `yaml:"allOf"`
	OneOf                []*Schema   `yaml:"oneOf"`
	Nullable             bool        `yaml:"nullable"`
}

// TypeSet is `type: x` or `type: [x, "null"]`.
type TypeSet []string

// UnmarshalYAML accepts a scalar or a sequence.
func (t *TypeSet) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*t = TypeSet{n.Value}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			*t = append(*t, c.Value)
		}
	default:
		return fmt.Errorf("line %d: unsupported type node", n.Line)
	}
	return nil
}

// Has reports whether the set contains s.
func (t TypeSet) Has(s string) bool {
	for _, x := range t {
		if x == s {
			return true
		}
	}
	return false
}

// Primary is the first type that is not "null".
func (t TypeSet) Primary() string {
	for _, x := range t {
		if x != "null" {
			return x
		}
	}
	return ""
}

// Additional is `additionalProperties: true|false|<schema>`.
type Additional struct {
	Any    bool
	Schema *Schema
}

// UnmarshalYAML accepts a boolean or a schema.
func (a *Additional) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		a.Any = n.Value == "true"
		return nil
	}
	a.Schema = &Schema{}
	return n.Decode(a.Schema)
}

// Prop is one property of an object schema.
type Prop struct {
	Name   string
	Schema *Schema
}

// Properties keeps the order of the document, which the generated structs follow.
type Properties []Prop

// UnmarshalYAML reads a mapping in document order.
func (p *Properties) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: properties must be a mapping", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		s := &Schema{}
		if err := n.Content[i+1].Decode(s); err != nil {
			return err
		}
		*p = append(*p, Prop{Name: n.Content[i].Value, Schema: s})
	}
	return nil
}

// Op is an operation with its place in the document.
type Op struct {
	Operation
	Method string // upper case
	Path   string
}

// Load parses the document at path.
func Load(path string) (*Spec, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path of the spec, given by the developer running the generator
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := yaml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

var httpMethods = map[string]bool{"get": true, "put": true, "post": true, "delete": true, "patch": true}

// TenantOps returns every operation under /v1/, sorted by path then method. These are the tenant API.
func (s *Spec) TenantOps() ([]Op, error) {
	var ops []Op
	for p, item := range s.Paths {
		if !strings.HasPrefix(p, "/v1/") {
			continue
		}
		for m, node := range item {
			if !httpMethods[m] {
				continue
			}
			n := node
			var o Operation
			if err := n.Decode(&o); err != nil {
				return nil, fmt.Errorf("%s %s: %w", m, p, err)
			}
			if o.OperationID == "" {
				return nil, fmt.Errorf("%s %s has no operationId", m, p)
			}
			// Resolve parameter refs once so later code sees names and schemas.
			for i, pr := range o.Parameters {
				if pr.Ref != "" {
					r := s.Components.Parameters[strings.TrimPrefix(pr.Ref, "#/components/parameters/")]
					if r == nil {
						return nil, fmt.Errorf("%s: unknown parameter %s", o.OperationID, pr.Ref)
					}
					o.Parameters[i] = r
				}
			}
			ops = append(ops, Op{Operation: o, Method: strings.ToUpper(m), Path: p})
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].Path != ops[j].Path {
			return ops[i].Path < ops[j].Path
		}
		return ops[i].Method < ops[j].Method
	})
	return ops, nil
}

// MethodName is the Go method name of an operation: the PascalCase of its operationId.
func MethodName(operationID string) string {
	if operationID == "" {
		return ""
	}
	return strings.ToUpper(operationID[:1]) + operationID[1:]
}
