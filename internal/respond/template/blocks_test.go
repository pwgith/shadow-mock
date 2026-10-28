package template_test

import (
	"encoding/json"
	"os"
	"testing"
)

// goldenTemplate is a template and what a real WireMock 3.13.2 rendered for it
// (testdata/wiremock_templates.json). Skip names a deliberate difference.
type goldenTemplate struct {
	Name string `json:"name"`
	Tpl  string `json:"tpl"`
	Want string `json:"want"`
	Skip string `json:"skip"`
}

func TestTemplatesRenderAsWireMockDoes(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/wiremock_templates.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenTemplate
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tt := range cases {
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()
			if tt.Skip != "" {
				t.Skip(tt.Skip)
			}
			got, err := engine().Render(tt.Tpl, map[string]any{})
			if err != nil {
				t.Fatalf("Render(%q): %v", tt.Tpl, err)
			}
			if got != tt.Want {
				t.Errorf("Render(%q)\n got %q\nwant %q", tt.Tpl, got, tt.Want)
			}
		})
	}
}

func TestBlockHelpersReportBadInput(t *testing.T) {
	t.Parallel()
	for _, tpl := range []string{
		"{{#parseJson 'x'}}not json{{/parseJson}}",
		"{{#formatJson}}{oops{{/formatJson}}",
		"{{#formatXml}}<a>{{/formatXml}}",
	} {
		if _, err := engine().Render(tpl, map[string]any{}); err == nil {
			t.Errorf("Render(%q) should fail", tpl)
		}
	}
}
