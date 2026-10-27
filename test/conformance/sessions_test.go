package conformance_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/test/contract"
)

// corpusFile reads a file under corpus/shadow-mock.
func corpusFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contract.RepoRoot(), "corpus", "shadow-mock", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read corpus file %s: %v", rel, err)
	}
	return string(b)
}

// session is a created key-based or URL-based shadow mock.
type session struct {
	id, key, baseURL string
}

// path returns baseURL's path, to send traffic through a URL-based shadow mock.
func (s session) path(t *testing.T, rest string) string {
	t.Helper()
	u, err := url.Parse(s.baseURL)
	if err != nil {
		t.Fatalf("parse %q: %v", s.baseURL, err)
	}
	return u.Path + rest
}

// create posts a session payload and fails the test unless it is accepted.
func (e *env) create(kind, payload string) session {
	e.t.Helper()
	r, v := e.admin("POST", "/sessions/"+kind, payload)
	if r.status != 201 {
		e.t.Fatalf("create %s session: %d %s", kind, r.status, r.body)
	}
	s := session{}
	s.id, _ = v["id"].(string)
	s.key, _ = v["key"].(string)
	s.baseURL, _ = v["baseUrl"].(string)
	return s
}

// payloadMappings returns the mappings inside a session payload.
func payloadMappings(t *testing.T, payload string) []mapping {
	t.Helper()
	var doc struct {
		Mappings []map[string]any `json:"mappings"`
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatal(err)
	}
	out := make([]mapping, 0, len(doc.Mappings))
	for _, m := range doc.Mappings {
		x := mapping{}
		x.ID, _ = m["id"].(string)
		x.Request, _ = m["request"].(map[string]any)
		x.Response, _ = m["response"].(map[string]any)
		out = append(out, x)
	}
	return out
}

// servedBy sends a request and checks the mapping that served it and its response.
func (e *env) servedBy(name string, c call, m mapping) {
	e.t.Helper()
	r := e.mustSend(c)
	if id, matched := e.latest(); !matched || id != m.ID {
		e.t.Errorf("%s: served by %q (matched %v), want %s", name, id, matched, m.ID)
		return
	}
	e.expectResponse(name, m.Response, r, 0)
}

// notServed sends a request and checks that no mapping served it.
func (e *env) notServed(name string, c call) {
	e.t.Helper()
	r := e.mustSend(c)
	if id, matched := e.latest(); matched || r.status != 404 {
		e.t.Errorf("%s: status %d, served by %q; want an unmatched 404", name, r.status, id)
	}
}

func keyHdr(s session) map[string][]string { return hdr("X-Shadow-Mock-Key", s.key) }

func TestKeyBasedShadowMock(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	e := start(t, "shadow-mock")
	ms := loadMappings(t, "shadow-mock")
	globalPayments, _ := find(ms, "5d03000101")
	globalCustomer, _ := find(ms, "5d03000102")
	payload := corpusFile(t, "sessions/key-based/02-checkout-declined.json")
	declined, creditCheck := payloadMappings(t, payload)[0], payloadMappings(t, payload)[1]
	s := e.create("key-based", payload)
	pay := func(kv map[string][]string, body string) call {
		return call{method: "POST", target: "/sm/payments", header: kv, body: body}
	}

	t.Run("the key in the designated header selects the session", func(t *testing.T) {
		e.with(t).servedBy("header", pay(keyHdr(s), ""), declined)
	})
	t.Run("without the key the global mapping answers", func(t *testing.T) {
		e.with(t).servedBy("global", pay(nil, ""), globalPayments)
	})
	t.Run("the key anywhere in the body selects the session", func(t *testing.T) {
		se := e.with(t)
		se.servedBy("body", pay(hdr("Content-Type", "application/json"), fmt.Sprintf(`{ "testRun": %q, "amount": 10 }`, s.key)), declined)
	})
	t.Run("a match token in the body selects the session", func(t *testing.T) {
		e.with(t).servedBy("token", call{
			method: "POST", target: "/sm/credit-check", header: hdr("Content-Type", "application/json"),
			body: `{ "customerId": "CUST-99812", "amount": 500 }`,
		}, creditCheck)
	})
	t.Run("a request the session does not mock falls through to global", func(t *testing.T) {
		se := e.with(t)
		r := se.mustSend(call{method: "GET", target: "/sm/customers/C-1", header: keyHdr(s)})
		if id, _ := se.latest(); id != globalCustomer.ID || r.status != 200 {
			t.Errorf("served by %s with %d, want the global mapping %s", id, r.status, globalCustomer.ID)
		}
	})
	t.Run("a session mapping with call counts counts only this session's calls", func(t *testing.T) {
		se := e.with(t)
		file := corpusFile(t, "sessions/key-based/06-session-mapping-with-call-counts.json")
		if r, _ := se.admin("POST", "/sessions/"+s.id+"/mappings", file); r.status != 201 {
			t.Fatalf("add the session mapping: %d %s", r.status, r.body)
		}
		var m mapping
		var raw map[string]any
		if err := json.Unmarshal([]byte(file), &raw); err != nil {
			t.Fatal(err)
		}
		m.ID, _ = raw["id"].(string)
		m.Response, _ = raw["response"].(map[string]any)
		for _, r := range raw["callResponses"].([]any) {
			m.CallResponses = append(m.CallResponses, r.(map[string]any))
		}
		for n := 1; n <= 4; n++ {
			r := se.mustSend(pay(keyHdr(s), ""))
			if id, _ := se.latest(); id != m.ID {
				t.Fatalf("call %d served by %s, want %s", n, id, m.ID)
			}
			se.expectResponse(fmt.Sprintf("call %d", n), callRuleFor(m, n), r, 0)
		}
		_, list := se.admin("GET", "/sessions/"+s.id+"/counters", "")
		if got := counterOf(list, m.ID); got != 4 {
			t.Errorf("session counter = %d, want 4", got)
		}
		// Global traffic is unaffected: the global mapping has no call rules and answers as before.
		se.servedBy("global after session calls", pay(nil, ""), globalPayments)
	})
	t.Run("the session journal holds only its own requests", func(t *testing.T) {
		se := e.with(t)
		_, v := se.admin("POST", "/sessions/"+s.id+"/requests/count", `{ "method": "POST", "urlPath": "/sm/payments" }`)
		// header + body + four counted calls; the two requests without the key are global.
		if n, _ := v["count"].(float64); int(n) != 6 {
			t.Errorf("session count = %v, want 6", v["count"])
		}
	})
	t.Run("a session file overrides a global file", func(t *testing.T) {
		se := e.with(t)
		file := corpusFile(t, "sessions/files/claim-from-file.json")
		if r := se.mustSend(call{
			method: "PUT", target: "/__admin/sessions/" + s.id + "/files/session/claim-from-file.json", body: file,
			header: hdr("Content-Type", "application/octet-stream"),
		}); r.status != 200 {
			t.Fatalf("put the session file: %d", r.status)
		}
		got := se.mustSend(call{method: "GET", target: "/__admin/sessions/" + s.id + "/files/session/claim-from-file.json"})
		if string(got.body) != file {
			t.Errorf("session file = %q, want the uploaded content", got.body)
		}
	})
	t.Run("tokens, masks and the lifetime can be patched", func(t *testing.T) {
		se := e.with(t)
		r, v := se.admin("PATCH", "/sessions/"+s.id, corpusFile(t, "sessions/key-based/patch-add-tokens.json"))
		if toks, _ := v["tokens"].([]any); r.status != 200 || !containsAny(toks, "POL-555123") {
			t.Errorf("add tokens: %d %v", r.status, v["tokens"])
		}
		r, v = se.admin("PATCH", "/sessions/"+s.id, corpusFile(t, "sessions/key-based/patch-add-masks-and-extend.json"))
		if r.status != 200 || v["name"] != "renamed-session" {
			t.Errorf("masks and extend: %d %v", r.status, v["name"])
		}
		if r, _ := se.admin("POST", "/sessions/"+s.id+"/keepalive", ""); r.status != 200 {
			t.Errorf("keepalive: %d", r.status)
		}
	})
	t.Run("deleting the session sends its traffic back to global", func(t *testing.T) {
		se := e.with(t)
		if r, _ := se.admin("DELETE", "/sessions/"+s.id, ""); r.status != 200 {
			t.Fatalf("delete: %d", r.status)
		}
		se.servedBy("after delete", pay(keyHdr(s), ""), globalPayments)
	})
}

func containsAny(xs []any, want string) bool {
	for _, x := range xs {
		if fmt.Sprint(x) == want {
			return true
		}
	}
	return false
}

func TestOverrideAndMasks(t *testing.T) {
	// The subtests read "the latest request" from one shared journal, so they run in order.
	e := start(t, "shadow-mock")
	ms := loadMappings(t, "shadow-mock")
	globalPayments, _ := find(ms, "5d03000101")
	catchAll, _ := find(ms, "5d03000105")
	customer, _ := find(ms, "5d03000102")

	t.Run("a session mapping with a global id hides it, even when it does not match", func(t *testing.T) {
		se := e.with(t)
		payload := corpusFile(t, "sessions/key-based/03-override-by-id.json")
		override := payloadMappings(t, payload)[0]
		s := se.create("key-based", payload)
		vip := call{method: "POST", target: "/sm/payments", header: map[string][]string{
			"X-Shadow-Mock-Key": {s.key}, "Content-Type": {"application/json"},
		}, body: `{ "tier": "vip" }`}
		se.servedBy("vip", vip, override)
		standard := vip
		standard.body = `{ "tier": "standard" }`
		se.notServed("standard customer: the global mapping is hidden", standard)
		se.servedBy("no key", call{method: "POST", target: "/sm/payments"}, globalPayments)
	})
	t.Run("masks hide global mappings by id and by pattern", func(t *testing.T) {
		se := e.with(t)
		s := se.create("key-based", corpusFile(t, "sessions/key-based/04-masks.json"))
		se.servedBy("masked rate: the catch-all answers", call{method: "GET", target: "/sm/fx/rates", header: keyHdr(s)}, catchAll)
		se.notServed("customers are masked by pattern", call{method: "GET", target: "/sm/customers/C-1", header: keyHdr(s)})
		r := se.mustSend(call{method: "GET", target: "/sm/customers/C-1"})
		if id, _ := se.latest(); id != customer.ID || r.status != 200 {
			t.Errorf("without the key: served by %s with %d, want %s", id, r.status, customer.ID)
		}
	})
}

func TestURLBasedShadowMock(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	e := start(t, "shadow-mock")
	ms := loadMappings(t, "shadow-mock")
	globalPolicy, _ := find(ms, "5d03000106")
	payload := corpusFile(t, "sessions/url-based/02-claims-api-ci.json")
	policy9 := payloadMappings(t, payload)[0]
	fraud := payloadMappings(t, payload)[1]
	u := e.create("url-based", payload)

	t.Run("the URL insert selects the shadow mock", func(t *testing.T) {
		e.with(t).servedBy("policy 9", call{method: "GET", target: u.path(t, "/sm/policies/9")}, policy9)
	})
	t.Run("a request it does not mock falls through to global", func(t *testing.T) {
		se := e.with(t)
		r := se.mustSend(call{method: "GET", target: u.path(t, "/sm/policies/10")})
		if id, _ := se.latest(); id != globalPolicy.ID || r.status != 200 {
			t.Errorf("served by %s with %d, want %s", id, r.status, globalPolicy.ID)
		}
	})
	t.Run("call counts belong to the shadow mock", func(t *testing.T) {
		se := e.with(t)
		fraudCalls := mapping{ID: fraud.ID, Response: fraud.Response}
		var raw map[string]any
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			t.Fatal(err)
		}
		for _, m := range raw["mappings"].([]any) {
			mm := m.(map[string]any)
			if mm["id"] == fraud.ID {
				for _, r := range mm["callResponses"].([]any) {
					fraudCalls.CallResponses = append(fraudCalls.CallResponses, r.(map[string]any))
				}
			}
		}
		for n := 1; n <= 3; n++ {
			r := se.mustSend(call{method: "POST", target: u.path(t, "/sm/fraud/score")})
			se.expectResponse(fmt.Sprintf("call %d", n), callRuleFor(fraudCalls, n), r, 0)
		}
	})
	t.Run("a key-based shadow mock nested inside it", func(t *testing.T) {
		se := e.with(t)
		nestedPayload := strings.ReplaceAll(corpusFile(t, "sessions/url-based/03-nested-key-based.json"), "${URL_MOCK_ID}", u.id)
		nestedMapping := payloadMappings(t, nestedPayload)[0]
		k := se.create("key-based", nestedPayload)
		se.servedBy("through the parent with the key", call{method: "GET", target: u.path(t, "/sm/policies/9"), header: keyHdr(k)}, nestedMapping)
		se.servedBy("through the parent without the key", call{method: "GET", target: u.path(t, "/sm/policies/9")}, policy9)
		r := se.mustSend(call{method: "GET", target: "/sm/policies/9", header: keyHdr(k)})
		if id, _ := se.latest(); id != globalPolicy.ID || r.status != 200 {
			t.Errorf("the nested key outside its parent: served by %s, want the global mapping %s", id, globalPolicy.ID)
		}
	})
	t.Run("an unknown id is a 404 and never falls back to global", func(t *testing.T) {
		r := e.with(t).mustSend(call{method: "GET", target: "/d/aaaaaaaaaaaaaaaa/sm/policies/9"})
		if r.status != 404 || !strings.Contains(string(r.body), "unknown_url_based_mock") {
			t.Errorf("got %d %s", r.status, r.body)
		}
	})
}

// sessionFiles says how each session payload file in the corpus is used. A file not listed
// here fails the test, so new payloads cannot be added untested.
var sessionFiles = map[string]string{
	"key-based/01-minimal.json":                          "create",
	"key-based/02-checkout-declined.json":                "create",
	"key-based/03-override-by-id.json":                   "create",
	"key-based/04-masks.json":                            "create",
	"key-based/05-seed-from-mapping-set.json":            "create",
	"key-based/06-session-mapping-with-call-counts.json": "mapping",
	"key-based/patch-add-tokens.json":                    "patch",
	"key-based/patch-add-masks-and-extend.json":          "patch",
	"url-based/01-minimal.json":                          "create-url",
	"url-based/02-claims-api-ci.json":                    "create-url",
	"url-based/03-nested-key-based.json":                 "nested",
	"counters/set-counter.json":                          "covered by TestCallCountResponses",
	"files/claim-from-file.json":                         "covered by TestKeyBasedShadowMock",
	"runtime/dry-run.json":                               "dry-run",
	"runtime/validate-mapping.json":                      "validate",
	"recording/record-into-session.json":                 "skip: recording is P3",
}

func TestSessionPayloadFiles(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	e := start(t, "shadow-mock")
	dir := filepath.Join(contract.RepoRoot(), "corpus", "shadow-mock", "sessions")
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if _, ok := sessionFiles[filepath.ToSlash(rel)]; !ok {
			t.Errorf("session payload %s has no conformance check; add it to sessionFiles", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	base := e.create("key-based", "{}")
	parent := e.create("url-based", "{}")
	declined := e.create("key-based", corpusFile(t, "sessions/key-based/02-checkout-declined.json"))

	for rel, how := range sessionFiles {
		t.Run(rel, func(t *testing.T) {
			se := e.with(t)
			body := corpusFile(t, "sessions/"+rel)
			switch how {
			case "create":
				if rel == "key-based/02-checkout-declined.json" {
					return // created above; its tokens are unique
				}
				se.create("key-based", body)
			case "create-url":
				se.create("url-based", body)
			case "nested":
				se.create("key-based", strings.ReplaceAll(body, "${URL_MOCK_ID}", parent.id))
			case "mapping":
				if r, _ := se.admin("POST", "/sessions/"+base.id+"/mappings", body); r.status != 201 {
					t.Errorf("%d %s", r.status, r.body)
				}
			case "patch":
				if r, _ := se.admin("PATCH", "/sessions/"+base.id, body); r.status != 200 {
					t.Errorf("%d %s", r.status, r.body)
				}
			case "validate":
				r, v := se.admin("POST", "/mappings/validate", body)
				if r.status != 200 || v["valid"] != true {
					t.Errorf("%d %s", r.status, r.body)
				}
			case "dry-run":
				r, v := se.admin("POST", "/mappings/dry-run", strings.ReplaceAll(body, "${SESSION_ID}", declined.id))
				resp, _ := v["response"].(map[string]any)
				if r.status != 200 || v["matched"] != true || v["layer"] != "key-based" || resp["status"] != float64(402) {
					t.Errorf("dry run: %d %s", r.status, r.body)
				}
			default:
				if strings.HasPrefix(how, "skip") {
					t.Skip(how)
				}
			}
		})
	}
}

func TestNegativeCorpus(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	e := start(t, "shadow-mock")

	// rejected checks that the engine refuses a mapping body with a 422 in either error format.
	rejected := func(t *testing.T, se *env, path, body, wantCode string) {
		t.Helper()
		r, v := se.admin("POST", path, body)
		if r.status != 422 {
			t.Errorf("status = %d, want 422; body: %s", r.status, r.body)
			return
		}
		_, hasErrors := v["errors"]
		switch {
		case wantCode == "" || wantCode == "validation_failed":
			if !hasErrors && v["error"] != "validation_failed" {
				t.Errorf("want validation_failed or a WireMock errors list, got %s", r.body)
			}
		case v["error"] != wantCode:
			t.Errorf("error = %v, want %s", v["error"], wantCode)
		}
	}
	cases := func(rel string) []map[string]any {
		var doc struct {
			Cases []map[string]any `json:"cases"`
		}
		if err := json.Unmarshal([]byte(corpusFile(t, rel)), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Cases
	}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}

	t.Run("scenario fields are rejected", func(t *testing.T) {
		for _, f := range []string{"negative/scenarios/all-scenario-fields.json", "negative/scenarios/scenario-name-only.json"} {
			var doc map[string]any
			if err := json.Unmarshal([]byte(corpusFile(t, f)), &doc); err != nil {
				t.Fatal(err)
			}
			rejected(t, e.with(t), "/mappings", enc(doc["mapping"]), "scenarios_not_supported")
		}
	})
	t.Run("an import with a scenario mapping applies nothing", func(t *testing.T) {
		se := e.with(t)
		var doc map[string]any
		if err := json.Unmarshal([]byte(corpusFile(t, "negative/scenarios/import-with-one-scenario-mapping.json")), &doc); err != nil {
			t.Fatal(err)
		}
		rejected(t, se, "/mappings/import", enc(doc["import"]), "scenarios_not_supported")
		se.notServed("the valid mapping of the import", call{method: "GET", target: "/sm/neg/import/ok"})
	})
	t.Run("invalid mappings are rejected", func(t *testing.T) {
		for _, c := range cases("negative/mappings/invalid-mappings.json") {
			t.Run(fmt.Sprint(c["why"]), func(t *testing.T) { rejected(t, e.with(t), "/mappings", enc(c["mapping"]), "") })
		}
	})
	t.Run("invalid call rules are rejected", func(t *testing.T) {
		for _, c := range cases("negative/call-responses/invalid-rules.json") {
			t.Run(fmt.Sprint(c["why"]), func(t *testing.T) { rejected(t, e.with(t), "/mappings", enc(c["mapping"]), "validation_failed") })
		}
	})
	t.Run("invalid session payloads are rejected with the documented error", func(t *testing.T) {
		se := e.with(t)
		se.create("key-based", corpusFile(t, "sessions/key-based/02-checkout-declined.json")) // the token_in_use precondition
		for _, c := range cases("negative/sessions/invalid-session-payloads.json") {
			t.Run(fmt.Sprint(c["error"]), func(t *testing.T) {
				path := strings.TrimPrefix(fmt.Sprint(c["endpoint"]), "/__admin")
				r, v := se.with(t).admin("POST", path, enc(c["payload"]))
				if float64(r.status) != c["status"] || v["error"] != c["error"] {
					t.Errorf("got %d %v, want %v %v; body: %s", r.status, v["error"], c["status"], c["error"], r.body)
				}
			})
		}
	})
}
