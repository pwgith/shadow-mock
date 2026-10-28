package acceptance

import "github.com/cucumber/godog"

// initAuth registers the steps that choose which admin token the scenario's calls carry.
func (w *world) initAuth(sc *godog.ScenarioContext) {
	sc.Step(`^I use the admin token "([^"]*)"$`, func(token string) error {
		w.adminToken = token
		return nil
	})
	sc.Step(`^I use no admin token$`, func() error {
		w.adminToken = ""
		return nil
	})
}
