package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/cli"
	"github.com/pwgith/shadow-mock/internal/console"
	"github.com/pwgith/shadow-mock/test/contract"
)

// The harness has no browser, so the console is played as its script plays it: by making the calls listed in the
// console's own calls.json, in the order app.js makes them, and checking the answers hold what the script shows.
// internal/console tests that the script makes only those calls and builds the page from text only.

// consoleWorld is the state of the console and command-line steps.
type consoleWorld struct {
	page       *reply
	entry      map[string]any   // the journal entry the console selected
	nearMisses []map[string]any // the closest mappings it showed for that entry
	dryRun     map[string]any
	deleted    string
	shown      []string // ids of the shadow mocks the console listed last
	refused    map[string]int

	cliCalls []cliCall
	cliOut   string
	cliErr   error
	cliKey   string
}

// cliCall is a call the command line made, and the answer it got.
type cliCall struct {
	method, uri, auth, body string
	status                  int
	answer                  string
}

// toolKey is the shape of a Tool app key (FR-11.3).
var toolKey = "smt_" + strings.Repeat("A", 16) + "_" + strings.Repeat("B", 52)

var (
	adminBaseMeta = regexp.MustCompile(`<meta name="admin-base" content="([^"]*)">`)
	pageRefs      = regexp.MustCompile(`(?:src|href)="([^"]*)"`)
	inlineScript  = regexp.MustCompile(`<script>|\son[a-z]+=`)
)

func (w *world) initConsole(sc *godog.ScenarioContext) {
	w.initConsoleSetup(sc)
	w.initConsoleJournal(sc)
	w.initConsoleSessions(sc)
	w.initConsoleViews(sc)
	w.initConsoleAccess(sc)
	w.initCLI(sc)
}

func (w *world) initConsoleSetup(sc *godog.ScenarioContext) {
	sc.Step(`^Shadow Mock is running with the console enabled$`, func() error {
		return w.reconfigure("console: { enabled: true }")
	})
	sc.Step(`^Shadow Mock is running as a central server with admin tokens and the console enabled$`, func() error {
		return w.reconfigure(`
console: { enabled: true }
auth:
  driver: token
  tokens:
    - { name: ops, role: admin, token: "admin-token-0123456789" }
`)
	})
	sc.Step(`^the response is a static page that calls only "([^"]*)"$`, func(admin string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		page := string(r.body)
		if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			return fmt.Errorf("Content-Type = %q, want HTML", ct)
		}
		m := adminBaseMeta.FindStringSubmatch(page)
		if m == nil || m[1] != admin {
			return fmt.Errorf("the page names the admin API as %v, want %q", m, admin)
		}
		for _, ref := range pageRefs.FindAllStringSubmatch(page, -1) {
			if strings.Contains(ref[1], "://") || strings.HasPrefix(ref[1], "/") {
				return fmt.Errorf("the page loads %q, which is not a file next to it", ref[1])
			}
		}
		if inlineScript.MatchString(page) {
			return errors.New("the page has inline script")
		}
		if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
			return fmt.Errorf("Content-Security-Policy = %q does not confine the page to its own origin", csp)
		}
		return nil
	})
}

// consoleCalls reads the calls the console makes, from the copy embedded in the binary.
func consoleCalls() (map[string][]string, error) {
	raw, err := console.Calls()
	if err != nil {
		return nil, fmt.Errorf("read the console's calls: %w", err)
	}
	var calls map[string][]string
	if err = json.Unmarshal(raw, &calls); err != nil {
		return nil, fmt.Errorf("parse the console's calls: %w", err)
	}
	return calls, nil
}

// consoleCall makes one of the console's calls, as app.js does: "{id}" in the path is the id, and a body is JSON.
func (w *world) consoleCall(name, id string, body any) (*reply, error) {
	return w.consoleCallFor(name, id, "00000000-0000-0000-0000-000000000000", body)
}

// consoleCallFor is consoleCall for a call that also names a stub mapping: "{stub}" in the path is the stub's id.
func (w *world) consoleCallFor(name, id, stub string, body any) (*reply, error) {
	calls, err := consoleCalls()
	if err != nil {
		return nil, err
	}
	spec, ok := calls[name]
	if !ok || len(spec) != 2 {
		return nil, fmt.Errorf("the console has no call named %q", name)
	}
	payload := ""
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode the body of %s: %w", name, err)
		}
		payload = string(b)
	}
	path := strings.ReplaceAll(spec[1], "{id}", url.PathEscape(w.sub(id)))
	path = strings.ReplaceAll(path, "{stub}", url.PathEscape(w.sub(stub)))
	return w.adminCall(spec[0], path, payload, nil)
}

// consoleObject makes a call the console expects to succeed and reads the JSON object it answers.
func (w *world) consoleObject(name, id string, body any) (map[string]any, error) {
	rep, err := w.consoleCall(name, id, body)
	if err != nil {
		return nil, err
	}
	if rep.status != http.StatusOK {
		return nil, fmt.Errorf("the console's %s call answered %d: %s", name, rep.status, truncate(rep.body))
	}
	var doc map[string]any
	if err = json.Unmarshal(rep.body, &doc); err != nil {
		return nil, fmt.Errorf("the console's %s call answered something that is not an object: %w (%s)", name, err, truncate(rep.body))
	}
	return doc, nil
}

func objects(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if m, ok := e.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (w *world) openConsole() error {
	rep, err := w.send(newRequest("GET", "/__console/"))
	w.console.page = rep
	return err
}

func (w *world) initConsoleJournal(sc *godog.ScenarioContext) {
	sc.Step(`^a request that matched nothing$`, func() error {
		rep, err := w.send(newRequest("GET", "/claims/1"))
		if err != nil {
			return err
		}
		if rep.status != http.StatusNotFound {
			return fmt.Errorf("GET /claims/1 answered %d, want the 404 of a request that matches nothing", rep.status)
		}
		return nil
	})
	sc.Step(`^I open the console's journal and select the unmatched entry$`, func() error {
		if err := w.openConsole(); err != nil {
			return err
		}
		doc, err := w.consoleObject("unmatched", "", nil)
		if err != nil {
			return err
		}
		entries := objects(doc["requests"])
		if len(entries) == 0 {
			return errors.New("the console's journal lists no unmatched entry")
		}
		w.console.entry = entries[0]
		// As app.js does: the entry's request, with each header joined to one string, goes to the near-miss call.
		req, _ := w.console.entry["request"].(map[string]any)
		headers := map[string]string{}
		hdr, _ := req["headers"].(map[string]any)
		for k, v := range hdr {
			if list, ok := v.([]any); ok {
				parts := make([]string, 0, len(list))
				for _, p := range list {
					parts = append(parts, fmt.Sprint(p))
				}
				headers[k] = strings.Join(parts, ", ")
			} else {
				headers[k] = fmt.Sprint(v)
			}
		}
		sample := map[string]any{"method": req["method"], "url": req["url"], "headers": headers}
		if body, _ := req["body"].(string); body != "" {
			sample["body"] = body
		}
		near, err := w.consoleObject("nearMisses", "", sample)
		if err != nil {
			return err
		}
		w.console.nearMisses = objects(near["nearMisses"])
		return nil
	})
	sc.Step(`^it shows the request, the response and the closest mappings with why each missed$`, func() error {
		e := w.console.entry
		req, _ := e["request"].(map[string]any)
		resp, _ := e["response"].(map[string]any)
		if req["method"] != "GET" || req["url"] != "/claims/1" {
			return fmt.Errorf("the entry's request is %v, want GET /claims/1", req)
		}
		if _, ok := resp["status"].(float64); !ok {
			return fmt.Errorf("the entry has no response status: %v", resp)
		}
		if matched, _ := e["wasMatched"].(bool); matched {
			return errors.New("the entry says it was matched")
		}
		if len(w.console.nearMisses) == 0 {
			return errors.New("the console shows no closest mapping")
		}
		for _, nm := range w.console.nearMisses {
			stub, _ := nm["stubMapping"].(map[string]any)
			result, _ := nm["matchResult"].(map[string]any)
			missed, _ := nm["missed"].([]any)
			if stub["request"] == nil {
				return fmt.Errorf("a closest mapping has no request pattern to show: %v", nm)
			}
			if _, ok := result["distance"].(float64); !ok {
				return fmt.Errorf("a closest mapping has no distance: %v", nm)
			}
			if len(missed) == 0 {
				return fmt.Errorf("a closest mapping does not say why it missed: %v", nm)
			}
		}
		if missed, _ := w.console.nearMisses[0]["missed"].([]any); !slices.Contains(missed, any("Header [Accept]")) {
			return fmt.Errorf("the closest mapping missed on %v, want it to name Header [Accept]", missed)
		}
		return nil
	})
	w.initConsoleDryRun(sc)
}

func (w *world) initConsoleDryRun(sc *godog.ScenarioContext) {
	sc.Step(`^I dry-run "([A-Z]+) ([^"]*)" in the console$`, func(method, target string) error {
		if err := w.ensure(); err != nil {
			return err
		}
		doc, err := w.consoleObject("dryRun", "", map[string]any{"request": map[string]any{"method": method, "url": target}})
		w.console.dryRun = doc
		return err
	})
	sc.Step(`^it shows which layer and mapping would serve it and the rendered response$`, func() error {
		d := w.console.dryRun
		if matched, _ := d["matched"].(bool); !matched {
			return fmt.Errorf("the dry run matched nothing: %v", d)
		}
		if layer, _ := d["layer"].(string); layer != "global" {
			return fmt.Errorf("the dry run names layer %q, want global", d["layer"])
		}
		if stub, _ := d["stubMapping"].(map[string]any); stub == nil {
			return fmt.Errorf("the dry run names no mapping: %v", d)
		}
		resp, _ := d["response"].(map[string]any)
		if status, _ := resp["status"].(float64); status != 200 || resp["body"] != "claim one" {
			return fmt.Errorf("the dry run's response is %v, want 200 with the body \"claim one\"", resp)
		}
		return nil
	})
	sc.Step(`^nothing is journalled$`, func() error {
		rep, err := w.mustAdmin("GET", "/requests", "", 200)
		if err != nil {
			return err
		}
		var doc struct {
			Requests []any `json:"requests"`
		}
		if err = json.Unmarshal(rep.body, &doc); err != nil {
			return fmt.Errorf("read the journal: %w", err)
		}
		if len(doc.Requests) != 0 {
			return fmt.Errorf("the journal holds %d entries, want none", len(doc.Requests))
		}
		return nil
	})
}

func (w *world) shownSessions() ([]string, error) {
	doc, err := w.consoleObject("sessions", "", nil)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, s := range objects(doc["sessions"]) {
		id, _ := s["id"].(string)
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (w *world) initConsoleSessions(sc *godog.ScenarioContext) {
	sc.Step(`^(\d+) shadow mocks$`, func(n int) error {
		return w.record(func() error {
			for i := range n {
				if err := w.createSession("key-based", fmt.Sprintf("C%d", i), "{}"); err != nil {
					return err
				}
			}
			return nil
		})
	})
	sc.Step(`^I open the console's shadow mocks and delete one$`, func() error {
		if err := w.openConsole(); err != nil {
			return err
		}
		ids, err := w.shownSessions()
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return errors.New("the console lists no shadow mock")
		}
		w.console.deleted = ids[0]
		rep, err := w.consoleCall("deleteSession", w.console.deleted, nil)
		if err != nil {
			return err
		}
		if rep.status != http.StatusOK {
			return fmt.Errorf("the console's deleteSession call answered %d: %s", rep.status, truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^(\d+) remain$`, func(n int) error {
		ids, err := w.shownSessions()
		if err != nil {
			return err
		}
		if len(ids) != n {
			return fmt.Errorf("the console lists %d shadow mocks, want %d", len(ids), n)
		}
		if slices.Contains(ids, w.console.deleted) {
			return fmt.Errorf("the console still lists the deleted shadow mock %s", w.console.deleted)
		}
		w.console.shown = ids
		return nil
	})
	sc.Step(`^the same two are listed by the admin API$`, func() error {
		rep, err := w.mustAdmin("GET", "/sessions", "", 200)
		if err != nil {
			return err
		}
		var doc struct {
			Sessions []struct {
				ID string `json:"id"`
			} `json:"sessions"`
		}
		if err = json.Unmarshal(rep.body, &doc); err != nil {
			return fmt.Errorf("read the sessions: %w", err)
		}
		var ids []string
		for _, s := range doc.Sessions {
			ids = append(ids, s.ID)
		}
		sort.Strings(ids)
		if !slices.Equal(ids, w.console.shown) {
			return fmt.Errorf("the admin API lists %v, the console listed %v", ids, w.console.shown)
		}
		return nil
	})
}

// initConsoleViews registers the steps that play the console's editing, counters and recordings views. Each makes
// one named call from calls.json, as the view's button does, and leaves the answer as the scenario's last response.
func (w *world) initConsoleViews(sc *godog.ScenarioContext) {
	const target = `^in the console I make the "([A-Za-z]+)" call(?: on "([^"]*)")?(?: for the stub "([^"]*)")?`
	sc.Step(target+`$`, func(name, id, stub string) error {
		_, err := w.consoleCallFor(name, id, stub, nil)
		return err
	})
	sc.Step(target+` with:$`, func(name, id, stub string, doc *godog.DocString) error {
		var body any
		if err := json.Unmarshal([]byte(doc.Content), &body); err != nil {
			return fmt.Errorf("the body of the console's %s call is not JSON: %w", name, err)
		}
		_, err := w.consoleCallFor(name, id, stub, body)
		return err
	})
}

func (w *world) initConsoleAccess(sc *godog.ScenarioContext) {
	sc.Step(`^I open the console$`, func() error {
		if err := w.openConsole(); err != nil {
			return err
		}
		w.console.refused = map[string]int{}
		rep, err := w.consoleCall("journal", "", nil)
		if err != nil {
			return err
		}
		w.console.refused["journal"] = rep.status
		return nil
	})
	sc.Step(`^I am not asked to sign in$`, func() error {
		if w.console.page.status != http.StatusOK {
			return fmt.Errorf("the console page answered %d", w.console.page.status)
		}
		if got := w.console.refused["journal"]; got != http.StatusOK {
			return fmt.Errorf("the console's journal call answered %d, want 200 with no token", got)
		}
		return nil
	})
	sc.Step(`^I open the console without a token$`, func() error {
		w.adminToken = ""
		if err := w.openConsole(); err != nil {
			return err
		}
		calls, err := consoleCalls()
		if err != nil {
			return err
		}
		w.console.refused = map[string]int{}
		for name, spec := range calls {
			var body any
			if spec[0] != http.MethodGet && spec[0] != http.MethodDelete {
				body = map[string]any{}
			}
			rep, err := w.consoleCall(name, "S1", body)
			if err != nil {
				return err
			}
			w.console.refused[name] = rep.status
		}
		return nil
	})
	sc.Step(`^the admin calls it makes are refused with 401$`, func() error {
		if w.console.page.status != http.StatusOK {
			return fmt.Errorf("the console page answered %d, want the static page", w.console.page.status)
		}
		if len(w.console.refused) == 0 {
			return errors.New("the console made no call")
		}
		for name, status := range w.console.refused {
			if status != http.StatusUnauthorized {
				return fmt.Errorf("the console's %s call answered %d, want 401", name, status)
			}
		}
		// The refusal is the token's doing: the same call with a token is served.
		w.adminToken = "admin-token-0123456789"
		defer func() { w.adminToken = "" }()
		_, err := w.consoleObject("journal", "", nil)
		return err
	})
}

// cliRecorder carries the command line's calls to the engine under test, and records them. A call to any other
// server, which this harness cannot reach, is answered with a canned success.
type cliRecorder struct{ w *world }

func (t cliRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		if body, err = io.ReadAll(req.Body); err != nil {
			return nil, fmt.Errorf("read the command's request body: %w", err)
		}
		_ = req.Body.Close()
	}
	call := cliCall{method: req.Method, uri: req.URL.RequestURI(), auth: req.Header.Get("Authorization"), body: string(body)}
	var resp *http.Response
	if t.w.srv != nil && req.URL.Host == strings.TrimPrefix(t.w.srv.URL, "http://") {
		out := req.Clone(req.Context())
		out.Body = io.NopCloser(bytes.NewReader(body))
		out.ContentLength = int64(len(body))
		var err error
		if resp, err = t.w.client.Transport.RoundTrip(out); err != nil {
			return nil, fmt.Errorf("send the command's call to the engine: %w", err)
		}
	} else {
		resp = &http.Response{
			StatusCode: http.StatusCreated, Status: "201 Created", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"canned"}`)), Request: req,
		}
	}
	answer, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read the engine's answer: %w", err)
	}
	call.status, call.answer = resp.StatusCode, string(answer)
	t.w.console.cliCalls = append(t.w.console.cliCalls, call)
	resp.Body = io.NopCloser(bytes.NewReader(answer))
	return resp, nil
}

func (w *world) initCLI(sc *godog.ScenarioContext) {
	sc.Step(`^the environment variable "([^"]*)" is a Tool app key$`, func(name string) error {
		w.cfgEnv[name] = toolKey
		w.console.cliKey = toolKey
		return nil
	})
	sc.Step(`^I run "shadowmock ([^"]*)"$`, func(command string) error {
		args := strings.Fields(w.sub(command))
		if !slices.Contains(args, "--url") {
			if err := w.ensure(); err != nil {
				return err
			}
			args = append(args, "--url", w.adminBase())
		}
		w.console.cliCalls = nil
		var out bytes.Buffer
		w.console.cliErr = cli.Run(context.Background(), args, cli.Env{
			Lookup: func(k string) (string, bool) { v, ok := w.cfgEnv[k]; return v, ok },
			Stdout: &out,
			Client: &http.Client{Transport: cliRecorder{w}},
		})
		w.console.cliOut = out.String()
		return nil
	})
	sc.Step(`^the admin API received "([^"]*)"$`, func(want string) error {
		want = w.sub(want)
		if len(w.console.cliCalls) != 1 {
			return fmt.Errorf("the command made %d calls (%v), want exactly 1", len(w.console.cliCalls), w.console.cliCalls)
		}
		if got := w.console.cliCalls[0]; got.method+" "+got.uri != want {
			return fmt.Errorf("the command called %s %s, want %s", got.method, got.uri, want)
		}
		return nil
	})
	sc.Step(`^the output is the API's answer$`, func() error {
		if len(w.console.cliCalls) != 1 {
			return fmt.Errorf("the command made %d calls, want 1", len(w.console.cliCalls))
		}
		call := w.console.cliCalls[0]
		want := call.answer
		if want != "" {
			want += "\n" // the command ends a printed answer with a newline and prints nothing for an empty one
		}
		if w.console.cliOut != want {
			return fmt.Errorf("the command printed %q, the API answered %q", w.console.cliOut, call.answer)
		}
		if failed := call.status >= http.StatusBadRequest; failed != (w.console.cliErr != nil) {
			return fmt.Errorf("the API answered %d but the command returned %v", call.status, w.console.cliErr)
		}
		return nil
	})
	sc.Step(`^the call carries the key as a bearer token$`, func() error {
		if w.console.cliErr != nil {
			return fmt.Errorf("the command failed: %w", w.console.cliErr)
		}
		if len(w.console.cliCalls) != 1 {
			return fmt.Errorf("the command made %d calls, want 1", len(w.console.cliCalls))
		}
		if got, want := w.console.cliCalls[0].auth, "Bearer "+w.console.cliKey; got != want {
			return fmt.Errorf("Authorization = %q, want %q", got, want)
		}
		return nil
	})
	sc.Step(`^every sub-command maps to one documented admin API operation$`, func() error {
		spec, err := contract.Load()
		if err != nil {
			return err
		}
		for _, c := range cli.Commands {
			routes := []string{c.Route}
			if strings.Contains(c.Route, "{kind}") {
				routes = []string{strings.ReplaceAll(c.Route, "{kind}", "key-based"), strings.ReplaceAll(c.Route, "{kind}", "url-based")}
			}
			for _, r := range routes {
				if spec.FindOperation(c.Method, "/__admin"+strings.ReplaceAll(r, "{id}", "S1")) == nil {
					return fmt.Errorf("%q calls %s %s, which the API does not describe", c.Name, c.Method, r)
				}
			}
		}
		return nil
	})
}
