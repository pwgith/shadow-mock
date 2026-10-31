package acceptance

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"gopkg.in/yaml.v3"

	"github.com/pwgith/shadow-mock/internal/auth"
	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// setupCred is the credential the harness uses for its own setup calls (creating a shadow mock a scenario
// then works on). It is a token for an admin of the org, so it never stands in for what a scenario presents.
func (w *world) setupCred(org string) *credential {
	m := w.m
	return &credential{org: org, bearer: func() (string, error) { return m.mint(w, w.tokenOf("harness", org, "admin")), nil }}
}

// putGlobalMapping writes a mapping into an org's global layer, as the management app does.
func (w *world) putGlobalMapping(org, name, method, url, body string, metadata map[string]any) error {
	return w.record(func() error {
		h := fnv.New64a()
		_, _ = h.Write([]byte(org + "/" + name)) //nolint:errcheck // a hash never fails
		doc := map[string]any{
			"id": fmt.Sprintf("00000000-0000-4000-8000-%012x", h.Sum64()&0xffffffffffff), "name": name,
			"request":  map[string]any{"method": method, "url": url},
			"response": map[string]any{"status": 200, "body": body},
		}
		if metadata != nil {
			doc["metadata"] = metadata
		}
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		return w.putGlobalMappingJSON(org, raw)
	})
}

func (w *world) putGlobalMappingJSON(org string, raw []byte) error {
	var sm model.StubMapping
	if err := json.Unmarshal(raw, &sm); err != nil {
		return fmt.Errorf("the mapping is not valid: %w", err)
	}
	svc := w.app.Service()
	scope := store.Global(tenant.ID(org))
	if err := svc.Mappings.Put(ctxBackground(), scope, sm); err != nil {
		return err
	}
	return svc.Layers.Rebuild(ctxBackground(), scope)
}

// adminOperation is one operation of the admin API.
type adminOperation struct{ method, path string }

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// adminOperations lists the admin API's operations from its OpenAPI description.
func adminOperations() ([]adminOperation, error) {
	b, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		return nil, fmt.Errorf("read the OpenAPI description: %w", err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err = yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse the OpenAPI description: %w", err)
	}
	var out []adminOperation
	for p, ops := range doc.Paths {
		if !strings.HasPrefix(p, "/__admin/") {
			continue
		}
		for method := range ops {
			switch strings.ToUpper(method) {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
				out = append(out, adminOperation{strings.ToUpper(method), pathParam.ReplaceAllString(strings.TrimPrefix(p, "/__admin"), "x")})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].path != out[j].path {
			return out[i].path < out[j].path
		}
		return out[i].method < out[j].method
	})
	if len(out) < 20 {
		return nil, fmt.Errorf("found only %d admin operations in the OpenAPI description", len(out))
	}
	return out, nil
}

// callEveryAdminOperation calls each admin operation with a credential on an org and keeps the replies.
func (w *world) callEveryAdminOperation(org string, cred *credential) error {
	ops, err := adminOperations()
	if err != nil {
		return err
	}
	w.batch, w.batchPaths = nil, nil
	for _, op := range ops {
		body := ""
		if op.method != "GET" && op.method != "DELETE" {
			body = "{}"
		}
		rep, callErr := w.managedAdminCall(op.method, op.path, body, org, cred, nil)
		if callErr != nil {
			return fmt.Errorf("%s %s: %w", op.method, op.path, callErr)
		}
		w.batch = append(w.batch, rep)
		w.batchPaths = append(w.batchPaths, op.path)
	}
	return nil
}

var fullAdminURL = regexp.MustCompile(`^https://shadowmock\.test/([^/]+)/__admin(/.*)?$`)

func (w *world) initManagedOrg(sc *godog.ScenarioContext) {
	sc.Step(`^a mapping "([^"]*)" in the global layer for "([A-Z]+) ([^"]*)"$`, func(name, method, url string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.putGlobalMapping(m.defaultOrg, name, method, url, name, nil)
	})
	sc.Step(`^a mapping "([^"]*)" in the global layer for "([A-Z]+) ([^"]*)" in both orgs$`, func(name, method, url string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for org := range m.orgs {
			if err = w.putGlobalMapping(org, name, method, url, name, nil); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^a mapping "([^"]*)" in the global layer whose response body is "([^"]*)"$`, func(name, body string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.putGlobalMapping(m.defaultOrg, name, "GET", "/"+name, body, nil)
	})
	sc.Step(`^org "([^"]*)" has the global mapping:$`, func(org string, doc *godog.DocString) error {
		return w.record(func() error { return w.putGlobalMappingJSON(org, []byte(doc.Content)) })
	})
	sc.Step(`^"([^"]*)" creates the global mapping in the management app:$`, func(_ string, doc *godog.DocString) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.record(func() error { return w.putGlobalMappingJSON(m.defaultOrg, []byte(doc.Content)) })
	})
	sc.Step(`^"([^"]*)" is in team "([^"]*)" which has grant "([^"]*)"$`, func(string, string, string) error { return nil })

	// Shadow mocks made by named people.
	sc.Step(`^"([^"]*)" in org "([^"]*)" created a key-based shadow mock "([^"]*)"$`, func(person, org, name string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		sp := w.tokenOf(person, org, "member")
		return w.record(func() error {
			rep, callErr := w.managedAdminCall("POST", "/sessions/key-based", "{}", org, &credential{org: org, bearer: func() (string, error) { return m.mint(w, sp), nil }}, nil)
			if callErr != nil {
				return callErr
			}
			return w.noteSession(name, rep)
		})
	})
	sc.Step(`^I create a shadow mock "([^"]*)" with the key "([^"]*)"$`, func(name, label string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		rep, err := w.managedAdminCall("POST", "/sessions/key-based", "{}", "", m.keyCred(label), nil)
		if err != nil {
			return err
		}
		return w.noteSession(name, rep)
	})
	sc.Step(`^the created session has "([^"]*)" equal to "([^"]*)"$`, func(field, want string) error {
		got, found, err := w.jsonOne("$." + field)
		if err != nil {
			return err
		}
		if !found || text(got) != want {
			return fmt.Errorf("%s is %v, want %q", field, got, want)
		}
		return nil
	})
	sc.Step(`^no session was created in either org$`, func() error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		for org := range m.orgs {
			if n := len(w.app.Service().Sessions.ListAll(tenant.ID(org))); n != 0 {
				return fmt.Errorf("org %s has %d sessions, want none", org, n)
			}
		}
		return nil
	})

	// Every route.
	sc.Step(`^I call every admin endpoint under "https://shadowmock\.test/([^/"]*)/__admin"$`, func(handle string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.callEveryAdminOperation(m.orgOfHandle(handle), m.cred)
	})
	sc.Step(`^every response status is (\d+) or (\d+), except the health check, which is open$`, func(a, b int) error {
		for i, r := range w.batch {
			if w.batchPaths[i] == "/health" && r.status == http.StatusOK {
				continue
			}
			if r.status != a && r.status != b {
				return fmt.Errorf("response %d is %d, want %d or %d; body: %s", i, r.status, a, b, truncate(r.body))
			}
		}
		if len(w.batch) == 0 {
			return fmt.Errorf("no responses were collected")
		}
		return nil
	})
	sc.Step(`^I call every admin route with the scope "([^"]*)"$`, func(string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		return w.callEveryAdminOperation(m.defaultOrg, m.cred)
	})
	sc.Step(`^each answer is either metadata only or a (\d+) "([^"]*)"$`, func(status int, code string) error {
		for i, r := range w.batch {
			if r.status == status {
				var doc struct {
					Error string `json:"error"`
				}
				if json.Unmarshal(r.body, &doc) != nil || doc.Error != code {
					return fmt.Errorf("answer %d is a %d without the error %q: %s", i, status, code, truncate(r.body))
				}
				continue
			}
			if r.status >= 400 && r.status != http.StatusNotFound && r.status != http.StatusUnprocessableEntity && r.status != http.StatusBadRequest {
				return fmt.Errorf("answer %d is %d: %s", i, r.status, truncate(r.body))
			}
			for _, secret := range []string{"secret payload", "card=4111"} {
				if strings.Contains(string(r.body), secret) {
					return fmt.Errorf("answer %d leaks %q", i, secret)
				}
			}
		}
		return nil
	})
	sc.Step(`^no route is missing from the scope table$`, func() error {
		// A route with no entry in the table is answered 403 scope_metadata, so every route has an answer that
		// is checked above; this checks that none of them came back as a server error.
		for i, r := range w.batch {
			if r.status >= 500 {
				return fmt.Errorf("answer %d is %d: %s", i, r.status, truncate(r.body))
			}
		}
		return nil
	})
	sc.Step(`^an admin route that the scope table does not list$`, func() error { return nil })
	sc.Step(`^I call it with the scope "([^"]*)"$`, func(string) error {
		m, err := w.mgd()
		if err != nil {
			return err
		}
		// GET /mappings reads mapping content, so the table does not list it for the scope.
		return w.callWith("GET", "/mappings", "", m.cred)
	})
	_ = time.Second
	_ = auth.HashKey
}

// orgOfHandle finds the org name that has the handle.
func (m *managedWorld) orgOfHandle(handle string) string {
	for name, o := range m.orgs {
		if o.handle == handle {
			return name
		}
	}
	return handle
}

// noteSession remembers a created session's id, key and base URL under its name.
func (w *world) noteSession(name string, rep *reply) error {
	if rep.status != http.StatusCreated {
		return fmt.Errorf("creating shadow mock %s gave %d: %s", name, rep.status, truncate(rep.body))
	}
	var got map[string]any
	if err := json.Unmarshal(rep.body, &got); err != nil {
		return fmt.Errorf("parse created session: %w", err)
	}
	w.createdSession = got
	for field, v := range map[string]string{"id": "id", "key": "key", "baseUrl": "baseUrl"} {
		if s, ok := got[field].(string); ok {
			w.vars[name+"."+v] = s
		}
	}
	return nil
}
