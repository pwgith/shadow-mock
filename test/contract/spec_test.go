package contract_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	validator "github.com/pb33f/libopenapi-validator"

	"github.com/pwgith/shadow-mock/test/contract"
)

func loadSpec(t *testing.T) *contract.Spec {
	t.Helper()
	s, err := contract.Load()
	if err != nil {
		t.Fatalf("load api/openapi.yaml: %v", err)
	}
	return s
}

// TestSpecsAreValidOpenAPI31 validates both spec files against the OpenAPI 3.1 meta-schema.
func TestSpecsAreValidOpenAPI31(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"openapi.yaml", "ci-token.openapi.yaml"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(filepath.Join(contract.RepoRoot(), "api", name))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			doc, err := libopenapi.NewDocument(data)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if v := doc.GetVersion(); !strings.HasPrefix(v, "3.1") {
				t.Fatalf("openapi version = %q, want 3.1.x", v)
			}
			v, errs := validator.NewValidator(doc)
			if len(errs) > 0 {
				t.Fatalf("build validator: %v", errs)
			}
			if ok, verrs := v.ValidateDocument(); !ok {
				for _, e := range verrs {
					t.Errorf("%s: %s", e.Message, e.Reason)
				}
			}
		})
	}
}

// TestEveryComponentSchemaCompiles compiles every schema as JSON Schema 2020-12, which also
// proves that every $ref inside it resolves.
func TestEveryComponentSchemaCompiles(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	schemas, ok := s.At("/components/schemas").(map[string]any)
	if !ok || len(schemas) == 0 {
		t.Fatal("no component schemas")
	}
	for name := range schemas {
		if _, err := s.ComponentSchema(name); err != nil {
			t.Errorf("schema %s: %v", name, err)
		}
	}
}

// TestOperationsAreWellFormed checks rules every operation must follow.
func TestOperationsAreWellFormed(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)

	declaredTags := map[string]bool{}
	tags, ok := s.At("/tags").([]any)
	if !ok {
		t.Fatal("spec declares no top-level tags")
	}
	for _, tag := range tags {
		if m, ok := tag.(map[string]any); ok {
			if name, ok := m["name"].(string); ok {
				declaredTags[name] = true
			}
		}
	}
	seen := map[string]bool{}

	for _, op := range s.Operations {
		if op.ID == "" {
			t.Errorf("%s %s: missing operationId", op.Method, op.Path)
			continue
		}
		if seen[op.ID] {
			t.Errorf("%s %s: duplicate operationId %s", op.Method, op.Path, op.ID)
		}
		seen[op.ID] = true
		checkOperationMetadata(t, op, declaredTags)
		checkOperationResponses(t, s, op)
	}
}

func checkOperationMetadata(t *testing.T, op *contract.Operation, declaredTags map[string]bool) {
	t.Helper()
	allowedXSaaS := map[string]bool{"": true, "global-write-rejected": true, "unavailable": true, "saas-only": true}
	if op.Summary == "" {
		t.Errorf("%s: missing summary", op.ID)
	}
	if len(op.Tags) == 0 {
		t.Errorf("%s: no tags", op.ID)
	}
	for _, tag := range op.Tags {
		if !declaredTags[tag] {
			t.Errorf("%s: tag %q is not declared at the top level", op.ID, tag)
		}
	}
	if !allowedXSaaS[op.XSaaS] {
		t.Errorf("%s: unknown x-saas value %q", op.ID, op.XSaaS)
	}
}

// checkOperationResponses checks the responses an operation must document.
func checkOperationResponses(t *testing.T, s *contract.Spec, op *contract.Operation) {
	t.Helper()
	if len(op.Responses) == 0 {
		t.Errorf("%s: no responses", op.ID)
	}
	required := map[string]string{}
	if strings.HasPrefix(op.Path, "/__admin/") && !op.NoAuth {
		required["401"] = "authenticated admin operations"
	}
	if op.XSaaS == "global-write-rejected" {
		required["403"] = "global-write-rejected operations"
	}
	if op.XSaaS == "unavailable" {
		required["404"] = "unavailable operations"
	}
	if strings.Contains(op.Path, "/sessions/{sessionId}") {
		required["404"] = "session-scoped operations (unknown session)"
	}
	for code, who := range required {
		if _, ok := op.Responses[code]; !ok {
			t.Errorf("%s: %s must document %s", op.ID, who, code)
		}
	}
	for code, ptr := range op.Responses {
		isError := strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5")
		if isError && s.At(ptr+"/content") == nil {
			t.Errorf("%s: error response %s has no body schema", op.ID, code)
		}
	}
}

// TestErrorCodesInSpecsAreDeclared makes sure every error code the feature files and the
// live cases expect exists in the ErrorCode enum.
func TestErrorCodesInSpecsAreDeclared(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	codes := s.ErrorCodes()
	if len(codes) == 0 {
		t.Fatal("ErrorCode enum is empty")
	}

	// OAuth codes belong to the CI token exchange, which has its own contract.
	oauth := map[string]bool{"invalid_client": true, "invalid_grant": true, "unsupported_grant_type": true, "slow_down": true, "access_denied": true}
	used := map[string][]string{}
	re := regexp.MustCompile(`"\$\.error" is "([a-z_]+)"|error "([a-z_]+)"`)
	root := filepath.Join(contract.RepoRoot(), "features")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".feature") {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			code := m[1] + m[2]
			used[code] = append(used[code], relTo(t, root, path))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan features: %v", err)
	}
	for _, c := range contract.Cases {
		if c.WantError != "" {
			used[c.WantError] = append(used[c.WantError], "contract case: "+c.Name)
		}
	}

	var missing []string
	for code, where := range used {
		if !codes[code] && !oauth[code] {
			missing = append(missing, code+" (used in "+strings.Join(where, ", ")+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("error code not in ErrorCode enum: %s", m)
	}
}
