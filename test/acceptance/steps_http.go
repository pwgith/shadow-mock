package acceptance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/pwgith/shadow-mock/internal/ids"
)

// initHTTP registers the steps that send mock traffic and admin calls.
func (w *world) initHTTP(sc *godog.ScenarioContext) {
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)"$`, func(m, p string) error { return w.sendSimple(m, p, nil, "") })
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with body:$`, func(m, p string, doc *godog.DocString) error {
		return w.sendSimple(m, p, nil, doc.Content, docType(doc))
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with header "([^"]*)" set to "([^"]*)"$`, func(m, p, h, v string) error {
		return w.sendSimple(m, p, http.Header{h: {v}}, "")
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" via "([^"]*)"\s*(?:with header "([^"]*)" set to "([^"]*)")?$`,
		func(m, p, name, h, v string) error {
			hdr := http.Header{}
			if h != "" {
				hdr.Set(h, v)
			}
			return w.sendSimple(m, w.urlBasedPath(name, p), hdr, "")
		})
	sc.Step(`^I send (\d+) "([^"]*)" requests to "([^"]*)"$`, func(n int, m, p string) error {
		return w.sendMany(n, m, p, nil)
	})
	sc.Step(`^I send (\d+) "([^"]*)" requests to "([^"]*)" with header "([^"]*)" set to "([^"]*)"$`, func(n int, m, p, h, v string) error {
		return w.sendMany(n, m, p, http.Header{h: {v}})
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with basic auth "([^"]*)" / "([^"]*)"$`, func(m, p, u, pw string) error {
		r := newRequest(m, p)
		r.basic = [2]string{u, pw}
		return w.sendOne(r)
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with the bytes "([^"]*)"$`, func(m, p, hexBytes string) error {
		b, err := hex.DecodeString(strings.ReplaceAll(hexBytes, " ", ""))
		if err != nil {
			return fmt.Errorf("parse bytes %q: %w", hexBytes, err)
		}
		r := newRequest(m, p)
		r.body = b
		return w.sendOne(r)
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with an 11 MiB body$`, func(m, p string) error {
		r := newRequest(m, p)
		r.body = bytes.Repeat([]byte("x"), 11<<20)
		r.header.Set("Content-Type", "application/octet-stream")
		return w.sendOne(r)
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with a JSON body where "([^"]*)" starts at byte (\d+)$`, func(m, p, key string, at int) error {
		const head, mid = `{"pad":"`, `","run":"`
		pad := at - len(head) - len(mid)
		if pad < 0 {
			return fmt.Errorf("byte %d is too early for the padded body", at)
		}
		r := newRequest(m, p)
		r.body = []byte(head + strings.Repeat("x", pad) + mid + w.sub(key) + `"}`)
		r.header.Set("Content-Type", "application/json")
		return w.sendOne(r)
	})
	sc.Step(`^I send a "([^"]*)" request to "([^"]*)" with a gzip-compressed JSON body containing "([^"]*)"$`, func(m, p, key string) error {
		z, err := gzipBody([]byte(`{"run":"` + w.sub(key) + `"}`))
		if err != nil {
			return err
		}
		r := newRequest(m, p)
		r.body = z
		r.header.Set("Content-Type", "application/json")
		r.header.Set("Content-Encoding", "gzip")
		return w.sendOne(r)
	})
	sc.Step(`^I send a request:$`, w.sendTable)

	sc.Step(`^a "([^"]*)" request to "([^"]*)" returns status (\d+)$`, func(m, p string, want int) error {
		return w.sendExpectStatus(newRequest(m, p), want)
	})
	sc.Step(`^a "([^"]*)" request to "([^"]*)" via "([^"]*)" returns status (\d+)$`, func(m, p, name string, want int) error {
		return w.sendExpectStatus(newRequest(m, w.urlBasedPath(name, p)), want)
	})
	sc.Step(`^a "([^"]*)" request to "([^"]*)" returns body "([^"]*)"$`, func(m, p, want string) error {
		return w.sendExpectBody(newRequest(m, p), want)
	})
	sc.Step(`^a "([^"]*)" request to "([^"]*)" with key "([^"]*)" returns body "([^"]*)"$`, func(m, p, key, want string) error {
		r := newRequest(m, p)
		r.header.Set("X-Shadow-Mock-Key", key)
		return w.sendExpectBody(r, want)
	})

	sc.Step(`^I call admin "([^"]*)" "([^"]*)"$`, func(m, p string) error { return w.callAdmin(m, p, "", nil) })
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with:$`, func(m, p string, doc *godog.DocString) error {
		return w.callAdmin(m, p, doc.Content, nil)
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)"\s*with JSON '(.*)'$`, func(m, p, body string) error {
		return w.callAdmin(m, p, body, nil)
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with header "([^"]*)" set to "([^"]*)"$`, func(m, p, h, v string) error {
		return w.callAdmin(m, p, "", http.Header{h: {w.sub(v)}})
	})
	sc.Step(`^I call admin "([^"]*)" "([^"]*)" with header "([^"]*)" set to "([^"]*)" and body:$`, func(m, p, h, v string, doc *godog.DocString) error {
		return w.callAdmin(m, p, doc.Content, http.Header{h: {w.sub(v)}})
	})
	w.initSessionCalls(sc)
}

func docType(doc *godog.DocString) string {
	if doc.MediaType == "json" {
		return "application/json"
	}
	return "text/plain"
}

func (w *world) urlBasedPath(name, path string) string {
	return "/d/" + w.vars[name+".id"] + path
}

func (w *world) sendOne(r *request) error {
	_, err := w.send(r)
	var te *transportError
	if errors.As(err, &te) {
		// A broken connection is what a fault scenario expects; the Then step checks for it.
		w.sendErr, w.last = te, nil
		return nil
	}
	return err
}

func (w *world) sendSimple(method, path string, header http.Header, body string, contentType ...string) error {
	r := newRequest(method, path)
	for k, vs := range header {
		for _, v := range vs {
			r.header.Add(k, v)
		}
	}
	if body != "" {
		r.body = []byte(w.sub(body))
		if len(contentType) > 0 {
			r.header.Set("Content-Type", contentType[0])
		}
	}
	return w.sendOne(r)
}

func (w *world) sendMany(n int, method, path string, header http.Header) error {
	w.batch = nil
	for range n {
		r := newRequest(method, path)
		for k, vs := range header {
			r.header[k] = vs
		}
		rep, err := w.send(r)
		if err != nil {
			return err
		}
		w.batch = append(w.batch, rep)
	}
	return nil
}

func (w *world) sendExpectStatus(r *request, want int) error {
	rep, err := w.send(r)
	if err != nil {
		return err
	}
	if rep.status != want {
		return fmt.Errorf("%s %s = %d, want %d; body: %s", r.method, r.target, rep.status, want, truncate(rep.body))
	}
	return nil
}

func (w *world) sendExpectBody(r *request, want string) error {
	rep, err := w.send(r)
	if err != nil {
		return err
	}
	if got := string(rep.body); got != want {
		return fmt.Errorf("%s %s body = %q, want %q (status %d)", r.method, r.target, truncate(rep.body), want, rep.status)
	}
	return nil
}

// sendTable sends one request per data row of a table with the columns method, path,
// header:<name>, query:<name>, cookie:<name> and body.
func (w *world) sendTable(t *godog.Table) error {
	if len(t.Rows) < 2 {
		return fmt.Errorf("the request table needs a header row and a data row")
	}
	cols := t.Rows[0].Cells
	for _, row := range t.Rows[1:] {
		r := newRequest("GET", "/")
		query := []string{}
		for i, c := range row.Cells {
			name, val := cols[i].Value, w.sub(c.Value)
			switch {
			case name == "method":
				r.method = val
			case name == "path":
				r.target = val
			case name == "body":
				r.body = []byte(val)
			case strings.HasPrefix(name, "header:"):
				if h := strings.TrimPrefix(name, "header:"); strings.EqualFold(h, "Host") {
					r.host = val
				} else if val != "" {
					r.header.Set(h, val)
				}
			case strings.HasPrefix(name, "query:"):
				query = append(query, strings.TrimPrefix(name, "query:")+"="+queryEscape(val))
			case strings.HasPrefix(name, "cookie:"):
				r.cookie = append(r.cookie, [2]string{strings.TrimPrefix(name, "cookie:"), val})
			default:
				return fmt.Errorf("unknown column %q in the request table", name)
			}
		}
		if len(query) > 0 {
			sep := "?"
			if strings.Contains(r.target, "?") {
				sep = "&"
			}
			r.target += sep + strings.Join(query, "&")
		}
		if _, err := w.send(r); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) callAdmin(method, path, body string, header http.Header) error {
	rep, err := w.adminCall(method, path, body, header)
	if err == nil && method == "POST" && rep.status == 201 && (strings.HasSuffix(path, "/sessions/key-based") || strings.HasSuffix(path, "/sessions/url-based")) {
		// Remember the session this call created, for "the created session has …".
		err = json.Unmarshal(rep.body, &w.createdSession)
	}
	return err
}

// initSessionCalls registers steps that exercise session creation and mappings as a user would.
func (w *world) initSessionCalls(sc *godog.ScenarioContext) {
	sc.Step(`^I create a session mapping:$`, func(doc *godog.DocString) error {
		return w.postSessionMapping(doc.Content)
	})
	sc.Step(`^I create a session mapping with callResponses:$`, func(doc *godog.DocString) error {
		return w.postSessionMapping(`{"request":{"urlPath":"/rule"},"response":{"status":200},"callResponses":` + doc.Content + `}`)
	})
	sc.Step(`^I import into a key-based shadow mock:$`, func(doc *godog.DocString) error {
		if err := w.ensure(); err != nil {
			return err
		}
		if err := w.createSession("key-based", "IMPORT", "{}"); err != nil {
			return err
		}
		_, err := w.adminCall("POST", "/sessions/${IMPORT.id}/mappings/import", doc.Content, nil)
		return err
	})
	sc.Step(`^no mappings were imported$`, func() error {
		rep, err := w.mustAdmin("GET", "/sessions/${IMPORT.id}/mappings", "", 200)
		if err != nil {
			return err
		}
		if n, err := listLen(rep.body, "$.mappings"); err != nil || n != 0 {
			return fmt.Errorf("the session has %d mappings (%v), want 0", n, err)
		}
		return nil
	})
	sc.Step(`^the created session has (\d+) mappings$`, func(want int) error {
		id, hasID := w.createdSession["id"].(string)
		if !hasID {
			return errors.New("no session has been created")
		}
		if _, err := w.mustAdmin("GET", "/sessions/"+id, "", 200); err != nil {
			return err
		}
		v, _, err := w.jsonOne("$.mappingCount")
		if err != nil {
			return err
		}
		if text(v) != strconv.Itoa(want) {
			return fmt.Errorf("mappingCount = %s, want %d", text(v), want)
		}
		return nil
	})
	sc.Step(`^the created session has "([^"]*)" equal to "([^"]*)" and tenant "([^"]*)"$`, func(field, want, tenantID string) error {
		if text(w.createdSession[field]) != want || text(w.createdSession["tenant"]) != tenantID {
			return fmt.Errorf("created session has %s=%v tenant=%v, want %s / %s", field, w.createdSession[field], w.createdSession["tenant"], want, tenantID)
		}
		return nil
	})
	sc.Step(`^no session was created$`, func() error {
		rep, err := w.mustAdmin("GET", "/sessions", "", 200)
		if err != nil {
			return err
		}
		if n, err := listLen(rep.body, "$.sessions"); err != nil || n != 0 {
			return fmt.Errorf("%d sessions exist (%v), want none", n, err)
		}
		return nil
	})
	sc.Step(`^the response is the mapping list of "([^"]*)"$`, func(name string) error {
		got := w.last
		rep, err := w.mustAdmin("GET", "/sessions/${"+name+".id}/mappings", "", 200)
		if err != nil {
			return err
		}
		if !jsonEqual(got.body, rep.body) {
			return fmt.Errorf("the response is not the mapping list of %s:\n%s\nwant\n%s", name, truncate(got.body), truncate(rep.body))
		}
		return nil
	})
	sc.Step(`^I call admin "POST" "/sessions/key-based" with a TTL beyond the expiry of "([^"]*)" and parent "([^"]*)"$`, func(_, parent string) error {
		w.clk.advance(time.Hour) // the parent now has less than the key-based maximum left
		return w.callAdmin("POST", "/sessions/key-based", `{"ttlSeconds":86400,"parentId":"`+w.sub(parent)+`"}`, nil)
	})
	sc.Step(`^the session's expiresAt equals the expiry of "([^"]*)"$`, func(name string) error {
		child, _, err := w.jsonOne("$.expiresAt")
		if err != nil {
			return err
		}
		if _, getErr := w.mustAdmin("GET", "/sessions/${"+name+".id}", "", 200); getErr != nil {
			return getErr
		}
		parent, _, err := w.jsonOne("$.expiresAt")
		if err != nil {
			return err
		}
		if text(child) != text(parent) {
			return fmt.Errorf("child expires %v, parent %v", child, parent)
		}
		return nil
	})
	sc.Step(`^I generate (\d+) (session keys|URL-based mock ids|CI client secrets)$`, func(n int, kind string) error {
		w.genKeys, w.genKind = w.genKeys[:0], kind
		for range n {
			switch kind {
			case "session keys":
				w.genKeys = append(w.genKeys, ids.NewSessionKey(""))
			case "URL-based mock ids":
				w.genKeys = append(w.genKeys, ids.NewURLMockID(0))
			default:
				return fmt.Errorf("the engine does not mint %s", kind)
			}
		}
		return nil
	})
	sc.Step(`^each has at least (\d+) bits of randomness from a cryptographic source$`, func(bits int) error {
		return bitsAtLeast(w.genKeys, bits, w.genKind)
	})
	sc.Step(`^there are no collisions$`, func() error { return distinct(w.genKeys, len(w.genKeys)) })
}

// postSessionMapping creates a throwaway key-based session and posts a mapping to it.
func (w *world) postSessionMapping(mapping string) error {
	if err := w.ensure(); err != nil {
		return err
	}
	if err := w.createSession("key-based", "MAP", "{}"); err != nil {
		return err
	}
	_, err := w.adminCall("POST", "/sessions/${MAP.id}/mappings", mapping, nil)
	return err
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xb, _ := json.Marshal(x) //nolint:errcheck // re-encoding a parsed value
	yb, _ := json.Marshal(y) //nolint:errcheck // re-encoding a parsed value
	return bytes.Equal(xb, yb)
}
