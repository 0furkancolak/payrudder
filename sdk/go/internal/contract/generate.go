package contract

import (
	"bytes"
	"fmt"
	"go/format"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Operations whose Go method is written by hand in the SDK, and why.
var handwritten = map[string]string{
	"ingestEvents": "events.go: splits batches, fills idempotency keys",
	"uploadAssets": "assets.go: builds the multipart body",
}

// responseOverride names a hand-written result type for an operation whose success schema is a oneOf.
var responseOverride = map[string]string{
	"cancelSubscription": "CancelSubscriptionResult",
}

// pageItemsOverride picks the list property to iterate when a page has more than one array.
var pageItemsOverride = map[string]string{
	"listAffiliateJournal": "entries",
}

// reserved are names the SDK defines by hand; a spec schema with one of them would clash.
var reserved = map[string]bool{
	"Client": true, "Error": true, "Int64": true, "Decimal": true, "Option": true, "RequestOption": true,
	"WebhookEvent": true, "Page": true,
	"CancelSubscriptionResult": true,
}

var initialisms = map[string]string{
	"id": "ID", "url": "URL", "uri": "URI", "pdf": "PDF", "api": "API", "http": "HTTP", "https": "HTTPS", "uuid": "UUID",
	"vat": "VAT", "csv": "CSV", "json": "JSON", "ip": "IP", "html": "HTML", "tls": "TLS", "ttl": "TTL", "utc": "UTC",
	"iso": "ISO", "psp": "PSP", "sha256": "SHA256", "ui": "UI",
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true, "default": true, "defer": true,
	"else": true, "fallthrough": true, "for": true, "func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true, "select": true, "struct": true,
	"switch": true, "type": true, "var": true,
}

func words(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == '.' || r == ' ' || r == '/' })
}

func titleWord(w string) string {
	if v, ok := initialisms[strings.ToLower(w)]; ok {
		return v
	}
	if w == "" {
		return w
	}
	return strings.ToUpper(w[:1]) + w[1:]
}

// Pascal turns a snake_case or kebab-case name into an exported Go name.
func Pascal(s string) string {
	var b strings.Builder
	for _, w := range words(s) {
		b.WriteString(titleWord(w))
	}
	return b.String()
}

func lowerCamel(s string) string {
	ws := words(s)
	var b strings.Builder
	for i, w := range ws {
		if i == 0 {
			b.WriteString(strings.ToLower(w[:1]) + w[1:])
			continue
		}
		b.WriteString(titleWord(w))
	}
	r := b.String()
	if goKeywords[r] {
		r += "_"
	}
	return r
}

var spaces = regexp.MustCompile(`\s+`)

func oneLine(s string) string {
	s = strings.TrimSpace(spaces.ReplaceAllString(s, " "))
	return s
}

func comment(indent, text string) string {
	text = oneLine(text)
	if text == "" {
		return ""
	}
	var lines []string
	for len(text) > 110 {
		cut := strings.LastIndex(text[:110], " ")
		if cut < 40 {
			cut = strings.Index(text, " ")
			if cut < 0 {
				break
			}
		}
		lines = append(lines, text[:cut])
		text = strings.TrimSpace(text[cut:])
	}
	lines = append(lines, text)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(indent + "// " + l + "\n")
	}
	return b.String()
}

type structDef struct {
	name string
	doc  string
	body string
}

type gen struct {
	spec    *Spec
	ops     []Op
	structs map[string]*structDef
	// reqReach holds the component schemas some request body can contain: their optional scalars become pointers
	// so that a zero value can be sent.
	reqReach map[string]bool
	inlineN  map[string]int
	queryTys []string
	err      error
}

func (g *gen) fail(format string, a ...any) {
	if g.err == nil {
		g.err = fmt.Errorf(format, a...)
	}
}

// Generate returns the content of types_gen.go and services_gen.go.
func Generate(spec *Spec) (types, services []byte, err error) {
	ops, err := spec.TenantOps()
	if err != nil {
		return nil, nil, err
	}
	g := &gen{spec: spec, ops: ops, structs: map[string]*structDef{}, reqReach: map[string]bool{}, inlineN: map[string]int{}}
	g.computeRequestReach()

	svcs := map[string][]string{}
	var svcOrder []string
	var methodsBySvc = map[string]*bytes.Buffer{}
	for _, op := range ops {
		if _, hand := handwritten[op.OperationID]; hand {
			g.noteService(op, svcs, &svcOrder, methodsBySvc)
			// The method is written by hand but its types are generated.
			g.responseType(op, MethodName(op.OperationID))
			if op.RequestBody != nil {
				if mt, ok := op.RequestBody.Content["application/json"]; ok {
					g.goType(mt.Schema, MethodName(op.OperationID)+"Request", "", true)
				}
			}
			continue
		}
		seg := g.noteService(op, svcs, &svcOrder, methodsBySvc)
		_, _ = methodsBySvc[seg].WriteString(g.method(op, seg))
	}
	if g.err != nil {
		return nil, nil, g.err
	}

	tb := &bytes.Buffer{}
	tb.WriteString(header)
	tb.WriteString("package payrudder\n\nimport (\n\t\"encoding/json\"\n\t\"net/url\"\n\t\"strconv\"\n\t\"time\"\n)\n\nvar (\n\t_ json.RawMessage\n\t_ = time.Time{}\n\t_ = strconv.Itoa\n\t_ = url.PathEscape\n)\n\n")
	names := make([]string, 0, len(g.structs))
	for n := range g.structs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := g.structs[n]
		tb.WriteString(comment("", d.doc))
		tb.WriteString("type " + n + " struct {\n" + d.body + "}\n\n")
	}
	for _, q := range g.queryTys {
		tb.WriteString(q)
	}

	sb := &bytes.Buffer{}
	sb.WriteString(header)
	sb.WriteString("package payrudder\n\nimport (\n\t\"context\"\n\t\"iter\"\n\t\"net/http\"\n\t\"net/url\"\n\t\"strconv\"\n\t\"time\"\n)\n\n")
	sb.WriteString("var (\n\t_ = strconv.Itoa\n\t_ = time.Time{}\n\t_ iter.Seq2[int, error]\n\t_ = url.PathEscape\n\t_ = http.MethodGet\n)\n\n")
	sort.Strings(svcOrder)
	sb.WriteString("// services holds the resource namespaces of a Client, one per first path segment of /v1.\ntype services struct {\n")
	for _, s := range svcOrder {
		fmt.Fprintf(sb, "\t// %s groups the %s operations.\n\t%s *%sService\n", s, spaced(s), s, s)
	}
	sb.WriteString("}\n\nfunc (s *services) initServices(c *Client) {\n")
	for _, s := range svcOrder {
		fmt.Fprintf(sb, "\ts.%s = (*%sService)(&service{c: c})\n", s, s)
	}
	sb.WriteString("}\n\n")
	for _, s := range svcOrder {
		fmt.Fprintf(sb, "// %sService is the %s namespace of the API.\ntype %sService service\n\n", s, spaced(s), s)
		sb.Write(methodsBySvc[s].Bytes())
	}
	if g.err != nil {
		return nil, nil, g.err
	}
	t, err := format.Source(tb.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("format types: %w\n%s", err, tb.String())
	}
	sv, err := format.Source(sb.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("format services: %w\n%s", err, sb.String())
	}
	return t, sv, nil
}

const header = "// Code generated by sdk/go/internal/contract/cmd/generate from api/openapi/v1.yaml. DO NOT EDIT.\n\n"

// ServiceName is the namespace of an operation: the PascalCase of the first path segment after /v1.
func ServiceName(path string) string {
	seg := strings.Split(strings.TrimPrefix(path, "/v1/"), "/")[0]
	return Pascal(seg)
}

func (g *gen) noteService(op Op, svcs map[string][]string, order *[]string, m map[string]*bytes.Buffer) string {
	seg := ServiceName(op.Path)
	if _, ok := m[seg]; !ok {
		m[seg] = &bytes.Buffer{}
		*order = append(*order, seg)
	}
	svcs[seg] = append(svcs[seg], op.OperationID)
	return seg
}

func (g *gen) computeRequestReach() {
	seen := map[*Schema]bool{}
	var walk func(s *Schema)
	walk = func(s *Schema) {
		if s == nil || seen[s] {
			return
		}
		seen[s] = true
		if s.Ref != "" {
			n := strings.TrimPrefix(s.Ref, "#/components/schemas/")
			if !g.reqReach[n] {
				g.reqReach[n] = true
				walk(g.spec.Components.Schemas[n])
			}
			return
		}
		walk(s.Items)
		for _, p := range s.Properties {
			walk(p.Schema)
		}
		for _, a := range s.AllOf {
			walk(a)
		}
		for _, a := range s.OneOf {
			walk(a)
		}
		if s.AdditionalProperties != nil {
			walk(s.AdditionalProperties.Schema)
		}
	}
	for _, op := range g.ops {
		if op.RequestBody != nil {
			for _, mt := range op.RequestBody.Content {
				walk(mt.Schema)
			}
		}
	}
}

// goType maps a schema to a Go type. Named structs are registered as a side effect. ptrable says whether a pointer
// to the type can express "absent" (scalars, times and structs can; slices, maps and raw JSON already can).
func (g *gen) goType(s *Schema, hint, jsonName string, inReq bool) (typ string, ptrable bool, nullable bool) {
	if s == nil {
		return "any", false, false
	}
	nullable = s.Nullable || s.Type.Has("null")
	if s.Ref == "" && len(s.AllOf) == 1 && s.Type.Primary() == "" && len(s.Properties) == 0 {
		t, p, n := g.goType(s.AllOf[0], hint, jsonName, inReq)
		return t, p, n || nullable
	}
	if s.Ref != "" {
		name := strings.TrimPrefix(s.Ref, "#/components/schemas/")
		cs := g.spec.Components.Schemas[name]
		if cs == nil {
			g.fail("unknown schema ref %s", s.Ref)
			return "any", false, false
		}
		if cs.Type.Primary() == "object" && len(cs.Properties) > 0 {
			g.ensureStruct(name, cs, g.reqReach[name], cs.Description)
			return name, true, nullable
		}
		t, p, n := g.goType(cs, name, jsonName, inReq)
		return t, p, n || nullable
	}
	if len(s.OneOf) > 0 {
		if len(s.OneOf) == 2 && s.OneOf[0].Type.Primary() == "string" && s.OneOf[1].Type.Primary() == "number" {
			return "Decimal", true, nullable
		}
		return "json.RawMessage", false, nullable
	}
	switch s.Type.Primary() {
	case "object", "":
		if len(s.Properties) > 0 {
			name := g.inlineName(hint)
			g.ensureStruct(name, s, inReq, s.Description)
			return name, true, nullable
		}
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			t, _, _ := g.goType(s.AdditionalProperties.Schema, hint+"Value", jsonName, inReq)
			return "map[string]" + t, false, nullable
		}
		if s.Type.Primary() == "" && len(s.Enum) == 0 && s.Format == "" {
			return "any", false, nullable
		}
		return "map[string]any", false, nullable
	case "array":
		t, _, _ := g.goType(s.Items, hint, jsonName, inReq)
		return "[]" + t, false, nullable
	case "string":
		switch {
		case s.Format == "date-time":
			return "time.Time", true, nullable
		case s.Format == "binary":
			return "[]byte", false, nullable
		case strings.HasSuffix(jsonName, "_minor") || strings.Contains(strings.ToLower(s.Description), "int64"):
			return "Int64", true, nullable
		}
		return "string", true, nullable
	case "integer":
		if s.Format == "int64" {
			return "Int64", true, nullable
		}
		return "int", true, nullable
	case "number":
		return "float64", true, nullable
	case "boolean":
		return "bool", true, nullable
	}
	g.fail("unsupported schema type %v at %s", s.Type, hint)
	return "any", false, false
}

func (g *gen) inlineName(hint string) string {
	if _, taken := g.structs[hint]; !taken {
		return hint
	}
	return hint
}

func (g *gen) ensureStruct(name string, s *Schema, inReq bool, doc string) {
	if d, ok := g.structs[name]; ok {
		if d.body == "" {
			return // being built (recursive schema)
		}
		return
	}
	if reserved[name] {
		g.fail("schema %s clashes with a hand-written SDK name", name)
		return
	}
	d := &structDef{name: name, doc: structDoc(name, doc)}
	g.structs[name] = d // registered before the fields so a recursive reference terminates
	req := map[string]bool{}
	for _, r := range s.Required {
		req[r] = true
	}
	var b strings.Builder
	used := map[string]string{}
	for _, p := range s.Properties {
		fname := Pascal(p.Name)
		if prev, dup := used[fname]; dup {
			g.fail("%s: properties %s and %s both become %s", name, prev, p.Name, fname)
			return
		}
		used[fname] = p.Name
		t, ptrable, nullable := g.goType(p.Schema, name+Pascal(p.Name), p.Name, inReq)
		required := req[p.Name]
		if ptrable && (nullable || (!required && inReq && t != "string")) {
			t = "*" + t
		}
		tag := p.Name
		if !required {
			tag += ",omitempty"
		}
		doc := p.Schema.Description
		if len(p.Schema.Enum) > 0 {
			var vs []string
			for _, e := range p.Schema.Enum {
				vs = append(vs, fmt.Sprint(e))
			}
			doc = sentence(doc)
			doc += "One of: " + strings.Join(vs, ", ") + "."
		}
		if doc != "" {
			b.WriteString(comment("\t", doc))
		}
		fmt.Fprintf(&b, "\t%s %s `json:\"%s\"`\n", fname, t, tag)
	}
	d.body = b.String()
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func structDoc(name, desc string) string {
	if d := oneLine(desc); d != "" {
		return name + ": " + d
	}
	switch {
	case strings.HasSuffix(name, "Request"):
		return name + " is the request body of " + strings.TrimSuffix(name, "Request") + "."
	case strings.HasSuffix(name, "Response"):
		return name + " is the response of " + strings.TrimSuffix(name, "Response") + "."
	}
	return name + " is an object of the API; see components.schemas." + name + " in api/openapi/v1.yaml."
}

// sentence ends a description with a full stop and a space so that more text can follow; empty stays empty.
func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "!") && !strings.HasSuffix(s, "?") {
		s += "."
	}
	return s + " "
}

// spaced turns CouponRedemptions into "coupon redemptions".
func spaced(s string) string {
	var out []rune
	rs := []rune(s)
	for i, r := range rs {
		if i > 0 && unicode.IsUpper(r) && !unicode.IsUpper(rs[i-1]) {
			out = append(out, ' ')
		}
		out = append(out, unicode.ToLower(r))
	}
	return string(out)
}

type pathPart struct {
	lit   string
	param string
}

func splitPath(p string) []pathPart {
	var parts []pathPart
	for len(p) > 0 {
		i := strings.Index(p, "{")
		if i < 0 {
			parts = append(parts, pathPart{lit: p})
			break
		}
		j := strings.Index(p, "}")
		if i > 0 {
			parts = append(parts, pathPart{lit: p[:i]})
		}
		parts = append(parts, pathPart{param: p[i+1 : j]})
		p = p[j+1:]
	}
	return parts
}

func (g *gen) method(op Op, svc string) string {
	name := MethodName(op.OperationID)
	var b strings.Builder

	// Path parameters, in the order of the path.
	pathArgs := ""
	var pathExpr []string
	pparams := map[string]*Param{}
	for _, p := range op.Parameters {
		if p.In == "path" {
			pparams[p.Name] = p
		}
	}
	for _, part := range splitPath(op.Path) {
		if part.param == "" {
			pathExpr = append(pathExpr, fmt.Sprintf("%q", part.lit))
			continue
		}
		if pparams[part.param] == nil {
			g.fail("%s: path parameter %s is not declared", op.OperationID, part.param)
			return ""
		}
		a := lowerCamel(part.param)
		pathArgs += ", " + a + " string"
		pathExpr = append(pathExpr, "url.PathEscape("+a+")")
	}

	// Query parameters.
	var qparams []*Param
	for _, p := range op.Parameters {
		if p.In == "query" {
			qparams = append(qparams, p)
		}
	}
	paramsTy := ""
	if len(qparams) > 0 {
		paramsTy = name + "Params"
		g.queryTys = append(g.queryTys, g.paramsStruct(paramsTy, op, qparams))
	}

	// Request body.
	bodyArg := ""
	bodyPrep := ""
	bodyExpr := "nil"
	if op.RequestBody != nil {
		var ms *Schema
		for ct, mt := range op.RequestBody.Content {
			if ct == "application/json" {
				ms = mt.Schema
			} else {
				g.fail("%s: unsupported request content type %s", op.OperationID, ct)
			}
		}
		t, _, _ := g.goType(ms, name+"Request", "", true)
		if strings.HasPrefix(t, "map[") {
			bodyArg = ", req " + t
			if op.RequestBody.Required {
				bodyPrep = "\tif req == nil {\n\t\treq = " + t + "{}\n\t}\n\tvar body any = req\n"
			} else {
				bodyPrep = "\tvar body any\n\tif req != nil {\n\t\tbody = req\n\t}\n"
			}
		} else {
			bodyArg = ", req *" + t
			if op.RequestBody.Required {
				bodyPrep = "\tif req == nil {\n\t\treq = &" + t + "{}\n\t}\n\tvar body any = req\n"
			} else {
				bodyPrep = "\tvar body any\n\tif req != nil {\n\t\tbody = req\n\t}\n"
			}
		}
		bodyExpr = "body"
	}
	queryArg := ""
	queryExpr := "nil"
	if paramsTy != "" {
		queryArg = ", params *" + paramsTy
		queryExpr = "params.values()"
	}

	// Response.
	retTy, outDecl, outRef, noBody := g.responseType(op, name)

	sig := fmt.Sprintf("ctx context.Context%s%s%s, opts ...RequestOption", pathArgs, bodyArg, queryArg)
	ret := "error"
	if !noBody {
		ret = "(" + retTy + ", error)"
	}
	doc := firstNonEmpty(op.Summary, op.OperationID)
	b.WriteString(comment("", name+": "+strings.TrimSuffix(doc, ".")+" ("+op.Method+" "+op.Path+")."))
	if d := oneLine(op.Description); d != "" {
		b.WriteString("//\n")
		b.WriteString(comment("", d))
	}
	if _, ok := hasIdempotencyHeader(op); ok {
		b.WriteString("//\n// Pass WithIdempotencyKey to make a retry safe.\n")
	}
	fmt.Fprintf(&b, "func (s *%sService) %s(%s) %s {\n", svc, name, sig, ret)
	b.WriteString(bodyPrep)
	if outDecl != "" {
		b.WriteString("\t" + outDecl + "\n")
	}
	pathCode := strings.Join(pathExpr, " + ")
	call := fmt.Sprintf("s.c.do(ctx, requestSpec{method: http.Method%s, path: %s, query: %s, body: %s}, %s, opts)", methodConst(op.Method), pathCode, queryExpr, bodyExpr, outRef)
	if noBody {
		fmt.Fprintf(&b, "\treturn %s\n}\n\n", call)
	} else {
		fmt.Fprintf(&b, "\tif err := %s; err != nil {\n\t\treturn %s, err\n\t}\n\treturn %s, nil\n}\n\n", call, zeroOf(retTy), returnExpr(retTy))
	}

	// Pagination helper.
	if p := g.pager(op, name, svc, paramsTy, qparams); p != "" {
		b.WriteString(p)
	}
	return b.String()
}

func hasIdempotencyHeader(op Op) (*Param, bool) {
	for _, p := range op.Parameters {
		if p.In == "header" && strings.EqualFold(p.Name, "Idempotency-Key") {
			return p, true
		}
	}
	return nil, false
}

func methodConst(m string) string {
	return strings.ToUpper(m[:1]) + strings.ToLower(m[1:])
}

// responseType returns the Go result type, the declaration of the result variable, the expression passed to do and
// whether the operation has no response body.
func (g *gen) responseType(op Op, name string) (retTy, outDecl, outRef string, noBody bool) {
	if ov, ok := responseOverride[op.OperationID]; ok {
		return "*" + ov, "var out " + ov, "&out", false
	}
	codes := make([]string, 0, len(op.Responses))
	for c := range op.Responses {
		if strings.HasPrefix(c, "2") {
			codes = append(codes, c)
		}
	}
	sort.Strings(codes)
	var content map[string]MediaType
	for _, c := range codes {
		r := op.Responses[c]
		if len(r.Content) > 0 {
			content = r.Content
			break
		}
	}
	if len(content) == 0 {
		return "", "", "nil", true
	}
	for ct, mt := range content {
		switch ct {
		case "application/json":
			t, _, _ := g.goType(mt.Schema, name+"Response", "", false)
			if g.structs[t] != nil {
				return "*" + t, "var out " + t, "&out", false
			}
			return t, "var out " + t, "&out", false
		case "text/csv", "application/octet-stream":
			return "[]byte", "var out []byte", "&out", false
		default:
			g.fail("%s: unsupported response content type %s", op.OperationID, ct)
		}
	}
	return "", "", "nil", true
}

func zeroOf(t string) string {
	if strings.HasPrefix(t, "*") || strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") {
		return "nil"
	}
	return "out"
}

func returnExpr(t string) string {
	if strings.HasPrefix(t, "*") {
		return "&out"
	}
	return "out"
}

func (g *gen) paramsStruct(name string, op Op, qs []*Param) string {
	var b strings.Builder
	var vals strings.Builder
	fmt.Fprintf(&b, "// %s are the query parameters of %s.\ntype %s struct {\n", name, MethodName(op.OperationID), name)
	vals.WriteString("func (p *" + name + ") values() url.Values {\n\tv := url.Values{}\n\tif p == nil {\n\t\treturn v\n\t}\n")
	for _, q := range qs {
		if q.Schema != nil && q.Schema.Ref != "" {
			q = &Param{Name: q.Name, Description: q.Description, Schema: g.spec.Components.Schemas[strings.TrimPrefix(q.Schema.Ref, "#/components/schemas/")]}
		}
		f := Pascal(q.Name)
		doc := q.Description
		if len(q.Schema.Enum) > 0 {
			var vs []string
			for _, e := range q.Schema.Enum {
				vs = append(vs, fmt.Sprint(e))
			}
			doc = strings.TrimSpace(sentence(doc) + "One of: " + strings.Join(vs, ", ") + ".")
		}
		if doc != "" {
			b.WriteString(comment("\t", doc))
		}
		switch {
		case q.Schema.Type.Primary() == "string" && q.Schema.Format == "date-time":
			fmt.Fprintf(&b, "\t%s *time.Time\n", f)
			fmt.Fprintf(&vals, "\tif p.%s != nil {\n\t\tv.Set(%q, p.%s.UTC().Format(time.RFC3339Nano))\n\t}\n", f, q.Name, f)
		case q.Schema.Type.Primary() == "string":
			fmt.Fprintf(&b, "\t%s string\n", f)
			fmt.Fprintf(&vals, "\tif p.%s != \"\" {\n\t\tv.Set(%q, p.%s)\n\t}\n", f, q.Name, f)
		case q.Schema.Type.Primary() == "integer" && q.Schema.Format == "int64":
			fmt.Fprintf(&b, "\t%s *int64\n", f)
			fmt.Fprintf(&vals, "\tif p.%s != nil {\n\t\tv.Set(%q, strconv.FormatInt(*p.%s, 10))\n\t}\n", f, q.Name, f)
		case q.Schema.Type.Primary() == "integer":
			fmt.Fprintf(&b, "\t%s *int\n", f)
			fmt.Fprintf(&vals, "\tif p.%s != nil {\n\t\tv.Set(%q, strconv.Itoa(*p.%s))\n\t}\n", f, q.Name, f)
		case q.Schema.Type.Primary() == "boolean":
			fmt.Fprintf(&b, "\t%s *bool\n", f)
			fmt.Fprintf(&vals, "\tif p.%s != nil {\n\t\tv.Set(%q, strconv.FormatBool(*p.%s))\n\t}\n", f, q.Name, f)
		case q.Schema.Type.Primary() == "number":
			fmt.Fprintf(&b, "\t%s *float64\n", f)
			fmt.Fprintf(&vals, "\tif p.%s != nil {\n\t\tv.Set(%q, strconv.FormatFloat(*p.%s, 'f', -1, 64))\n\t}\n", f, q.Name, f)
		default:
			g.fail("%s: unsupported query parameter %s", op.OperationID, q.Name)
		}
	}
	b.WriteString("}\n\n")
	vals.WriteString("\treturn v\n}\n\n")
	return b.String() + vals.String()
}

// pager emits the iterator for a list operation that takes page_token and answers next_page_token.
func (g *gen) pager(op Op, name, svc, paramsTy string, qs []*Param) string {
	hasToken := false
	for _, q := range qs {
		if q.Name == "page_token" {
			hasToken = true
		}
	}
	if !hasToken {
		return ""
	}
	var schema *Schema
	for _, c := range []string{"200", "201"} {
		if r, ok := op.Responses[c]; ok {
			if mt, ok := r.Content["application/json"]; ok {
				schema = mt.Schema
			}
		}
	}
	if schema == nil {
		return ""
	}
	if schema.Ref != "" {
		schema = g.spec.Components.Schemas[strings.TrimPrefix(schema.Ref, "#/components/schemas/")]
	}
	var next bool
	var arrays []Prop
	for _, p := range schema.Properties {
		if p.Name == "next_page_token" {
			next = true
		}
		if p.Schema.Type.Primary() == "array" {
			arrays = append(arrays, p)
		}
	}
	if !next {
		return ""
	}
	var items *Prop
	if want, ok := pageItemsOverride[op.OperationID]; ok {
		for i := range arrays {
			if arrays[i].Name == want {
				items = &arrays[i]
			}
		}
	} else if len(arrays) == 1 {
		items = &arrays[0]
	}
	if items == nil {
		g.fail("%s: cannot tell which list property to iterate, add it to pageItemsOverride", op.OperationID)
		return ""
	}
	respName := name + "Response"
	if schema != nil && op.Responses["200"].Content["application/json"].Schema != nil && op.Responses["200"].Content["application/json"].Schema.Ref != "" {
		respName = strings.TrimPrefix(op.Responses["200"].Content["application/json"].Schema.Ref, "#/components/schemas/")
	}
	elem, _, _ := g.goType(items.Schema.Items, respName+Pascal(items.Name), "", false)
	var pathArgs, pathPass string
	for _, part := range splitPath(op.Path) {
		if part.param != "" {
			a := lowerCamel(part.param)
			pathArgs += ", " + a + " string"
			pathPass += ", " + a
		}
	}
	var b strings.Builder
	b.WriteString(comment("", name+"All walks every page of "+name+" and yields the items one by one. It stops at the first error, which is yielded once."))
	fmt.Fprintf(&b, "func (s *%sService) %sAll(ctx context.Context%s, params *%s, opts ...RequestOption) iter.Seq2[%s, error] {\n", svc, name, pathArgs, paramsTy, elem)
	fmt.Fprintf(&b, "\treturn paginate(func(token string) ([]%s, string, error) {\n", elem)
	fmt.Fprintf(&b, "\t\tvar p %s\n\t\tif params != nil {\n\t\t\tp = *params\n\t\t}\n\t\tp.PageToken = token\n", paramsTy)
	fmt.Fprintf(&b, "\t\tpage, err := s.%s(ctx%s, &p, opts...)\n\t\tif err != nil {\n\t\t\treturn nil, \"\", err\n\t\t}\n", name, pathPass)
	fmt.Fprintf(&b, "\t\treturn page.%s, page.NextPageToken, nil\n\t})\n}\n\n", Pascal(items.Name))
	return b.String()
}
