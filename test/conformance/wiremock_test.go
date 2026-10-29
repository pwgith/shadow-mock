package conformance_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// tc is one corpus case: a request, and the mapping that must serve it.
type tc struct {
	// want is the short id (first 8 and last 2 characters) of the mapping that must serve the
	// request. Empty means no mapping may serve it and the engine must answer with the 404
	// diagnostic.
	want string
	call call
	// status replaces the status in the mapping file, for a response the engine changes (a
	// missing body file is a 500 whatever the mapping says). The body is then not compared.
	status int
	// fails means the connection is broken on purpose (a fault).
	fails bool
	// contains lists text the 404 diagnostic must include.
	contains []string
	// body is the text a templated response must produce. A JSON body is compared as JSON, so
	// spacing does not matter; anything else is compared exactly after trimming.
	body string
}

// hdr builds headers from name, value pairs; a repeated name adds another value.
func hdr(kv ...string) map[string][]string {
	h := map[string][]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		h[kv[i]] = append(h[kv[i]], kv[i+1])
	}
	return h
}

func get(target string, kv ...string) call {
	return call{method: "GET", target: target, header: hdr(kv...)}
}

func post(target, body string, kv ...string) call {
	return call{method: "POST", target: target, body: body, header: hdr(kv...)}
}

const (
	jsonCT = "Content-Type"
	appJS  = "application/json"
)

// multipartBody builds a body with the boundary "corpus".
func multipartBody(parts ...string) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString("--corpus\r\n" + p + "\r\n")
	}
	b.WriteString("--corpus--\r\n")
	return b.String()
}

const mpType = "multipart/form-data; boundary=corpus"

// wiremockCases covers corpus/wiremock/mappings.
func wiremockCases() []tc {
	return slices.Concat(urlAndMethodCases(), attributeCases(), valueCases(), bodyCases(), responseCases(), miscCases(), shipmentCases(), parityCases())
}

func urlAndMethodCases() []tc {
	return []tc{
		// 01 URL matching
		{want: "c001000101", call: get("/wm/url/exact?status=OPEN&page=1")},
		{want: "c001000102", call: get("/wm/url/path?anything=goes")},
		{want: "c001000103", call: get("/wm/url/pattern/42?page=3")},
		{want: "c001000104", call: get("/wm/url/path-pattern/motor-claims")},
		{want: "c001000105", call: get("/wm/url/claims/42/notes/n-7")},
		{want: "c001000106", call: get("/wm/url/CaseSensitive")},
		{want: "c001000107", call: get("/wm/url/files/my%20doc.pdf")},
		{call: get("/wm/url/exact?status=CLOSED&page=1"), contains: []string{"Request was not matched", "URL"}},
		{call: get("/wm/url/casesensitive")},
		{call: get("/wm/url/claims/42/notes/draft-1"), contains: []string{"Path parameter [noteId]"}},
		// 02 methods
		{want: "c002000101", call: get("/wm/methods/get")},
		{want: "c002000102", call: post("/wm/methods/post", "")},
		{want: "c002000103", call: call{method: "PUT", target: "/wm/methods/put"}},
		{want: "c002000104", call: call{method: "PATCH", target: "/wm/methods/patch"}},
		{want: "c002000105", call: call{method: "DELETE", target: "/wm/methods/delete"}},
		{want: "c002000106", call: call{method: "HEAD", target: "/wm/methods/head"}},
		{want: "c002000107", call: call{method: "OPTIONS", target: "/wm/methods/options"}},
		{want: "c002000108", call: call{method: "DELETE", target: "/wm/methods/any"}},
		{want: "c002000109", call: call{method: "PATCH", target: "/wm/methods/unspecified"}},
		{call: post("/wm/methods/get", "")},
	}
}

func attributeCases() []tc {
	return []tc{
		// 03 headers
		{want: "c003000101", call: get("/wm/headers/tenant", "x-tenant", "acme")},
		{want: "c003000102", call: post("/wm/headers/all", "{}", jsonCT, "application/json; charset=utf-8",
			"Accept", "application/json", "X-Request-Id", "3f2b8c1e-1d2a-4b9e-9a77-0c3d2e1f4a5b")},
		{want: "c003000103", call: get("/wm/headers/anonymous")},
		{want: "c003000104", call: get("/wm/headers/authenticated", "Authorization", "Bearer abc")},
		{want: "c003000105", call: get("/wm/headers/multi", "X-Role", "reader", "X-Role", "writer")},
		{call: get("/wm/headers/multi", "X-Role", "reader", "X-Role", "writer", "X-Role", "admin")},
		// 03 query parameters
		{want: "c003000201", call: get("/wm/query/search?page=2&status=OPEN")},
		{want: "c003000202", call: get("/wm/query/exactly?id=1&id=2")},
		{want: "c003000203", call: get("/wm/query/includes?id=1&id=95&id=3")},
		{want: "c003000204", call: get("/wm/query/no-debug")},
		{call: get("/wm/query/exactly?id=1&id=2&id=3"), contains: []string{"Query parameter [id]"}},
		// 03 cookies, forms, basic auth
		{want: "c003000301", call: get("/wm/cookies/session", "Cookie", "session=s-abc; locale=en-AU")},
		{want: "c003000302", call: post("/wm/forms/token", "grant_type=client_credentials&scope=claims:read%20mocks:write",
			jsonCT, "application/x-www-form-urlencoded")},
		{want: "c003000303", call: get("/wm/auth/basic", "Authorization", "Basic c3ZjLWNsYWltczpzM2NyZXQ=")},
		{want: "c003000304", call: get("/wm/auth/basic", "Authorization", "Basic d3Jvbmc6d3Jvbmc=")},
		// 03 host
		{want: "c003000401", call: get("/wm/host/info", "Host", "payments.internal")},
		{want: "c003000402", call: get("/wm/host/info", "Host", "app.claims.internal")},
	}
}

func valueCases() []tc {
	return []tc{
		// 04 string operators
		{want: "c004000101", call: get("/wm/values/equal-to", "X-Value", "abc")},
		{want: "c004000102", call: get("/wm/values/equal-to-ci", "X-Value", "ABC")},
		{want: "c004000103", call: get("/wm/values/contains", "X-Value", "abcd")},
		{want: "c004000104", call: get("/wm/values/does-not-contain", "X-Value", "plain")},
		{want: "c004000105", call: get("/wm/values/matches", "X-Value", "ABC-1234")},
		{want: "c004000106", call: get("/wm/values/does-not-match", "X-Value", "prod-1")},
		{want: "c004000107", call: get("/wm/values/absent")},
		{call: get("/wm/values/equal-to", "X-Value", "ABC")},
		// 04 logic
		{want: "c004000201", call: get("/wm/logic/and", "X-Value", "mockery")},
		{want: "c004000202", call: get("/wm/logic/or?region=NZ")},
		{want: "c004000203", call: get("/wm/logic/not", "X-Env", "staging")},
		{want: "c004000204", call: get("/wm/logic/nested", "X-Value", "gold-member-active")},
		// 04 dates, against the engine's fixed time of 2026-10-27T12:00:00Z
		{want: "c004000301", call: get("/wm/dates/before", "X-Date", "2025-12-31T00:00:00Z")},
		{want: "c004000302", call: get("/wm/dates/recent", "X-Date", "2026-10-26T09:00:00Z")},
		{want: "c004000303", call: get("/wm/dates/booking?date=2026-10-28")},
		{want: "c004000304", call: get("/wm/dates/exact", "X-Date", "2026-06-24T00:00:00Z")},
		{want: "c004000305", call: get("/wm/dates/formatted", "X-Date", "31/12/2025")},
		{want: "c004000306", call: get("/wm/dates/this-month", "X-Date", "2026-10-15T08:00:00Z")},
		{call: get("/wm/dates/recent", "X-Date", "2026-10-01T09:00:00Z")},
	}
}

func bodyCases() []tc {
	j := func(path, body string) call { return post(path, body, jsonCT, appJS) }
	return []tc{
		// 05 JSON bodies
		{want: "c005000101", call: j("/wm/json/strict", `{ "tags": ["a", "b"], "id": 1 }`)},
		{want: "c005000102", call: j("/wm/json/array-order", `{ "tags": ["c", "a", "b"] }`)},
		{want: "c005000103", call: j("/wm/json/extra", `{ "customer": { "id": "C-1", "name": "Ada" }, "other": 1 }`)},
		{want: "c005000104", call: j("/wm/json/string-form", `{ "active": true }`)},
		{want: "c005000105", call: j("/wm/json/placeholders", `{ "id": 7, "name": "Ada", "active": true, "ref": "ABC-123", "note": null }`)},
		{want: "c005000201", call: j("/wm/jsonpath/present", `{ "claim": { "id": 5 } }`)},
		{want: "c005000202", call: j("/wm/jsonpath/filter", `{ "items": [ { "sku": "A1", "qty": 2 }, { "sku": "B1", "qty": 9 } ] }`)},
		{want: "c005000203", call: j("/wm/jsonpath/equal", `{ "claim": { "status": "OPEN" } }`)},
		{want: "c005000204", call: j("/wm/jsonpath/regex", `{ "customer": { "email": "ada@example.com" } }`)},
		{want: "c005000205", call: j("/wm/jsonpath/subtree", `{ "address": { "postcode": "2000", "country": "AU", "street": "1 George St" } }`)},
		{want: "c005000206", call: j("/wm/jsonpath/no-discount", `{ "total": 10 }`)},
		{want: "c005000208", call: j("/wm/jsonschema/claim", `{ "id": 3, "amount": 120.5, "currency": "AUD" }`)},
		{want: "c005000209", call: j("/wm/json/all-patterns", `{ "a": 1, "b": 2 }`)},
		{call: j("/wm/jsonschema/claim", `{ "id": 3, "amount": -1 }`)},
		{call: j("/wm/json/all-patterns", `{ "a": 1, "b": 2, "test": true }`)},
		{call: j("/wm/json/strict", `{ "id": 1, "tags": ["b", "a"] }`)},
		// 06 XML bodies
		{want: "c006000101", call: post("/wm/xml/equal", "<claim type=\"motor\" id=\"1\">\n  <status>OPEN</status>\n</claim>", jsonCT, "application/xml")},
		{want: "c006000102", call: post("/wm/xml/placeholders", "<claim><id>9</id><lodged>2026-11-01T09:30:00Z</lodged><amount>12.5</amount></claim>", jsonCT, "application/xml")},
		{want: "c006000103", call: post("/wm/xml/custom-delimiters", "<claim><id>anything</id></claim>", jsonCT, "application/xml")},
		{want: "c006000104", call: post("/wm/xml/exempted", "<b:claim xmlns:b=\"urn:claims\"><b:id>1</b:id></b:claim>", jsonCT, "application/xml")},
		{want: "c006000105", call: post("/wm/xpath/item", "<items><item sku=\"A1\" qty=\"3\"/></items>", jsonCT, "application/xml")},
		{want: "c006000106", call: post("/wm/xpath/value", "<claim><status>OPEN</status></claim>", jsonCT, "application/xml")},
		{want: "c006000107", call: post("/wm/soap/claims", "<s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\"><s:Body><k:GetClaim xmlns:k=\"urn:claims\"><k:ClaimId>42</k:ClaimId></k:GetClaim></s:Body></s:Envelope>", jsonCT, "text/xml", "SOAPAction", "urn:claims/GetClaim")},
		// 07 text, binary and multipart bodies
		{want: "c007000101", call: post("/wm/text/equal", "hello world")},
		{want: "c007000102", call: post("/wm/text/equal-ci", "Hello World")},
		{want: "c007000103", call: post("/wm/text/contains", "this is urgent, please")},
		{want: "c007000104", call: post("/wm/text/matches", "BEGIN\nmiddle\nEND")},
		{want: "c007000105", call: post("/wm/text/empty", "")},
		{want: "c007000106", call: post("/wm/binary/equal", "\x01\x02\x03", jsonCT, "application/octet-stream")},
		{want: "c007000107", call: post("/wm/multipart/any", multipartBody(
			"Content-Disposition: form-data; name=\"document\"; filename=\"a.pdf\"\r\nContent-Type: application/pdf\r\n\r\n%PDF-1.7 fake",
			"Content-Disposition: form-data; name=\"note\"\r\n\r\nhello"), jsonCT, mpType)},
		{want: "c007000108", call: post("/wm/multipart/all", multipartBody(
			"Content-Disposition: form-data; name=\"a\"\r\n\r\n1",
			"Content-Disposition: form-data; name=\"b\"\r\n\r\n2"), jsonCT, mpType)},
		{want: "c007000109", call: post("/wm/multipart/claim", multipartBody(
			"Content-Disposition: form-data; name=\"metadata\"\r\nContent-Type: application/json\r\n\r\n{\"claimId\":1}",
			"Content-Disposition: form-data; name=\"photo\"; filename=\"p.png\"\r\nContent-Type: image/png\r\n\r\nPNGDATA"), jsonCT, mpType)},
		{call: post("/wm/multipart/claim", multipartBody(
			"Content-Disposition: form-data; name=\"metadata\"\r\nContent-Type: application/json\r\n\r\n{\"claimId\":1}"), jsonCT, mpType)},
		{call: post("/wm/text/equal", "hello"), contains: []string{"Body"}},
	}
}

func responseCases() []tc {
	return []tc{
		// 08 priority
		{want: "c008000102", call: get("/wm/priority/claims/42")},
		{want: "c008000103", call: get("/wm/priority/claims/99")},
		{want: "c008000104", call: get("/wm/priority/claims/7")},
		{want: "c008000103", call: get("/wm/priority/claims/8")}, // priority 6 loses to the default 5
		{want: "c008000101", call: get("/wm/priority/other")},
		// 09 responses
		{want: "c009000101", call: get("/wm/responses/teapot")},
		{want: "c009000102", call: get("/wm/responses/json")},
		{want: "c009000103", call: get("/wm/responses/base64")},
		{want: "c009000104", call: get("/wm/responses/cookies")},
		{want: "c009000105", call: call{method: "DELETE", target: "/wm/responses/claims/12"}},
		{want: "c009000106", call: get("/wm/responses/old-location")},
		{want: "c009000301", call: get("/wm/responses/large")},
		{want: "c009000302", call: post("/wm/responses/large-upload", largeUpload(), jsonCT, appJS)},
		{want: "c009000107", call: post("/wm/responses/broken", "")},
		// 09 body files
		{want: "c009000201", call: get("/wm/files/claim")},
		{want: "c009000202", call: get("/wm/files/policy.xml")},
		{want: "c009000203", call: get("/wm/files/terms")},
		{want: "c009000204", call: get("/wm/files/export.csv")},
		{want: "c009000205", call: get("/wm/files/missing"), status: 500}, // the file does not exist
		// 10 templating: matched and answered with the right status; the unit and acceptance tests check the rendered text
		{want: "c010000101", call: get("/wm/template/echo/a/b?status=OPEN")},
		{want: "c010000102", call: get("/wm/template/customers/1/orders/2")},
		{want: "c010000103", call: post("/wm/template/raw", "raw")},
		{want: "c010000104", call: get("/wm/template/parameters")},
		{want: "c010000105", call: get("/wm/template/claims/42")},
		{want: "c010000106", call: post("/wm/template/orders", `{ "customer": "Ada" }`, jsonCT, appJS)},
		{want: "c010000107", call: get("/wm/template/literal")}, // not templated: served as written
		{want: "c010000201", call: get("/wm/template/dates", "X-Date", "15/10/2026")},
		{want: "c010000202", call: get("/wm/template/random")},
		{want: "c010000203", call: get("/wm/template/strings?qty=4&name=ada&ref=ORD-778", "X-Padded", "  padded  ")},
		{want: "c010000204", call: post("/wm/template/regex-groups", "ORD-778")},
		{want: "c010000205", call: get("/wm/template/logic?plan=gold&code=123")},
		{want: "c010000206", call: post("/wm/template/form", "firstName=Ada%20L&lastName=Lovelace", jsonCT, "application/x-www-form-urlencoded")},
		{want: "c010000301", call: post("/wm/template/json-path", `{ "customer": { "name": "Ada" } }`, jsonCT, appJS)},
		{want: "c010000302", call: post("/wm/template/order-lines", `{ "items": [] }`, jsonCT, appJS)},
		{want: "c010000303", call: post("/wm/template/json-transform", jsonTransformBody, jsonCT, appJS)},
		{want: "c010000304", call: post("/wm/template/xpath", "<claim/>", jsonCT, "application/xml")},
		{want: "c010000305", call: post("/wm/template/soap", "<e/>", jsonCT, "text/xml")},
	}
}

func miscCases() []tc {
	return []tc{
		// 11 delays (the engine's clock is fake, so they complete at once)
		{want: "c011000101", call: get("/wm/delay/fixed")},
		{want: "c011000102", call: get("/wm/delay/uniform")},
		{want: "c011000103", call: get("/wm/delay/lognormal")},
		{want: "c011000104", call: get("/wm/delay/lognormal-capped")},
		{want: "c011000105", call: get("/wm/delay/dribble")},
		{want: "c011000106", call: get("/wm/delay/timeout")},
		// 12 faults
		{want: "c012000101", call: get("/wm/fault/reset"), fails: true},
		{want: "c012000102", call: get("/wm/fault/empty"), fails: true},
		{want: "c012000103", call: get("/wm/fault/malformed"), fails: true},
		{want: "c012000104", call: get("/wm/fault/garbage"), fails: true},
		// 13 proxying: only the stubbed decline needs no upstream
		{want: "c013000102", call: post("/wm/proxy/payments/declined", "")},
		// 14 webhooks: the response is served; the webhook call itself is P3
		{want: "c014000101", call: post("/wm/webhooks/payments", `{ "ref": "P-77" }`, jsonCT, appJS)},
		{want: "c014000102", call: post("/wm/webhooks/delayed", "")},
		{want: "c014000103", call: post("/wm/webhooks/random-delay", "")},
		{want: "c014000104", call: post("/wm/webhooks/orders/9", "")},
		{want: "c014000105", call: post("/wm/webhooks/legacy", "")},
		// 15 metadata and near misses
		{want: "c015000101", call: get("/wm/metadata/claims")},
		{want: "c015000102", call: get("/wm/metadata/billing")},
		{want: "c015000103", call: post("/wm/near-miss/claims?dryRun=false", `{ "type": "motor" }`, jsonCT, appJS, "X-Tenant", "acme")},
		{want: "c015000104", call: get("/wm/misc/persistent")},
		{
			call:     post("/wm/near-miss/claims?dryRun=true", `{ "type": "home" }`, jsonCT, appJS, "X-Tenant", "globex"),
			contains: []string{"does not match"},
		},
	}
}

// wiremockUnsupported lists mappings the engine cannot serve yet, with the reason. They are
// stored (the corpus loads in full) but never compiled into the serving snapshot.
var wiremockUnsupported = map[string]string{
	"c003000403": "scheme and port matching needs an HTTPS listener on 8443; TestTLSMatchesWireMock compares it",
	"c013000101": "proxies to an upstream on 8089; the acceptance suite covers proxying", "c013000103": "proxies to an upstream on 8089; the acceptance suite covers proxying",
	"c013000104": "proxies to an upstream on 8089; the acceptance suite covers proxying", "c013000105": "proxies to a templated host; the acceptance suite covers proxying",
}

// wiremockShadowed lists mappings that are never served because another wins for every
// request they match. The corpus has them to prove it.
var wiremockShadowed = map[string]string{
	"c008000105": "priority 6 loses to the default priority 5 (c008000103)",
}

func TestWireMockCorpus(t *testing.T) {
	// The subtests share one engine and its journal, so they run in order.
	e := start(t, "wiremock")
	ms := loadMappings(t, "wiremock")

	t.Run("every mapping file is read and stored", func(t *testing.T) {
		se := e.with(t)
		for _, m := range ms {
			if r, _ := se.admin("GET", "/mappings/"+m.ID, ""); r.status != 200 {
				t.Errorf("mapping %s (%s) was not stored: %d", m.ID, m.File, r.status)
			}
		}
	})

	covered := map[string]bool{}
	for _, c := range wiremockCases() {
		if c.want != "" {
			covered[c.want] = true
		}
	}
	t.Run("every mapping has a case or a documented reason", func(t *testing.T) {
		for _, m := range ms {
			short := m.ID[:8] + m.ID[len(m.ID)-2:]
			_, unsupported := wiremockUnsupported[short]
			_, shadowed := wiremockShadowed[short]
			if !covered[short] && !unsupported && !shadowed {
				t.Errorf("mapping %s (%s, %q) has no conformance case; add one to wiremockCases or explain it in wiremockUnsupported", short, m.File, m.Response["body"])
			}
			if covered[short] && unsupported {
				t.Errorf("mapping %s has a case and is also marked unsupported", short)
			}
		}
		for short := range wiremockUnsupported {
			if _, ok := find(ms, short); !ok {
				t.Errorf("wiremockUnsupported lists %s, which is not in the corpus", short)
			}
		}
	})

	for _, c := range wiremockCases() {
		name := c.call.method + " " + c.call.target
		if c.want != "" {
			name += " → " + c.want
		}
		t.Run(name, func(t *testing.T) {
			runCase(t, e.with(t), ms, c)
		})
	}
}

func runCase(t *testing.T, e *env, ms []mapping, c tc) {
	t.Helper()
	var m mapping
	if c.want != "" {
		var ok bool
		if m, ok = find(ms, c.want); !ok {
			t.Fatalf("no corpus mapping %s", c.want)
		}
	}
	r, err := e.send(c.call)
	if c.fails {
		if err == nil {
			t.Fatalf("expected the connection to fail, got status %d", r.status)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	id, matched := e.latest()
	if c.want == "" {
		if matched {
			t.Fatalf("expected no mapping to serve the request, but %s did", id)
		}
		if r.status != 404 {
			t.Fatalf("unmatched request got %d, want 404", r.status)
		}
		for _, want := range append([]string{"Request was not matched"}, c.contains...) {
			if !strings.Contains(string(r.body), want) {
				t.Errorf("the 404 diagnostic lacks %q:\n%s", want, r.body)
			}
		}
		return
	}
	if !matched || id != m.ID {
		t.Fatalf("served by %q (matched %v), want %s (%s)", id, matched, m.ID, m.File)
	}
	if !c.fails {
		e.expectResponse(c.want, m.Response, r, c.status)
		if c.body != "" {
			e.expectRendered(c.want, c.body, r.body)
		}
	}
}

// with returns the environment for a subtest.
func (e *env) with(t *testing.T) *env {
	t.Helper()
	cp := *e
	cp.t = t
	return &cp
}

// A template that uses inline partials, block parameters, a partial called with a context, and a
// default for every field. Its output is checked in full, because a templated response is
// otherwise compared only by status.
const (
	defaultParcel = `{
		"id": "parcel-0001", "number": 2, "weight_kg": 5, "colour": "Blue", "pack": "M",
		"category": "Household", "label": "Books", "kind": "Standard_Parcel",
		"packed_on": "2025-04-25", "fragile": false, "mode": "standard", "state": "new",
		"value": { "amount": 123.00, "currency": "EUR" }, "limit": { "amount": 1000.00, "currency": "EUR" },
		"note": "Packing test", "tag": "Surface"
	}`
	shipmentsRequest = `{
		"order_id": "O2",
		"shipments": [
			{ "shipment_id": "sh-1", "carrier": "air", "parcels": [
				{ "colour": "Red & Gold", "pack": "L", "weight_kg": 3, "fragile": true,
				  "value": { "amount": 50.5, "currency": "USD" }, "note": "<this way up>" },
				{} ] },
			{ "shipment_code": "Other" }
		]
	}`
)

func shipmentCases() []tc {
	defaultShipment := `{ "shipment_id": "shipment-0001", "shipment_code": "General_Shipment", "carrier": "freight",
		"type": "StandardShipment", "parcels": [ ` + defaultParcel + ` ] }`
	return []tc{
		{
			want: "c010000401", call: post("/wm/template/shipments", `{"order_id":"O1"}`, jsonCT, appJS),
			body: `{ "order_id": "O1", "shipments": [ ` + defaultShipment + ` ] }`,
		},
		{
			want: "c010000401", call: post("/wm/template/shipments", shipmentsRequest, jsonCT, appJS),
			body: `{ "order_id": "O2", "shipments": [
				{ "shipment_id": "sh-1", "shipment_code": "General_Shipment", "carrier": "air",
				  "type": "StandardShipment", "parcels": [
					{ "id": "parcel-0001", "number": 2, "weight_kg": 3, "colour": "Red & Gold", "pack": "L",
					  "category": "Household", "label": "Books", "kind": "Standard_Parcel",
					  "packed_on": "2025-04-25", "fragile": true, "mode": "standard", "state": "new",
					  "value": { "amount": 50.5, "currency": "USD" }, "limit": { "amount": 1000.00, "currency": "EUR" },
					  "note": "<this way up>", "tag": "Surface" },
					` + defaultParcel + ` ] },
				{ "shipment_id": "shipment-0001", "shipment_code": "Other", "carrier": "freight",
				  "type": "StandardShipment", "parcels": [ ` + defaultParcel + ` ] }
			] }`,
		},
	}
}

// largeUpload is a JSON request body of about 100 KB, far past any size a journal might cut at.
func largeUpload() string {
	var b strings.Builder
	b.WriteString(`{"items":[`)
	for i := range 1500 {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":%d,"ref":"UPLOAD-%05d","note":"a large request body that must be kept whole"}`, i, i)
	}
	b.WriteString(`]}`)
	return b.String()
}

// jsonTransformBody holds JSON documents as strings, which is what WireMock's JSON helpers take.
const jsonTransformBody = `{
	"base": "{\"a\":1,\"b\":null}",
	"patch": "{\"b\":2,\"c\":null}",
	"items": "[{\"id\":123},{\"id\":7},{\"id\":8}]",
	"newItem": "{\"id\":9}"
}`
