package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// specResource is the URL the spec is registered under in the schema compiler.
const specResource = "https://shadowmock.test/openapi.json"

// Operation is one operation from the spec, with what the checks need.
type Operation struct {
	ID          string
	Method      string // upper case
	Path        string // as written in the spec, e.g. /__admin/mappings/{stubMappingId}
	Summary     string
	Tags        []string
	XSaaS       string
	NoAuth      bool              // security: [{}] on the operation
	Responses   map[string]string // status code or "default" → JSON pointer of the response object
	RequestBody string            // JSON pointer of the request body object, or ""
	pattern     *regexp.Regexp
	params      int
}

// Spec is the loaded contract.
type Spec struct {
	Raw        map[string]any
	Operations []*Operation
	Root       string // repository root

	compiler *jsonschema.Compiler
	mu       sync.Mutex
	compiled map[string]*jsonschema.Schema
}

var (
	loadOnce   sync.Once
	loadedSpec *Spec
	errLoad    error
)

// RepoRoot returns the repository root, found from this file's location.
func RepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Load parses api/openapi.yaml once and returns it.
func Load() (*Spec, error) {
	loadOnce.Do(func() {
		root := RepoRoot()
		loadedSpec, errLoad = LoadFile(root, filepath.Join(root, "api", "openapi.yaml"))
	})
	return loadedSpec, errLoad
}

// LoadFile parses an OpenAPI YAML file.
func LoadFile(root, path string) (*Spec, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: the path is a spec file in this repository, chosen by the test
	if err != nil {
		return nil, fmt.Errorf("read spec: %w", err)
	}
	raw, err := YAMLToJSONValue(data)
	if err != nil {
		return nil, fmt.Errorf("parse spec %s: %w", path, err)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("spec root is not an object")
	}

	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource(specResource, m); err != nil {
		return nil, fmt.Errorf("register spec with the schema compiler: %w", err)
	}

	s := &Spec{Raw: m, Root: root, compiler: c, compiled: map[string]*jsonschema.Schema{}}
	if err := s.indexOperations(); err != nil {
		return nil, err
	}
	return s, nil
}

// YAMLToJSONValue decodes YAML into values that encoding/json would produce
// (map[string]any, []any, float64, string, bool, nil), via a JSON round trip.
func YAMLToJSONValue(data []byte) (any, error) {
	var v any
	if err := yaml.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	j, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("yaml to json: %w", err)
	}
	return DecodeJSON(j)
}

// DecodeJSON decodes JSON into a value the schema validator accepts.
func DecodeJSON(data []byte) (any, error) {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("json: %w", err)
	}
	return v, nil
}

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

func (s *Spec) indexOperations() error {
	paths := asMap(s.Raw["paths"])
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, p := range keys {
		item := asMap(paths[p])
		for _, method := range httpMethods {
			if opRaw := asMap(item[method]); opRaw != nil {
				s.Operations = append(s.Operations, s.newOperation(p, method, opRaw))
			}
		}
	}
	if len(s.Operations) == 0 {
		return errors.New("spec has no operations")
	}
	return nil
}

func (s *Spec) newOperation(p, method string, opRaw map[string]any) *Operation {
	op := &Operation{
		ID:        asString(opRaw["operationId"]),
		Method:    strings.ToUpper(method),
		Path:      p,
		Summary:   asString(opRaw["summary"]),
		XSaaS:     asString(opRaw["x-saas"]),
		Responses: map[string]string{},
	}
	for _, t := range asSlice(opRaw["tags"]) {
		if ts := asString(t); ts != "" {
			op.Tags = append(op.Tags, ts)
		}
	}
	if sec := asSlice(opRaw["security"]); len(sec) == 1 {
		if m, ok := sec[0].(map[string]any); ok && len(m) == 0 {
			op.NoAuth = true
		}
	}
	opPtr := "/paths/" + escapePointer(p) + "/" + method
	for code := range asMap(opRaw["responses"]) {
		op.Responses[code] = s.resolveRef(opPtr + "/responses/" + escapePointer(code))
	}
	if _, ok := opRaw["requestBody"]; ok {
		op.RequestBody = s.resolveRef(opPtr + "/requestBody")
	}
	op.pattern, op.params = pathPattern(p)
	return op
}

// resolveRef follows a local $ref at ptr, if any, and returns the pointer of the target.
func (s *Spec) resolveRef(ptr string) string {
	for range 10 {
		node, ok := s.At(ptr).(map[string]any)
		if !ok {
			return ptr
		}
		ref, ok := node["$ref"].(string)
		if !ok || !strings.HasPrefix(ref, "#") {
			return ptr
		}
		ptr = strings.TrimPrefix(ref, "#")
	}
	return ptr
}

// At returns the raw value at a JSON pointer, or nil.
func (s *Spec) At(ptr string) any {
	var cur any = s.Raw
	for _, part := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
		if part == "" {
			continue
		}
		part = strings.NewReplacer("~1", "/", "~0", "~").Replace(part)
		switch n := cur.(type) {
		case map[string]any:
			cur = n[part]
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(n) {
				return nil
			}
			cur = n[i]
		default:
			return nil
		}
	}
	return cur
}

// Schema compiles (once) and returns the schema at a JSON pointer in the spec.
func (s *Spec) Schema(ptr string) (*jsonschema.Schema, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sch, ok := s.compiled[ptr]; ok {
		return sch, nil
	}
	sch, err := s.compiler.Compile(specResource + "#" + ptr)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", ptr, err)
	}
	s.compiled[ptr] = sch
	return sch, nil
}

// ComponentSchema returns a schema from components/schemas by name.
func (s *Spec) ComponentSchema(name string) (*jsonschema.Schema, error) {
	return s.Schema("/components/schemas/" + escapePointer(name))
}

// ValidateComponent validates a JSON document against a named component schema.
func (s *Spec) ValidateComponent(name string, doc []byte) error {
	sch, err := s.ComponentSchema(name)
	if err != nil {
		return err
	}
	v, err := DecodeJSON(doc)
	if err != nil {
		return err
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// FindOperation returns the operation for a method and a path relative to the base URL.
// Literal paths win over templated ones, as OpenAPI requires.
func (s *Spec) FindOperation(method, path string) *Operation {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	var best *Operation
	for _, op := range s.Operations {
		if !strings.EqualFold(op.Method, method) || !op.pattern.MatchString(path) {
			continue
		}
		if best == nil || op.params < best.params {
			best = op
		}
	}
	return best
}

// OperationByID returns an operation by its operationId.
func (s *Spec) OperationByID(id string) *Operation {
	for _, op := range s.Operations {
		if op.ID == id {
			return op
		}
	}
	return nil
}

// ErrorCodes returns the ErrorCode enum.
func (s *Spec) ErrorCodes() map[string]bool {
	out := map[string]bool{}
	for _, v := range asSlice(s.At("/components/schemas/ErrorCode/enum")) {
		if code, ok := v.(string); ok {
			out[code] = true
		}
	}
	return out
}

// Exchange is one request and response to check against the contract.
type Exchange struct {
	Method       string
	Path         string // relative to the base URL, may include a query
	RequestType  string
	RequestBody  []byte
	Status       int
	ResponseType string
	ResponseBody []byte
}

// Check validates an exchange and returns every contract violation found.
func (s *Spec) Check(ex Exchange) []string {
	op := s.FindOperation(ex.Method, ex.Path)
	if op == nil {
		return []string{fmt.Sprintf("%s %s: no operation in the contract", ex.Method, ex.Path)}
	}
	found := append(s.checkRequest(op, ex), s.checkResponse(op, ex)...)
	out := make([]string, 0, len(found))
	for _, v := range found {
		out = append(out, fmt.Sprintf("%s (%s %s): %s", op.ID, ex.Method, ex.Path, v))
	}
	return out
}

func (s *Spec) checkRequest(op *Operation, ex Exchange) []string {
	if op.RequestBody == "" {
		return nil
	}
	required, ok := s.At(op.RequestBody + "/required").(bool)
	body := bytes.TrimSpace(ex.RequestBody)
	switch {
	case len(body) == 0 && ok && required:
		return []string{"request body is required"}
	case len(body) > 0 && isJSON(ex.RequestType):
		if ptr := s.mediaSchema(op.RequestBody, "application/json"); ptr != "" {
			if err := s.validateAt(ptr, body); err != nil {
				return []string{fmt.Sprintf("request body: %v", err)}
			}
		}
	}
	return nil
}

func (s *Spec) checkResponse(op *Operation, ex Exchange) []string {
	respPtr, ok := op.Responses[strconv.Itoa(ex.Status)]
	if !ok {
		respPtr, ok = op.Responses["default"]
	}
	if !ok {
		return []string{fmt.Sprintf("status %d is not documented", ex.Status)}
	}
	body := bytes.TrimSpace(ex.ResponseBody)
	if len(body) == 0 {
		return nil
	}
	content := asMap(s.At(respPtr + "/content"))
	if len(content) == 0 {
		return []string{fmt.Sprintf("status %d documents no body, but %d bytes were returned", ex.Status, len(body))}
	}
	mt := mediaType(ex.ResponseType)
	if _, declared := content[mt]; !declared {
		return []string{fmt.Sprintf("response content type %q is not documented for status %d", ex.ResponseType, ex.Status)}
	}
	if !isJSON(mt) {
		return nil
	}
	if ptr := s.mediaSchema(respPtr, mt); ptr != "" {
		if err := s.validateAt(ptr, body); err != nil {
			return []string{fmt.Sprintf("response body for status %d: %v", ex.Status, err)}
		}
	}
	return nil
}

func (s *Spec) mediaSchema(objPtr, mt string) string {
	ptr := objPtr + "/content/" + escapePointer(mt) + "/schema"
	if s.At(ptr) == nil {
		return ""
	}
	return ptr
}

func (s *Spec) validateAt(ptr string, body []byte) error {
	sch, err := s.Schema(ptr)
	if err != nil {
		return err
	}
	v, err := DecodeJSON(body)
	if err != nil {
		return err
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("does not match the schema: %w", err)
	}
	return nil
}

func pathPattern(p string) (*regexp.Regexp, int) {
	var b strings.Builder
	b.WriteString("^")
	params := 0
	for p != "" {
		i := strings.IndexByte(p, '{')
		if i < 0 {
			b.WriteString(regexp.QuoteMeta(p))
			break
		}
		b.WriteString(regexp.QuoteMeta(p[:i]))
		j := strings.IndexByte(p[i:], '}')
		if j < 0 {
			b.WriteString(regexp.QuoteMeta(p[i:]))
			break
		}
		b.WriteString("[^/]+")
		params++
		p = p[i+j+1:]
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String()), params
}

func escapePointer(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

func mediaType(ct string) string {
	if ct == "" {
		return ""
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return strings.TrimSpace(strings.SplitN(ct, ";", 2)[0])
	}
	return mt
}

func isJSON(ct string) bool {
	mt := mediaType(ct)
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
