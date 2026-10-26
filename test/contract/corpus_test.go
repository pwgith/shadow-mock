package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/test/contract"
)

func corpusPath(parts ...string) string {
	return filepath.Join(append([]string{contract.RepoRoot(), "corpus"}, parts...)...)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func relTo(t *testing.T, base, path string) string {
	t.Helper()
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// negativeCases is the shape of the corpus/shadow-mock/negative/**/invalid-*.json files.
type negativeCases struct {
	Cases []struct {
		Why      string `json:"why"`
		Error    string `json:"error"`
		Endpoint string `json:"endpoint"`
		Mapping  any    `json:"mapping"`
		Payload  any    `json:"payload"`
	} `json:"cases"`
}

func readNegative(t *testing.T, path string) negativeCases {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var n negativeCases
	if err := json.Unmarshal(data, &n); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(n.Cases) == 0 {
		t.Fatalf("%s has no cases", path)
	}
	return n
}

// mappingsIn returns every mapping in a mapping file (single or {"mappings": [...]}).
func mappingsIn(t *testing.T, path string) []any {
	t.Helper()
	doc := readJSON(t, path)
	if list, ok := doc["mappings"].([]any); ok {
		return list
	}
	return []any{doc}
}

// TestCorpusMappingsConformToStubMapping validates every mapping in both corpus trees.
func TestCorpusMappingsConformToStubMapping(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	dirs := []string{
		corpusPath("wiremock", "mappings"),
		corpusPath("wiremock", "extensions"),
		corpusPath("shadow-mock", "mappings"),
		corpusPath("shadow-mock", "mapping-sets"),
	}
	count := 0
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
				return err
			}
			for i, m := range mappingsIn(t, path) {
				count++
				if err := s.ValidateComponent("StubMapping", mustJSON(t, m)); err != nil {
					t.Errorf("%s mapping %d: %v", relTo(t, corpusPath(), path), i, err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if count < 100 {
		t.Fatalf("validated only %d mappings; is the corpus missing?", count)
	}
}

// TestCorpusPayloadsConformToTheirSchemas validates every admin and session payload in the
// corpus against the schema of the operation it is sent to.
func TestCorpusPayloadsConformToTheirSchemas(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)
	cases := []struct {
		file   string
		schema string
	}{
		{"wiremock/admin/mappings/import.json", "ImportRequest"},
		{"wiremock/admin/mappings/find-by-metadata.json", "ContentPattern"},
		{"wiremock/admin/mappings/remove-by-metadata.json", "ContentPattern"},
		{"wiremock/admin/verification/count-posts-with-body.json", "RequestPattern"},
		{"wiremock/admin/verification/find-any-method.json", "RequestPattern"},
		{"wiremock/admin/near-misses/request.json", "SampleRequest"},
		{"wiremock/admin/near-misses/request-pattern.json", "RequestPattern"},
		{"wiremock/admin/recording/record-spec.json", "RecordSpec"},
		{"wiremock/admin/recording/snapshot-spec.json", "SnapshotSpec"},
		{"wiremock/admin/settings/global-settings.json", "GlobalSettings"},
		{"shadow-mock/sessions/key-based/01-minimal.json", "CreateKeyBasedSessionRequest"},
		{"shadow-mock/sessions/key-based/02-checkout-declined.json", "CreateKeyBasedSessionRequest"},
		{"shadow-mock/sessions/key-based/03-override-by-id.json", "CreateKeyBasedSessionRequest"},
		{"shadow-mock/sessions/key-based/04-masks.json", "CreateKeyBasedSessionRequest"},
		{"shadow-mock/sessions/key-based/05-seed-from-mapping-set.json", "CreateKeyBasedSessionRequest"},
		{"shadow-mock/sessions/key-based/06-session-mapping-with-call-counts.json", "StubMapping"},
		{"shadow-mock/sessions/key-based/patch-add-tokens.json", "SessionPatch"},
		{"shadow-mock/sessions/key-based/patch-add-masks-and-extend.json", "SessionPatch"},
		{"shadow-mock/sessions/url-based/01-minimal.json", "CreateURLBasedSessionRequest"},
		{"shadow-mock/sessions/url-based/02-claims-api-ci.json", "CreateURLBasedSessionRequest"},
		{"shadow-mock/sessions/url-based/03-nested-key-based.json", "CreateKeyBasedSessionRequest"},
		{"shadow-mock/sessions/counters/set-counter.json", "CounterUpdate"},
		{"shadow-mock/sessions/recording/record-into-session.json", "RecordSpec"},
		{"shadow-mock/sessions/runtime/dry-run.json", "DryRunRequest"},
		{"shadow-mock/sessions/runtime/validate-mapping.json", "StubMapping"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(corpusPath(filepath.FromSlash(tc.file)))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if err := s.ValidateComponent(tc.schema, data); err != nil {
				t.Errorf("%v", err)
			}
		})
	}
}

// TestNegativeCorpusIsRejectedByTheContract checks that inputs which must be rejected are
// already invalid at the schema level, wherever the schema can express the rule. Rules the
// schema cannot express (regex validity, uniqueness, unknown matchers) are enforced by the
// engine and covered by the live and acceptance suites.
func TestNegativeCorpusIsRejectedByTheContract(t *testing.T) {
	t.Parallel()
	s := loadSpec(t)

	expectInvalid := func(t *testing.T, schema string, v any, why string) {
		t.Helper()
		if err := s.ValidateComponent(schema, mustJSON(t, v)); err == nil {
			t.Errorf("%s: %s should be rejected by %s, but it validates", why, string(mustJSON(t, v)), schema)
		}
	}

	t.Run("scenario fields", func(t *testing.T) {
		t.Parallel()
		for _, f := range []string{"all-scenario-fields.json", "scenario-name-only.json"} {
			doc := readJSON(t, corpusPath("shadow-mock", "negative", "scenarios", f))
			expectInvalid(t, "StubMapping", doc["mapping"], f)
		}
		doc := readJSON(t, corpusPath("shadow-mock", "negative", "scenarios", "import-with-one-scenario-mapping.json"))
		expectInvalid(t, "ImportRequest", doc["import"], "import with a scenario mapping")
	})

	t.Run("invalid call rules", func(t *testing.T) {
		t.Parallel()
		// A pattern cannot compare the two ends of a range, so "4-2" is an engine-level rule.
		engineOnly := map[string]bool{"range runs backwards": true}
		for _, c := range readNegative(t, corpusPath("shadow-mock", "negative", "call-responses", "invalid-rules.json")).Cases {
			if !engineOnly[c.Why] {
				expectInvalid(t, "StubMapping", c.Mapping, c.Why)
			}
		}
	})

	t.Run("invalid mappings detectable by schema", func(t *testing.T) {
		t.Parallel()
		schemaDetectable := map[string]bool{"status out of range": true, "unknown fault": true, "request is required": true}
		for _, c := range readNegative(t, corpusPath("shadow-mock", "negative", "mappings", "invalid-mappings.json")).Cases {
			if schemaDetectable[c.Why] {
				expectInvalid(t, "StubMapping", c.Mapping, c.Why)
			}
		}
	})

	t.Run("invalid session payloads detectable by schema", func(t *testing.T) {
		t.Parallel()
		expectInvalidSessionPayloads(t, expectInvalid)
	})
}

func expectInvalidSessionPayloads(t *testing.T, expectInvalid func(*testing.T, string, any, string)) {
	t.Helper()
	schemaFor := map[string]string{
		"/__admin/sessions/key-based": "CreateKeyBasedSessionRequest",
		"/__admin/sessions/url-based": "CreateURLBasedSessionRequest",
	}
	engineOnly := map[string]bool{"token_in_use": true, "parent_not_found": true}
	for _, c := range readNegative(t, corpusPath("shadow-mock", "negative", "sessions", "invalid-session-payloads.json")).Cases {
		if engineOnly[c.Error] {
			continue
		}
		schema, ok := schemaFor[c.Endpoint]
		if !ok {
			t.Errorf("%s: no schema for endpoint %q", c.Error, c.Endpoint)
			continue
		}
		expectInvalid(t, schema, c.Payload, c.Error)
	}
}
