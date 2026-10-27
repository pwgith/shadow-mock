package acceptance

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/store"
	"github.com/pwgith/shadow-mock/internal/tenant"
)

// initAssert registers the steps that check responses, the journal and engine state.
func (w *world) initAssert(sc *godog.ScenarioContext) {
	w.initResponseAsserts(sc)
	w.initJSONAsserts(sc)
	w.initAdminAsserts(sc)
	w.initJournalAsserts(sc)
	w.initStateAsserts(sc)
	w.initMappingAsserts(sc)
}

func (w *world) needLast() (*reply, error) {
	if w.last == nil {
		return nil, fmt.Errorf("no response has been received yet")
	}
	return w.last, nil
}

func (w *world) initResponseAsserts(sc *godog.ScenarioContext) {
	sc.Step(`^the response status is (\d+)$`, func(want int) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if r.status != want {
			return fmt.Errorf("status = %d, want %d; body: %s", r.status, want, truncate(r.body))
		}
		return nil
	})
	sc.Step(`^the response status message is "([^"]*)"$`, func(want string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if r.statusText != want {
			return fmt.Errorf("status message = %q, want %q", r.statusText, want)
		}
		return nil
	})
	sc.Step(`^the last response status is (\d+)$`, func(want int) error { return w.statusIs(w.last, want) })
	sc.Step(`^both responses have status (\d+)$`, func(want int) error {
		if len(w.history) < 2 {
			return fmt.Errorf("fewer than two responses were received")
		}
		for _, r := range w.history[len(w.history)-2:] {
			if err := w.statusIs(r, want); err != nil {
				return err
			}
		}
		return nil
	})
	sc.Step(`^the response body is "([^"]*)"$`, func(want string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if got := string(r.body); got != w.sub(want) {
			return fmt.Errorf("body = %q, want %q (status %d)", truncate(r.body), w.sub(want), r.status)
		}
		return nil
	})
	sc.Step(`^the response body contains "([^"]*)"$`, func(part string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if !strings.Contains(string(r.body), w.sub(part)) {
			return fmt.Errorf("body does not contain %q: %s", part, truncate(r.body))
		}
		return nil
	})
	sc.Step(`^the response body is empty$`, func() error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if len(r.body) != 0 {
			return fmt.Errorf("body has %d bytes, want none", len(r.body))
		}
		return nil
	})
	sc.Step(`^the response body is the (\d+) bytes of "([^"]*)"$`, func(n int, file string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		want, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(file)))
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		if len(want) != n || !bytes.Equal(r.body, want) {
			return fmt.Errorf("body is %d bytes and differs from %s (%d bytes)", len(r.body), file, len(want))
		}
		return nil
	})
	sc.Step(`^the response header "([^"]*)" is "([^"]*)"$`, func(name, want string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		want = w.sub(want)
		got := r.header.Get(name)
		switch {
		case want == "absent" && got != "":
			return fmt.Errorf("header %s = %q, want it absent", name, got)
		case want != "absent" && got != want:
			return fmt.Errorf("header %s = %q, want %q", name, got, want)
		}
		return nil
	})
	sc.Step(`^the response header "([^"]*)" is absent$`, func(name string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if v := r.header.Values(name); len(v) > 0 {
			return fmt.Errorf("header %s = %v, want it absent", name, v)
		}
		return nil
	})
	sc.Step(`^the response has (\d+) "([^"]*)" headers$`, func(n int, name string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if got := len(r.header.Values(name)); got != n {
			return fmt.Errorf("%d %s headers, want %d", got, name, n)
		}
		return nil
	})
	sc.Step(`^the response arrives after at least (\d+) ms$`, func(ms int) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		if r.elapsed < time.Duration(ms)*time.Millisecond {
			return fmt.Errorf("the response took %v, want at least %d ms", r.elapsed, ms)
		}
		return nil
	})
	sc.Step(`^the response statuses in order are (.*)$`, func(list string) error {
		got := make([]string, 0, len(w.batch))
		for _, r := range w.batch {
			got = append(got, strconv.Itoa(r.status))
		}
		return sameList(got, splitList(list))
	})
	sc.Step(`^the response bodies in order are (.*)$`, func(list string) error {
		got := make([]string, 0, len(w.batch))
		for _, r := range w.batch {
			got = append(got, string(r.body))
		}
		return sameList(got, splitList(list))
	})
	sc.Step(`^the JSON values at "([^"]*)" in order are (.*)$`, func(path, list string) error {
		got := make([]string, 0, len(w.batch))
		for _, r := range w.batch {
			res, err := jsonAt(r.body, path)
			if err != nil || len(res) == 0 {
				return fmt.Errorf("no value at %s in %s (%v)", path, truncate(r.body), err)
			}
			got = append(got, text(res[0]))
		}
		return sameList(got, splitList(list))
	})
	sc.Step(`^the response body lists near misses from layers "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		for _, layer := range []string{a, b} {
			if !strings.Contains(string(r.body), layer+" layer") {
				return fmt.Errorf("no near miss from the %s layer: %s", layer, truncate(r.body))
			}
		}
		return nil
	})
	sc.Step(`^I remember the response JSON at "([^"]*)" as "([^"]*)"$`, func(path, name string) error {
		v, found, err := w.jsonOne(path)
		if err != nil || !found {
			return fmt.Errorf("no value at %s (%v)", path, err)
		}
		w.vars[name] = text(v)
		return nil
	})
	sc.Step(`^an HTTP client configured with base URL "([^"]*)"$`, func(base string) error {
		w.httpClientBase = strings.TrimSuffix(w.sub(base), "/")
		return nil
	})
	sc.Step(`^the client calls "([^"]*) ([^"]*)" relative to its base URL$`, func(method, rel string) error {
		return w.sendOne(newRequest(method, w.httpClientBase+"/"+strings.TrimPrefix(rel, "/")))
	})
}

func (w *world) statusIs(r *reply, want int) error {
	if r == nil {
		return fmt.Errorf("no response has been received yet")
	}
	if r.status != want {
		return fmt.Errorf("status = %d, want %d", r.status, want)
	}
	return nil
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.Trim(strings.TrimSpace(p), `"`))
	}
	return out
}

func sameList(got, want []string) error {
	if !slices.Equal(got, want) {
		return fmt.Errorf("got %v, want %v", got, want)
	}
	return nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (w *world) initJSONAsserts(sc *godog.ScenarioContext) {
	sc.Step(`^the response JSON at "([^"]*)" is "\{mock base\}/d/" followed by the id$`, func(path string) error {
		v, found, err := w.jsonOne(path)
		if err != nil || !found {
			return fmt.Errorf("no value at %s (%v)", path, err)
		}
		id, _, idErr := w.jsonOne("$.id")
		if idErr != nil {
			return idErr
		}
		if want := w.srv.URL + "/d/" + text(id); text(v) != want {
			return fmt.Errorf("%s = %q, want %q", path, text(v), want)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is "([^"]*)"$`, func(path, want string) error {
		v, found, err := w.jsonOne(path)
		if err != nil {
			return err
		}
		if !found || text(v) != w.sub(want) {
			return fmt.Errorf("%s = %v (found %v), want %q", path, v, found, w.sub(want))
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is (-?\d+(?:\.\d+)?)$`, func(path, want string) error {
		v, found, err := w.jsonOne(path)
		if err != nil {
			return err
		}
		if !found || text(v) != want {
			return fmt.Errorf("%s = %v (found %v), want %s", path, v, found, want)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is null$`, func(path string) error {
		v, found, err := w.jsonOne(path)
		if err != nil || !found || v != nil {
			return fmt.Errorf("%s = %v (found %v, %v), want null", path, v, found, err)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is absent$`, func(path string) error {
		_, found, err := w.jsonOne(path)
		if err != nil || found {
			return fmt.Errorf("%s is present (%v), want it absent", path, err)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is a number$`, func(path string) error {
		v, _, err := w.jsonOne(path)
		if _, ok := v.(float64); err != nil || !ok {
			return fmt.Errorf("%s = %v (%v), want a number", path, v, err)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is a UUID$`, func(path string) error {
		v, _, err := w.jsonOne(path)
		if s, ok := v.(string); err != nil || !ok || !uuidPattern.MatchString(s) {
			return fmt.Errorf("%s = %v (%v), want a UUID", path, v, err)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is the current org$`, func(path string) error {
		v, _, err := w.jsonOne(path)
		if err != nil || text(v) != string(tenant.Local) {
			return fmt.Errorf("%s = %v (%v), want %s", path, v, err, tenant.Local)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" is the first 8 characters of "([^"]*)"$`, func(path, of string) error {
		v, _, err := w.jsonOne(path)
		want := w.sub(of)
		if len(want) > 8 {
			want = want[:8]
		}
		if err != nil || text(v) != want {
			return fmt.Errorf("%s = %v (%v), want %s", path, v, err, want)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" matches "([^"]*)"$`, func(path, pattern string) error {
		v, _, err := w.jsonOne(path)
		re, rerr := regexp.Compile(pattern)
		if rerr != nil || err != nil || !re.MatchString(text(v)) {
			return fmt.Errorf("%s = %v (%v), want a match for %s", path, v, err, pattern)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" contains "([^"]*)"$`, func(path, part string) error {
		v, _, err := w.jsonOne(path)
		if err != nil {
			return err
		}
		part = w.sub(part)
		if list, ok := v.([]any); ok {
			for _, e := range list {
				if text(e) == part {
					return nil
				}
			}
			return fmt.Errorf("%s = %v does not contain %q", path, v, part)
		}
		if !strings.Contains(text(v), part) {
			return fmt.Errorf("%s = %v does not contain %q", path, v, part)
		}
		return nil
	})
	sc.Step(`^the response JSON at "([^"]*)" has (\d+) items$`, func(path string, n int) error {
		r, err := w.needLast()
		if err != nil {
			return err
		}
		got, err := listLen(r.body, w.sub(path))
		if err != nil || got != n {
			return fmt.Errorf("%s has %d items (%v), want %d", path, got, err, n)
		}
		return nil
	})
}

func (w *world) initAdminAsserts(sc *godog.ScenarioContext) {
	get := func(path string) (*reply, error) { return w.mustAdmin("GET", path, "", 200) }
	sc.Step(`^admin "GET" "([^"]*)" lists "([^"]*)"$`, func(path, name string) error {
		return w.adminListHas(path, name, true)
	})
	sc.Step(`^admin "GET" "([^"]*)" does not list "([^"]*)"$`, func(path, name string) error {
		return w.adminListHas(path, name, false)
	})
	sc.Step(`^admin "GET" "([^"]*)" returns body "([^"]*)"$`, func(path, want string) error {
		r, err := get(path)
		if err != nil {
			return err
		}
		if string(r.body) != want {
			return fmt.Errorf("GET %s body = %q, want %q", path, truncate(r.body), want)
		}
		return nil
	})
	sc.Step(`^admin "GET" "([^"]*)" returns JSON at "([^"]*)" equal to "([^"]*)"$`, func(path, jp, want string) error {
		r, err := get(path)
		if err != nil {
			return err
		}
		res, err := jsonAt(r.body, jp)
		if err != nil || len(res) == 0 || text(res[0]) != want {
			return fmt.Errorf("GET %s %s = %v (%v), want %s", path, jp, res, err, want)
		}
		return nil
	})
	sc.Step(`^admin "GET" "([^"]*)" (?:lists|returns) (\d+) (requests?|mappings?)$`, func(path string, n int, kind string) error {
		r, err := get(path)
		if err != nil {
			return err
		}
		field := "$.requests"
		if strings.HasPrefix(kind, "mapping") {
			field = "$.mappings"
		}
		got, err := listLen(r.body, field)
		if err != nil || got != n {
			return fmt.Errorf("GET %s lists %d %s (%v), want %d", path, got, kind, err, n)
		}
		return nil
	})
	sc.Step(`^admin "GET" "([^"]*)" lists 1 request with url "([^"]*)"$`, func(path, url string) error {
		r, err := get(path)
		if err != nil {
			return err
		}
		res, err := jsonAt(r.body, "$.requests[*].url")
		if err != nil || len(res) != 1 || text(res[0]) != url {
			return fmt.Errorf("GET %s lists %v (%v), want one request with url %s", path, res, err, url)
		}
		return nil
	})
	sc.Step(`^admin "GET" "([^"]*)" returns status (\d+)$`, func(path string, want int) error {
		r, err := w.adminCall("GET", path, "", nil)
		if err != nil {
			return err
		}
		return w.statusIs(r, want)
	})
	sc.Step(`^admin "([^"]*)" "([^"]*)" with pattern '(.*)' returns (\d+)$`, func(method, path, pattern string, want int) error {
		r, err := w.mustAdmin(method, path, pattern, 200)
		if err != nil {
			return err
		}
		res, err := jsonAt(r.body, "$.count")
		if err != nil || len(res) == 0 || text(res[0]) != strconv.Itoa(want) {
			return fmt.Errorf("%s %s count = %v (%v), want %d", method, path, res, err, want)
		}
		return nil
	})
	sc.Step(`^admin "GET" "/counters" shows count (\d+) for stub "([^"]*)"$`, func(want int, id string) error {
		r, err := get("/counters")
		if err != nil {
			return err
		}
		res, err := jsonAt(r.body, `$.counters[?(@.stubMappingId=='`+id+`')].count`)
		if err != nil {
			return err
		}
		got := 0 // a reset counter is removed, which reads as zero
		if len(res) > 0 {
			got, _ = strconv.Atoi(text(res[0])) //nolint:errcheck // text of a JSON number
		}
		if got != want {
			return fmt.Errorf("counter of %s = %d, want %d", id, got, want)
		}
		return nil
	})
	sc.Step(`^admin "GET" "/counters" lists no counter for "([^"]*)"$`, func(path string) error {
		ms, err := get("/mappings")
		if err != nil {
			return err
		}
		ids, err := jsonAt(ms.body, `$.mappings[?(@.request.urlPath=='`+path+`' || @.request.url=='`+path+`')].id`)
		if err != nil {
			return err
		}
		cs, err := get("/counters")
		if err != nil {
			return err
		}
		for _, id := range ids {
			if found, _ := jsonAt(cs.body, `$.counters[?(@.stubMappingId=='`+text(id)+`')]`); len(found) > 0 { //nolint:errcheck // an unparsable list reads as no counters
				return fmt.Errorf("a counter exists for %s (%s)", path, text(id))
			}
		}
		return nil
	})
}

func (w *world) adminListHas(path, name string, want bool) error {
	r, err := w.mustAdmin("GET", path, "", 200)
	if err != nil {
		return err
	}
	res, err := jsonAt(r.body, "$[*]")
	if err != nil {
		return err
	}
	has := false
	for _, e := range res {
		has = has || text(e) == name
	}
	if has != want {
		return fmt.Errorf("GET %s lists %v; has %q = %v, want %v", path, res, name, has, want)
	}
	return nil
}

// latestEntry fetches the newest entry of the global journal.
func (w *world) latestEntry() ([]byte, error) {
	r, err := w.mustAdmin("GET", "/requests?limit=1", "", 200)
	if err != nil {
		return nil, err
	}
	res, err := jsonAt(r.body, "$.requests[0]")
	if err != nil || len(res) == 0 {
		return nil, fmt.Errorf("the journal is empty (%v)", err)
	}
	return jsonBytes(res[0]), nil
}

func (w *world) initJournalAsserts(sc *godog.ScenarioContext) {
	sc.Step(`^the journal entry records detection stage "([^"]*)"$`, func(stage string) error {
		return w.latestHas("$.shadowMock.detection.stage", stage)
	})
	sc.Step(`^the journal entry records "scanTruncated" as true$`, func() error {
		return w.latestHas("$.shadowMock.detection.scanTruncated", "true")
	})
	sc.Step(`^the journal entry records an ambiguity between "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		e, err := w.latestEntry()
		if err != nil {
			return err
		}
		res, err := jsonAt(e, "$.shadowMock.detection.ambiguousSessionIds[*]")
		if err != nil {
			return err
		}
		got := make([]string, 0, len(w.batch))
		for _, id := range res {
			got = append(got, text(id))
		}
		for _, name := range []string{a, b} {
			if !slices.Contains(got, w.vars[name+".id"]) {
				return fmt.Errorf("ambiguousSessionIds %v lacks %s (%s)", got, name, w.vars[name+".id"])
			}
		}
		return nil
	})
	sc.Step(`^the entry for "([^"]*)" has "([^"]*)" equal to (?:"([^"]*)"|(null))$`, func(url, path, want, null string) error {
		r, err := w.mustAdmin("GET", "/requests", "", 200)
		if err != nil {
			return err
		}
		res, err := jsonAt(r.body, `$.requests[?(@.request.url=='`+url+`')]`)
		if err != nil || len(res) == 0 {
			return fmt.Errorf("no journal entry for %s (%v)", url, err)
		}
		return entryHas(jsonBytes(res[0]), "$."+path, w.sub(want), null != "")
	})
	sc.Step(`^the latest journal entry has "([^"]*)" equal to "([^"]*)" and "([^"]*)" equal to "([^"]*)"$`, func(p1, v1, p2, v2 string) error {
		e, err := w.latestEntry()
		if err != nil {
			return err
		}
		if err := entryHas(e, "$."+p1, w.sub(v1), false); err != nil {
			return err
		}
		return entryHas(e, "$."+p2, w.sub(v2), false)
	})
	sc.Step(`^a key-based shadow mock "([^"]*)" is created at time T$`, func(name string) error {
		w.marks["T"] = time.Now()
		return w.record(func() error { return w.createSession("key-based", name, "{}") })
	})
	sc.Step(`^a request with "([^"]*)" in its body is attributed to "([^"]*)" no later than T \+ 100 ms$`, func(key, name string) error {
		if err := w.sendKeyInBody(key); err != nil {
			return err
		}
		if d := time.Since(w.marks["T"]); d > 100*time.Millisecond {
			return fmt.Errorf("attribution took %v after creation, want at most 100 ms", d)
		}
		return w.latestHas("$.shadowMock.sessionId", w.vars[name+".id"])
	})
	sc.Step(`^"([^"]*)" is deleted at time T2$`, func(name string) error {
		w.marks["T2"] = time.Now()
		_, err := w.mustAdmin("DELETE", "/sessions/${"+name+".id}", "", 200)
		return err
	})
	sc.Step(`^a request with "([^"]*)" in its body is served by the global session no later than T2 \+ 100 ms$`, func(key string) error {
		if err := w.sendKeyInBody(key); err != nil {
			return err
		}
		if d := time.Since(w.marks["T2"]); d > 100*time.Millisecond {
			return fmt.Errorf("the global session served it %v after deletion, want at most 100 ms", d)
		}
		e, err := w.latestEntry()
		if err != nil {
			return err
		}
		return entryHas(e, "$.shadowMock.sessionId", "", true)
	})
}

func (w *world) sendKeyInBody(key string) error {
	r := newRequest("POST", "/b")
	r.body = []byte(`{"run":"` + w.sub(key) + `"}`)
	r.header.Set("Content-Type", "application/json")
	return w.sendOne(r)
}

func (w *world) latestHas(path, want string) error {
	e, err := w.latestEntry()
	if err != nil {
		return err
	}
	return entryHas(e, path, want, false)
}

func entryHas(entry []byte, path, want string, null bool) error {
	res, err := jsonAt(entry, path)
	if err != nil {
		return err
	}
	switch {
	case null && (len(res) == 0 || res[0] != nil):
		return fmt.Errorf("%s = %v, want null", path, res)
	case !null && (len(res) == 0 || text(res[0]) != want):
		return fmt.Errorf("%s = %v, want %q", path, res, want)
	}
	return nil
}

func (w *world) initStateAsserts(sc *godog.ScenarioContext) {
	scope := func(name string) store.Scope { return store.Scope{Tenant: tenant.Local, SessionID: w.vars[name+".id"]} }
	sc.Step(`^the store holds no call counters for "([^"]*)"$`, func(name string) error {
		cs, err := w.app.Service().Counters.List(context.Background(), scope(name))
		if err != nil || len(cs) != 0 {
			return fmt.Errorf("%d counters remain for %s (%v)", len(cs), name, err)
		}
		return nil
	})
	sc.Step(`^the store holds no mappings, journal entries or call counters for "([^"]*)"$`, func(name string) error {
		svc, ctx, sc := w.app.Service(), context.Background(), scope(name)
		ms, err := svc.Mappings.List(ctx, sc)
		if err != nil {
			return err
		}
		cs, err := svc.Counters.List(ctx, sc)
		if err != nil {
			return err
		}
		if n := svc.Journal.Count(sc, nil); len(ms)+len(cs)+n != 0 {
			return fmt.Errorf("%s still holds %d mappings, %d counters and %d journal entries", name, len(ms), len(cs), n)
		}
		return nil
	})
	sc.Step(`^the blob store holds no files for "([^"]*)"$`, func(name string) error {
		files, err := w.app.Service().Files.List(context.Background(), scope(name))
		if err != nil || len(files) != 0 {
			return fmt.Errorf("%s still holds files %v (%v)", name, files, err)
		}
		return nil
	})
	sc.Step(`^a file under "([^"]*)" contains "([^"]*)"$`, func(dir, part string) error {
		var found bool
		err := filepath.Walk(filepath.Join(w.root, filepath.FromSlash(dir)), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			found = found || strings.Contains(string(b), part)
			return nil
		})
		if err != nil || !found {
			return fmt.Errorf("no file under %s contains %q (%v)", dir, part, err)
		}
		return nil
	})
}

func (w *world) initMappingAsserts(sc *godog.ScenarioContext) {
	sc.Step(`^the admin mapping "([^"]*)" has name "([^"]*)"$`, func(id, name string) error {
		r, err := w.mustAdmin("GET", "/mappings/"+id, "", 200)
		if err != nil {
			return err
		}
		res, err := jsonAt(r.body, "$.name")
		if err != nil || len(res) == 0 || text(res[0]) != name {
			return fmt.Errorf("mapping %s has name %v (%v), want %q", id, res, err, name)
		}
		return nil
	})
}
