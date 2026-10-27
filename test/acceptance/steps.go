package acceptance

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"
)

// InitializeScenario gives every scenario its own engine, root directory and clock, and
// registers all step definitions on it.
func InitializeScenario(sc *godog.ScenarioContext) {
	var w *world
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		var err error
		w, err = newWorld()
		if err != nil {
			return ctx, err
		}
		w.initEnv(sc)
		w.initData(sc)
		w.initHTTP(sc)
		w.initAssert(sc)
		w.initWebhook(sc)
		w.initConcurrent(sc)
		w.initUpstream(sc)
		w.initMatchers(sc)
		w.initBehaviour(sc)
		w.initMetrics(sc)
		w.initRecordingAsserts(sc)
		return ctx, nil
	})
	sc.After(func(ctx context.Context, s *godog.Scenario, _ error) (context.Context, error) {
		if w == nil {
			return ctx, nil
		}
		if err := w.close(); err != nil {
			return ctx, fmt.Errorf("end of scenario %q: %w", s.Name, err)
		}
		return ctx, nil
	})
}
