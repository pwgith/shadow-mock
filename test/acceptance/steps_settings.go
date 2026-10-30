package acceptance

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"
)

// initSettings registers the steps that read the settings a start-up resolved.
func (w *world) initSettings(sc *godog.ScenarioContext) {
	sc.Step(`^the setting "([^"]*)" is "([^"]*)"$`, func(key, want string) error {
		if w.cfgErr != nil {
			return fmt.Errorf("start-up failed: %w", w.cfgErr)
		}
		got, err := w.setting(key)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s = %q, want %q", key, got, want)
		}
		return nil
	})
}

// setting reads a dotted key from the resolved configuration, as the YAML file would write it. A list is
// its items joined by commas.
func (w *world) setting(key string) (string, error) {
	raw, err := yaml.Marshal(w.cfgOut)
	if err != nil {
		return "", fmt.Errorf("render the settings: %w", err)
	}
	var tree any
	if err = yaml.Unmarshal(raw, &tree); err != nil {
		return "", fmt.Errorf("read the settings back: %w", err)
	}
	for _, part := range strings.Split(key, ".") {
		m, ok := tree.(map[string]any)
		if !ok {
			return "", fmt.Errorf("no setting %q: %q is not a section", key, part)
		}
		if tree, ok = m[part]; !ok {
			return "", fmt.Errorf("no setting %q", key)
		}
	}
	if items, ok := tree.([]any); ok {
		parts := make([]string, len(items))
		for i, it := range items {
			parts[i] = fmt.Sprint(it)
		}
		return strings.Join(parts, ","), nil
	}
	return fmt.Sprint(tree), nil
}
