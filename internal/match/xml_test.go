package match

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/pkg/extension"
)

func matchesXML(t *testing.T, pattern, body string) Result {
	t.Helper()
	m, err := CompileContent(vp(t, pattern), opts())
	if err != nil {
		t.Fatalf("CompileContent(%s): %v", pattern, err)
	}
	return m.MatchContent(ContentOf([]byte(body)))
}

func TestEqualToXML(t *testing.T) {
	t.Parallel()
	const want = `{"equalToXml":"<claim a=\"1\" b=\"2\"><id>1</id><!-- note --><s>  OPEN  </s></claim>"}`
	tests := []struct {
		name, body string
		ok         bool
	}{
		{"identical", `<claim a="1" b="2"><id>1</id><s>OPEN</s></claim>`, true},
		{"whitespace between elements", "<claim a=\"1\" b=\"2\">\n  <id>1</id>\n  <s> OPEN </s>\n</claim>", true},
		{"attribute order", `<claim b="2" a="1"><id>1</id><s>OPEN</s></claim>`, true},
		{"comments are ignored", `<claim a="1" b="2"><!-- other --><id>1</id><s>OPEN</s></claim>`, true},
		{"declaration", `<?xml version="1.0"?><claim a="1" b="2"><id>1</id><s>OPEN</s></claim>`, true},
		{"different text", `<claim a="1" b="2"><id>1</id><s>CLOSED</s></claim>`, false},
		{"different attribute", `<claim a="1" b="3"><id>1</id><s>OPEN</s></claim>`, false},
		{"missing attribute", `<claim a="1"><id>1</id><s>OPEN</s></claim>`, false},
		{"extra element", `<claim a="1" b="2"><id>1</id><s>OPEN</s><x/></claim>`, false},
		{"element order", `<claim a="1" b="2"><s>OPEN</s><id>1</id></claim>`, false},
		{"different root", `<other a="1" b="2"><id>1</id><s>OPEN</s></other>`, false},
		{"not XML", `nope`, false},
		{"empty", ``, false},
	}
	for _, tt := range tests {
		if got := matchesXML(t, want, tt.body); got.Matched != tt.ok {
			t.Errorf("%s: matched = %v, want %v", tt.name, got.Matched, tt.ok)
		}
	}
	if d := matchesXML(t, want, `<claim a="1" b="2"><id>1</id><s>CLOSED</s></claim>`).Distance; d <= 0 || d >= 1 {
		t.Errorf("distance of a near miss = %v, want between 0 and 1", d)
	}
}

func TestEqualToXMLNamespacesAndPlaceholders(t *testing.T) {
	t.Parallel()
	// The prefix does not matter, the namespace does.
	const ns = `{"equalToXml":"<a:r xmlns:a=\"urn:x\"><a:c/></a:r>"}`
	if !matchesXML(t, ns, `<b:r xmlns:b="urn:x"><b:c/></b:r>`).Matched {
		t.Error("a different prefix for the same namespace did not match")
	}
	if matchesXML(t, ns, `<a:r xmlns:a="urn:y"><a:c/></a:r>`).Matched {
		t.Error("a different namespace matched")
	}
	const ph = `{"equalToXml":"<r a=\"${xmlunit.ignore}\"><n>${xmlunit.isNumber}</n><s>${xmlunit.matchesRegex([A-Z]+-\\d+)}</s><i>${xmlunit.ignore}</i></r>","enablePlaceholders":true}`
	for body, want := range map[string]bool{
		`<r a="anything"><n>3.5</n><s>ORD-12</s><i><deep/></i></r>`: true,
		`<r a="x"><n>abc</n><s>ORD-12</s><i>y</i></r>`:              false,
		`<r a="x"><n>1</n><s>ord-12</s><i>y</i></r>`:                false,
	} {
		if got := matchesXML(t, ph, body).Matched; got != want {
			t.Errorf("%s: matched = %v, want %v", body, got, want)
		}
	}
	// Without enablePlaceholders the text is compared as it is.
	if matchesXML(t, `{"equalToXml":"<r>${xmlunit.ignore}</r>"}`, `<r>1</r>`).Matched {
		t.Error("a placeholder was honoured without enablePlaceholders")
	}
	const custom = `{"equalToXml":"<r>[[ignore]]</r>","enablePlaceholders":true,"placeholderOpeningDelimiterRegex":"\\[\\[","placeholderClosingDelimiterRegex":"\\]\\]"}`
	if !matchesXML(t, custom, `<r>x</r>`).Matched {
		t.Error("custom placeholder delimiters were not used")
	}
	// Comments count when comments are not ignored.
	const strict = `{"equalToXml":"<r><!--c--></r>","ignoreComments":false}`
	if matchesXML(t, strict, `<r/>`).Matched || !matchesXML(t, strict, `<r><!-- c --></r>`).Matched {
		t.Error("ignoreComments=false was not honoured")
	}
}

func TestEqualToXMLRejectsBadPatterns(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		`{"equalToXml":5}`,
		`{"equalToXml":"<a>"}`,
		`{"equalToXml":"<a/>","enablePlaceholders":"yes"}`,
		`{"equalToXml":"<a/>","enablePlaceholders":true,"placeholderOpeningDelimiterRegex":"("}`,
		`{"equalToXml":"<a/>","enablePlaceholders":true,"placeholderClosingDelimiterRegex":"("}`,
	} {
		if _, err := CompileContent(vp(t, p), opts()); err == nil {
			t.Errorf("CompileContent(%s) succeeded, want an error", p)
		}
	}
}

func TestMatchesXPath(t *testing.T) {
	t.Parallel()
	const doc = `<claim><id>42</id><items><item sku="A1"/><item sku="B2"/></items><note>Hello &amp; bye</note></claim>`
	tests := []struct {
		pattern string
		ok      bool
	}{
		{`{"matchesXPath":"/claim/id"}`, true},
		{`{"matchesXPath":"/claim/missing"}`, false},
		{`{"matchesXPath":"//item[@sku='B2']"}`, true},
		{`{"matchesXPath":"count(//item) = 2"}`, true},
		{`{"matchesXPath":"count(//item) = 3"}`, false},
		{`{"matchesXPath":"count(//item)"}`, true},
		{`{"matchesXPath":"string(/claim/id)"}`, true},
		{`{"matchesXPath":{"expression":"/claim/id/text()","equalTo":"42"}}`, true},
		{`{"matchesXPath":{"expression":"/claim/id/text()","equalTo":"43"}}`, false},
		{`{"matchesXPath":{"expression":"//item/@sku","matches":"B.*"}}`, true},
		{`{"matchesXPath":{"expression":"/claim/note","contains":"Hello & bye"}}`, false}, // an element prints as XML
		{`{"matchesXPath":{"expression":"/claim/note/text()","contains":"Hello & bye"}}`, true},
		{`{"matchesXPath":{"expression":"/claim/none","absent":true}}`, true}, // nothing selected is absent
		{`{"matchesXPath":{"expression":"/claim/id","equalToXml":"<id>42</id>"}}`, true},
	}
	for _, tt := range tests {
		if got := matchesXML(t, tt.pattern, doc).Matched; got != tt.ok {
			t.Errorf("%s: matched = %v, want %v", tt.pattern, got, tt.ok)
		}
	}
	if matchesXML(t, `{"matchesXPath":"/a"}`, `not xml <`).Matched {
		t.Error("an unparsable body matched")
	}
}

func TestMatchesXPathNamespaces(t *testing.T) {
	t.Parallel()
	const doc = `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><x:Get xmlns:x="urn:claims"/></s:Body></s:Envelope>`
	// The expression's prefixes are its own: they map to namespaces, not to the document's prefixes.
	const p = `{"matchesXPath":"/soap:Envelope/soap:Body/c:Get","xPathNamespaces":{"soap":"http://schemas.xmlsoap.org/soap/envelope/","c":"urn:claims"}}`
	if !matchesXML(t, p, doc).Matched {
		t.Error("the expression did not match through the namespace map")
	}
	const wrong = `{"matchesXPath":"/soap:Envelope/soap:Body/c:Get","xPathNamespaces":{"soap":"http://schemas.xmlsoap.org/soap/envelope/","c":"urn:other"}}`
	if matchesXML(t, wrong, doc).Matched {
		t.Error("a wrong namespace matched")
	}
}

func TestMatchesXPathRejectsBadPatterns(t *testing.T) {
	t.Parallel()
	for _, p := range []string{
		`{"matchesXPath":5}`,
		`{"matchesXPath":"//["}`,
		`{"matchesXPath":{"equalTo":"x"}}`,
		`{"matchesXPath":{"expression":"/a","bogus":1}}`,
		`{"matchesXPath":"/a","xPathNamespaces":"nope"}`,
	} {
		if _, err := CompileContent(vp(t, p), opts()); err == nil {
			t.Errorf("CompileContent(%s) succeeded, want an error", p)
		}
	}
}

func TestCustomMatcher(t *testing.T) {
	t.Parallel()
	var seen extension.Request
	var params map[string]any
	o := opts()
	o.CustomMatchers = map[string]extension.RequestMatcher{
		"even": extension.MatcherFunc("even", func(r extension.Request, p map[string]any) bool {
			seen, params = r, p
			return r.Query.Get("id") == "4"
		}),
	}
	var rp model.RequestPattern
	if err := rp.UnmarshalJSON([]byte(`{"urlPath":"/x","customMatcher":{"name":"even","parameters":{"k":"v"}}}`)); err != nil {
		t.Fatal(err)
	}
	m, err := Compile(&rp, o)
	if err != nil {
		t.Fatal(err)
	}
	req := func(target string) *Request {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://h"+target, strings.NewReader("body"))
		r.Header.Set("X-A", "1")
		return NewRequest(r, []byte("body"), r.URL.EscapedPath())
	}
	if !m.Match(req("/x?id=4")) || m.Match(req("/x?id=5")) {
		t.Error("the custom matcher's answer was not used")
	}
	if seen.Method != http.MethodPost || seen.Path != "/x" || seen.URL != "/x?id=5" || string(seen.Body) != "body" ||
		seen.Header.Get("X-A") != "1" || seen.Query.Get("id") != "5" || params["k"] != "v" {
		t.Errorf("the matcher saw %+v with %v", seen, params)
	}
	// The matcher gets copies: changing them does not change the request.
	r := req("/x?id=4")
	mutating := extension.MatcherFunc("mut", func(r extension.Request, _ map[string]any) bool {
		r.Header.Set("X-A", "changed")
		r.Body[0] = 'X'
		return true
	})
	o.CustomMatchers["mut"] = mutating
	var mp model.RequestPattern
	if perr := mp.UnmarshalJSON([]byte(`{"urlPath":"/x","customMatcher":{"name":"mut"}}`)); perr != nil {
		t.Fatal(perr)
	}
	mm, err := Compile(&mp, o)
	if err != nil || !mm.Match(r) || r.Header.Get("X-A") != "1" || string(r.Body) != "body" {
		t.Errorf("the request was changed by a matcher: %v %q (%v)", r.Header, r.Body, err)
	}
	var unknown model.RequestPattern
	if err := unknown.UnmarshalJSON([]byte(`{"urlPath":"/x","customMatcher":{"name":"nope"}}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(&unknown, o); err == nil || !strings.Contains(err.Error(), "unknown custom matcher") {
		t.Errorf("an unknown matcher gave %v", err)
	}
}

func TestEqualToXMLDateTimeAndUnorderedChildren(t *testing.T) {
	t.Parallel()
	const dt = `{"equalToXml":"<r><d>${xmlunit.isDateTime}</d></r>","enablePlaceholders":true}`
	for body, want := range map[string]bool{
		`<r><d>2026-11-01T09:30:00Z</d></r>`:      true,
		`<r><d>2026-11-01T09:30:00.123</d></r>`:   true,
		`<r><d>2026-11-01T09:30:00+10:00</d></r>`: true,
		`<r><d>yesterday</d></r>`:                 false,
	} {
		if got := matchesXML(t, dt, body).Matched; got != want {
			t.Errorf("%s: matched = %v, want %v", body, got, want)
		}
	}
	const ordered = `{"equalToXml":"<r><a/><b/></r>"}`
	const unordered = `{"equalToXml":"<r><a/><b/></r>","exemptedComparisons":["CHILD_NODELIST_SEQUENCE"]}`
	if matchesXML(t, ordered, `<r><b/><a/></r>`).Matched || !matchesXML(t, unordered, `<r><b/><a/></r>`).Matched {
		t.Error("exempting CHILD_NODELIST_SEQUENCE did not allow a different child order")
	}
	if matchesXML(t, unordered, `<r><b/><b/></r>`).Matched {
		t.Error("an unordered match reused one child for two expected children")
	}
}
