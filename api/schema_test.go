package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"

	"github.com/pwgith/shadow-mock/api"
	"github.com/pwgith/shadow-mock/internal/admin"
)

// schemaPath is the JSON Schema for mappings that each release publishes (FR-11.11e).
var schemaPath = filepath.Join("..", "schema", "stub-mapping.schema.json")

// generate builds the schema from the OpenAPI description, so the two cannot disagree: the StubMapping component and
// every component it reaches, with the references pointed at the schema's own definitions.
func generate(t *testing.T) []byte {
	t.Helper()
	var doc struct {
		Info       map[string]any `yaml:"info"`
		Components struct {
			Schemas map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(api.OpenAPI, &doc); err != nil {
		t.Fatal(err)
	}
	defs := map[string]any{}
	var collect func(name string)
	collect = func(name string) {
		if _, done := defs[name]; done {
			return
		}
		def, ok := doc.Components.Schemas[name]
		if !ok {
			t.Fatalf("the OpenAPI description has no schema %q", name)
		}
		defs[name] = def
		for _, ref := range refsIn(def) {
			collect(ref)
		}
	}
	collect("StubMapping")
	out := map[string]any{
		"$schema":          "https://json-schema.org/draft/2020-12/schema",
		"title":            "Shadow Mock stub mapping",
		"x-schemaVersion":  doc.Info["x-mapping-schema-version"],
		"$ref":             "#/$defs/StubMapping",
		"$defs":            rewrite(defs),
		"x-generatedFrom":  "api/openapi.yaml: do not edit; run UPDATE_SCHEMA=1 go test ./api",
		"x-wireMockFields": "Unknown fields are kept and returned unchanged (FR-1.4).",
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const refPrefix = "#/components/schemas/"

// refsIn lists the components a schema refers to.
func refsIn(v any) []string {
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if s, ok := e.(string); ok && k == "$ref" && strings.HasPrefix(s, refPrefix) {
				out = append(out, strings.TrimPrefix(s, refPrefix))
				continue
			}
			out = append(out, refsIn(e)...)
		}
	case []any:
		for _, e := range x {
			out = append(out, refsIn(e)...)
		}
	}
	return out
}

// rewrite points component references at the schema's own definitions.
func rewrite(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			if s, ok := e.(string); ok && k == "$ref" && strings.HasPrefix(s, refPrefix) {
				out[k] = "#/$defs/" + strings.TrimPrefix(s, refPrefix)
				continue
			}
			out[k] = rewrite(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = rewrite(e)
		}
		return out
	default:
		return v
	}
}

func TestSchemaFileIsGeneratedFromTheOpenAPIDescription(t *testing.T) {
	want := generate(t)
	if os.Getenv("UPDATE_SCHEMA") != "" {
		if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(schemaPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("%v: run UPDATE_SCHEMA=1 go test ./api to write it", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is out of date with api/openapi.yaml: run UPDATE_SCHEMA=1 go test ./api", schemaPath)
	}
}

func TestSchemaVersionIsTheOneTheEngineReports(t *testing.T) {
	var doc map[string]any
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	version, _ := doc["x-schemaVersion"].(float64)
	if int(version) != admin.MappingSchemaVersion {
		t.Errorf("the schema file is version %d, the engine reports mappingSchema %d", int(version), admin.MappingSchemaVersion)
	}
}

func TestSchemaAcceptsAndRejectsMappings(t *testing.T) {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var schema any
	if err = json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource("stub-mapping.schema.json", schema); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile("stub-mapping.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, doc string
		valid     bool
	}{
		{"a plain mapping", `{"request":{"method":"GET","url":"/a"},"response":{"status":200}}`, true},
		{"a field it does not know", `{"request":{"url":"/a"},"response":{"status":200},"extra":1}`, true},
		{"call responses", `{"request":{"url":"/a"},"response":{"status":200},"callResponses":[{"calls":"1-2","response":{"status":503}}]}`, true},
		{"no response", `{"request":{"url":"/a"}}`, false},
		{"a scenario field", `{"request":{"url":"/a"},"response":{"status":200},"scenarioName":"s"}`, false},
	} {
		var v any
		if err = json.Unmarshal([]byte(tc.doc), &v); err != nil {
			t.Fatal(err)
		}
		if got := s.Validate(v) == nil; got != tc.valid {
			t.Errorf("%s: valid = %v, want %v", tc.name, got, tc.valid)
		}
	}
}
