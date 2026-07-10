// Command gen emits the Feishu-style SDK layer (namespaced services, fluent
// request builders, and typed response wrappers) from the hduhelp-neo OpenAPI
// spec. It complements oapi-codegen, which generates the plain model structs
// in package models; this generator wires those models into ergonomic services.
//
// Usage: gen <normalized-openapi.yaml> <module-path> <repo-root>
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---- spec model (only the fields we need) ----

type schema struct {
	Ref        string            `yaml:"$ref"`
	Type       string            `yaml:"type"`
	Format     string            `yaml:"format"`
	Items      *schema           `yaml:"items"`
	Properties map[string]schema `yaml:"properties"`
}

type mediaType struct {
	Schema schema `yaml:"schema"`
}

type param struct {
	Name        string `yaml:"name"`
	In          string `yaml:"in"`
	Description string `yaml:"description"`
	Schema      schema `yaml:"schema"`
}

type operation struct {
	Tags        []string `yaml:"tags"`
	OperationID string   `yaml:"operationId"`
	Summary     string   `yaml:"summary"`
	Description string   `yaml:"description"`
	Parameters  []param  `yaml:"parameters"`
	RequestBody *struct {
		Content map[string]mediaType `yaml:"content"`
	} `yaml:"requestBody"`
	Responses map[string]struct {
		Content map[string]mediaType `yaml:"content"`
	} `yaml:"responses"`
}

type spec struct {
	Paths      map[string]map[string]operation `yaml:"paths"`
	Components struct {
		Schemas map[string]schema `yaml:"schemas"`
	} `yaml:"components"`
}

var httpMethods = []string{"get", "post", "put", "delete", "patch"}

// ---- resolved intermediate representation ----

type field struct {
	Setter  string // exported setter/method name
	RawName string // spec parameter name (query/path key)
	GoType  string // scalar Go type of the setter argument
	Conv    string // expression converting the arg `v` to a string
	IsPath  bool
	Doc     string
}

type endpoint struct {
	Method    string // Go method name
	HTTPVerb  string // "GET"
	Path      string
	Summary   string
	Fields    []field // query + path params
	BodyType  string  // e.g. "*models.XxxRequestBody"; empty if none
	DataType  string  // e.g. "*models.XxxData", "[]models.Y", "string", "json.RawMessage"; empty = no Data field
	NeedStrmt bool
	NeedJSON  bool
	NeedModel bool
}

type service struct {
	Package   string // e.g. "academic"
	GoName    string // e.g. "Academic"
	Endpoints []endpoint
}

func main() {
	if len(os.Args) != 4 {
		fail("usage: gen <normalized-openapi.yaml> <module-path> <repo-root>")
	}
	specPath, modulePath, repoRoot := os.Args[1], os.Args[2], os.Args[3]

	raw, err := os.ReadFile(specPath)
	must(err)
	var doc spec
	must(yaml.Unmarshal(raw, &doc))

	services := build(&doc)

	for _, svc := range services {
		emitService(svc, modulePath, repoRoot)
	}
	emitClient(services, modulePath, repoRoot)

	fmt.Printf("generated %d service packages\n", len(services))
}

func build(doc *spec) []*service {
	byTag := map[string]*service{}
	var order []string

	paths := sortedKeys(doc.Paths)
	for _, path := range paths {
		methods := doc.Paths[path]
		for _, verb := range httpMethods {
			op, ok := methods[verb]
			if !ok {
				continue
			}
			if len(op.Tags) == 0 {
				continue
			}
			tag := op.Tags[0]
			svc := byTag[tag]
			if svc == nil {
				pkg := strings.ToLower(strings.TrimSuffix(tag, "Service"))
				svc = &service{Package: pkg, GoName: strings.TrimSuffix(tag, "Service")}
				byTag[tag] = svc
				order = append(order, tag)
			}
			svc.Endpoints = append(svc.Endpoints, resolveEndpoint(doc, path, verb, op))
		}
	}

	var out []*service
	for _, tag := range order {
		svc := byTag[tag]
		dedupeMethodNames(svc)
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}

func resolveEndpoint(doc *spec, path, verb string, op operation) endpoint {
	method := op.OperationID
	if i := strings.Index(method, "_"); i >= 0 {
		method = method[i+1:]
	}
	ep := endpoint{
		Method:   exportIdent(method),
		HTTPVerb: strings.ToUpper(verb),
		Path:     path,
		Summary:  firstLine(op.Summary),
	}

	for _, p := range op.Parameters {
		if p.In != "query" && p.In != "path" {
			continue
		}
		goType, conv, needStr := scalarType(p.Schema)
		if needStr {
			ep.NeedStrmt = true
		}
		ep.Fields = append(ep.Fields, field{
			Setter:  exportIdent(p.Name),
			RawName: p.Name,
			GoType:  goType,
			Conv:    conv,
			IsPath:  p.In == "path",
			Doc:     firstLine(p.Description),
		})
	}

	if op.RequestBody != nil {
		if mt, ok := op.RequestBody.Content["application/json"]; ok {
			if name := refName(mt.Schema.Ref); name != "" {
				ep.BodyType = "*models." + name
				ep.NeedModel = true
			} else {
				ep.BodyType = "any"
			}
		}
	}

	if resp, ok := op.Responses["200"]; ok {
		if mt, ok := resp.Content["application/json"]; ok {
			if name := refName(mt.Schema.Ref); name != "" {
				if body, ok := doc.Components.Schemas[name]; ok {
					if data, ok := body.Properties["data"]; ok {
						dt, needModel, needJSON := dataType(data)
						ep.DataType = dt
						ep.NeedModel = ep.NeedModel || needModel
						ep.NeedJSON = ep.NeedJSON || needJSON
					}
				}
			} else {
				ep.DataType = "json.RawMessage"
				ep.NeedJSON = true
			}
		}
	}
	return ep
}

// dataType maps a response `data` schema to a Go type for resp.Data.
func dataType(s schema) (goType string, needModel, needJSON bool) {
	if name := refName(s.Ref); name != "" {
		return "*models." + name, true, false
	}
	if s.Type == "array" && s.Items != nil {
		if name := refName(s.Items.Ref); name != "" {
			return "[]models." + name, true, false
		}
		if t := scalarGoType(*s.Items); t != "" {
			return "[]" + t, false, false
		}
		return "json.RawMessage", false, true
	}
	if t := scalarGoType(s); t != "" {
		return t, false, false
	}
	return "json.RawMessage", false, true
}

// scalarType returns the setter argument Go type, the conversion of `v` to a
// string, and whether strconv is needed.
func scalarType(s schema) (goType, conv string, needStrconv bool) {
	switch s.Type {
	case "integer":
		if s.Format == "int32" {
			return "int32", "strconv.FormatInt(int64(v), 10)", true
		}
		return "int64", "strconv.FormatInt(v, 10)", true
	case "boolean":
		return "bool", "strconv.FormatBool(v)", true
	case "number":
		if s.Format == "float" {
			return "float32", "strconv.FormatFloat(float64(v), 'g', -1, 32)", true
		}
		return "float64", "strconv.FormatFloat(v, 'g', -1, 64)", true
	case "array":
		return "[]string", "strings.Join(v, \",\")", false // handled specially below
	default:
		return "string", "v", false
	}
}

func scalarGoType(s schema) string {
	switch s.Type {
	case "string":
		return "string"
	case "integer":
		if s.Format == "int32" {
			return "int32"
		}
		return "int64"
	case "boolean":
		return "bool"
	case "number":
		if s.Format == "float" {
			return "float32"
		}
		return "float64"
	default:
		return ""
	}
}

func dedupeMethodNames(svc *service) {
	seen := map[string]int{}
	for i := range svc.Endpoints {
		name := svc.Endpoints[i].Method
		if n, ok := seen[name]; ok {
			seen[name] = n + 1
			svc.Endpoints[i].Method = fmt.Sprintf("%s%d", name, n+1)
		} else {
			seen[name] = 1
		}
	}
}

// ---- emission ----

func emitService(svc *service, modulePath, repoRoot string) {
	needStrconv, needStrings, needJSON, needModel := false, false, false, false
	for _, ep := range svc.Endpoints {
		if ep.NeedStrmt {
			needStrconv = true
		}
		if ep.NeedJSON {
			needJSON = true
		}
		if ep.NeedModel || ep.BodyType != "" && ep.BodyType != "any" {
			needModel = true
		}
		for _, f := range ep.Fields {
			if f.GoType == "[]string" {
				needStrings = true
			}
		}
	}

	var b bytes.Buffer
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("// Code generated by internal/gen; DO NOT EDIT.\n\n")
	p("package %s\n\n", svc.Package)
	p("import (\n")
	p("\t\"context\"\n")
	if needJSON {
		p("\t\"encoding/json\"\n")
	}
	if needStrconv {
		p("\t\"strconv\"\n")
	}
	if needStrings {
		p("\t\"strings\"\n")
	}
	p("\n")
	p("\t%q\n", modulePath+"/core")
	if needModel {
		p("\t%q\n", modulePath+"/models")
	}
	p(")\n\n")

	p("// Service groups the %s endpoints.\n", svc.GoName)
	p("type Service struct{ config *core.Config }\n\n")
	p("// NewService binds the %s service to a client config.\n", svc.GoName)
	p("func NewService(config *core.Config) *Service { return &Service{config: config} }\n\n")

	for _, ep := range svc.Endpoints {
		emitEndpoint(p, ep)
	}

	writeGoFile(filepath.Join(repoRoot, "service", svc.Package, svc.Package+".gen.go"), b.Bytes())
}

func emitEndpoint(p func(string, ...any), ep endpoint) {
	reqType := ep.Method + "Req"
	builderType := ep.Method + "ReqBuilder"
	respType := ep.Method + "Resp"

	// Request + builder.
	p("// %s is the request for %s.\n", reqType, ep.Method)
	p("type %s struct {\n", reqType)
	p("\tpathParams  map[string]string\n")
	p("\tqueryParams map[string]string\n")
	p("\tbody        any\n")
	p("}\n\n")

	p("// %s builds a %s with a fluent setter per field.\n", builderType, reqType)
	p("type %s struct{ req *%s }\n\n", builderType, reqType)

	p("// New%s creates a request builder for %s.\n", builderType, ep.Method)
	p("func New%s() *%s {\n", builderType, builderType)
	p("\treturn &%s{req: &%s{pathParams: map[string]string{}, queryParams: map[string]string{}}}\n", builderType, reqType)
	p("}\n\n")

	for _, f := range ep.Fields {
		target := "queryParams"
		if f.IsPath {
			target = "pathParams"
		}
		if f.Doc != "" {
			p("// %s sets the %q %s parameter: %s\n", f.Setter, f.RawName, paramKind(f.IsPath), f.Doc)
		} else {
			p("// %s sets the %q %s parameter.\n", f.Setter, f.RawName, paramKind(f.IsPath))
		}
		p("func (b *%s) %s(v %s) *%s {\n", builderType, f.Setter, f.GoType, builderType)
		p("\tb.req.%s[%q] = %s\n", target, f.RawName, f.Conv)
		p("\treturn b\n")
		p("}\n\n")
	}

	if ep.BodyType != "" {
		p("// Body sets the request body.\n")
		p("func (b *%s) Body(body %s) *%s {\n", builderType, ep.BodyType, builderType)
		p("\tb.req.body = body\n")
		p("\treturn b\n")
		p("}\n\n")
	}

	p("// Build finalizes the request.\n")
	p("func (b *%s) Build() *%s { return b.req }\n\n", builderType, reqType)

	// Response.
	p("// %s is the response for %s.\n", respType, ep.Method)
	p("type %s struct {\n", respType)
	p("\tcore.APIResp `json:\"-\"`\n")
	p("\tcore.CodeMsg\n")
	if ep.DataType != "" {
		p("\tData %s `json:\"data\"`\n", ep.DataType)
	}
	p("}\n\n")

	// Method.
	if ep.Summary != "" {
		p("// %s: %s\n", ep.Method, ep.Summary)
	} else {
		p("// %s calls %s %s.\n", ep.Method, ep.HTTPVerb, ep.Path)
	}
	p("func (s *Service) %s(ctx context.Context, req *%s, opts ...core.RequestOption) (*%s, error) {\n", ep.Method, reqType, respType)
	p("\tresp := &%s{}\n", respType)
	p("\terr := s.config.Do(ctx, &core.APIReq{\n")
	p("\t\tHTTPMethod:   %q,\n", ep.HTTPVerb)
	p("\t\tPathTemplate: %q,\n", ep.Path)
	p("\t\tPathParams:   req.pathParams,\n")
	p("\t\tQueryParams:  req.queryParams,\n")
	p("\t\tBody:         req.body,\n")
	p("\t}, resp, opts...)\n")
	p("\treturn resp, err\n")
	p("}\n\n")
}

func emitClient(services []*service, modulePath, repoRoot string) {
	var b bytes.Buffer
	p := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	p("// Code generated by internal/gen; DO NOT EDIT.\n\n")
	p("package hduhelp\n\n")
	p("import (\n")
	p("\t%q\n\n", modulePath+"/core")
	for _, svc := range services {
		p("\t%q\n", modulePath+"/service/"+svc.Package)
	}
	p(")\n\n")

	p("// Client is a fully configured hduhelp-neo API client. Each field is a\n")
	p("// namespaced service; call endpoints as client.<Service>.<Method>(ctx, req, opts...).\n")
	p("type Client struct {\n")
	p("\tconfig *core.Config\n\n")
	for _, svc := range services {
		p("\t%s *%s.Service\n", svc.GoName, svc.Package)
	}
	p("}\n\n")

	p("// attachServices wires every generated service to the client config.\n")
	p("func attachServices(c *Client) {\n")
	for _, svc := range services {
		p("\tc.%s = %s.NewService(c.config)\n", svc.GoName, svc.Package)
	}
	p("}\n")

	writeGoFile(filepath.Join(repoRoot, "client.gen.go"), b.Bytes())
}

// ---- helpers ----

func writeGoFile(path string, src []byte) {
	formatted, err := format.Source(src)
	if err != nil {
		// Emit unformatted for debugging, then fail loudly.
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, src, 0o644)
		fail(fmt.Sprintf("format %s: %v", path, err))
	}
	must(os.MkdirAll(filepath.Dir(path), 0o755))
	must(os.WriteFile(path, formatted, 0o644))
}

func refName(ref string) string {
	const prefix = "#/components/schemas/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

func paramKind(isPath bool) string {
	if isPath {
		return "path"
	}
	return "query"
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// exportIdent turns a spec name into an exported Go identifier. Segments split
// on non-alphanumeric boundaries are title-cased; a name with no separators
// keeps its internal capitals (classID -> ClassID).
func exportIdent(name string) string {
	if name == "" {
		return "X"
	}
	hasSep := strings.ContainsAny(name, "_-. /")
	var out strings.Builder
	if hasSep {
		for _, seg := range strings.FieldsFunc(name, func(r rune) bool {
			return !isAlnum(r)
		}) {
			out.WriteString(strings.ToUpper(seg[:1]) + seg[1:])
		}
	} else {
		out.WriteString(strings.ToUpper(name[:1]) + name[1:])
	}
	res := out.String()
	if res == "" {
		return "X"
	}
	if r := rune(res[0]); r >= '0' && r <= '9' {
		return "X" + res
	}
	return res
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "gen: "+msg)
	os.Exit(1)
}
