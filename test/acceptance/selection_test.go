package acceptance_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gherkin "github.com/cucumber/gherkin/go/v42"
	messages "github.com/cucumber/messages/go/v34"
)

// A scenario that no tag selects is never run, and nothing says so: the suite passes without it.
// This test lists every scenario the default run would skip without an exclusion tag saying why.
func TestEveryScenarioIsRunOrExcludedOnPurpose(t *testing.T) {
	t.Parallel()
	var skipped []string
	err := filepath.WalkDir("../../features", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".feature") {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		doc, err := gherkin.ParseGherkinDocument(f, (&messages.Incrementing{}).NewId)
		if err != nil {
			return err
		}
		if doc.Feature == nil {
			return nil
		}
		featureTags := tagNames(doc.Feature.Tags)
		for _, child := range doc.Feature.Children {
			scenarios := []*messages.Scenario{}
			if child.Scenario != nil {
				scenarios = append(scenarios, child.Scenario)
			}
			if child.Rule != nil {
				for _, rc := range child.Rule.Children {
					if rc.Scenario != nil {
						scenarios = append(scenarios, rc.Scenario)
					}
				}
			}
			for _, sc := range scenarios {
				tags := slices.Concat(featureTags, tagNames(sc.Tags))
				for _, ex := range sc.Examples {
					tags = slices.Concat(tags, tagNames(ex.Tags))
				}
				if !selected(tags) && !hasAny(tags, excluded) {
					skipped = append(skipped, path+": "+sc.Name)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range skipped {
		t.Errorf("no phase tag selects this scenario and no exclusion tag explains why it is skipped:\n  %s", s)
	}
}

func tagNames(ts []*messages.Tag) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func hasAny(tags, want []string) bool {
	return slices.ContainsFunc(tags, func(t string) bool { return slices.Contains(want, t) })
}

// selected reports whether the default run includes a scenario with these tags.
func selected(tags []string) bool {
	return hasAny(tags, phases) && !hasAny(tags, excluded)
}
