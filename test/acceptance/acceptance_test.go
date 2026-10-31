package acceptance_test

import (
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/test/acceptance"
)

// phases lists the delivery phases the engine implements (DESIGN.md §15). Managed mode is P5; its scenarios run when
// they are tagged @managed and are excluded by @saas until the harness can run them.
var phases = []string{"@P0", "@P1", "@P2", "@P3", "@P4", "@P5"}

// excluded lists what this harness does not run: SaaS and multi-instance behaviour, the
// performance pipeline, later phases, and scenarios marked @wip with the reason beside them.
var excluded = []string{
	"@saas", "@multi-instance", "@perf", "@wip",
}

// defaultTags selects the standalone scenarios of the implemented phases. Godog tag filters are a comma-separated
// list of alternatives, each a list of "&&" terms.
func defaultTags() string {
	alternatives := make([]string, 0, len(phases))
	for _, p := range phases {
		terms := make([]string, 1, 1+len(excluded))
		terms[0] = p
		for _, x := range excluded {
			terms = append(terms, "~"+x)
		}
		alternatives = append(alternatives, strings.Join(terms, " && "))
	}
	return strings.Join(alternatives, ", ")
}

func TestFeatures(t *testing.T) {
	tags := defaultTags()
	if v := os.Getenv("ACCEPTANCE_TAGS"); v != "" {
		tags = v
	}
	paths := []string{"../../features"}
	if v := os.Getenv("ACCEPTANCE_PATHS"); v != "" {
		paths = strings.Split(v, ",")
	}
	suite := godog.TestSuite{
		ScenarioInitializer: acceptance.InitializeScenario,
		Options: &godog.Options{
			Format:      "progress",
			Paths:       paths,
			Tags:        tags,
			Strict:      true,
			NoColors:    true,
			Concurrency: 1,
			TestingT:    t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acceptance specs failed")
	}
}
