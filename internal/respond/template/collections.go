package template

import (
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"
	"github.com/mailgun/raymond/v2"

	"github.com/pwgith/shadow-mock/internal/jsonpath"
	"github.com/pwgith/shadow-mock/internal/xmlguard"
)

// Helpers that read documents and build lists, with the values printed as WireMock prints them
// (FR-10.5).

// jsonPath evaluates a JSONPath over JSON text or an already parsed value. A path that names one
// place gives that value, and any other path gives a list. An object prints as indented JSON and a
// list as compact JSON. When a path that names one place finds nothing, or null, the result is
// default=, or nothing.
func jsonPath(doc, expr any, o *raymond.Options) any {
	x, err := jsonpath.Parse(text(expr))
	if err != nil {
		return inlineError("%s is not a valid JSONPath expression", text(expr))
	}
	res := x.Get(document(doc))
	if !x.Definite() {
		if len(res) == 0 {
			return jsonList{}
		}
		return jsonResult(res)
	}
	if len(res) == 0 || res[0] == nil {
		return safe(o.HashStr("default"))
	}
	return jsonResult(res[0])
}

// jsonResult marks a parsed JSON value for printing: objects and lists print as JSON.
func jsonResult(v any) any {
	switch t := Safe(v).(type) {
	case map[string]any:
		return jsonObject(t)
	case []any:
		return jsonList(t)
	default:
		return t
	}
}

// xPath evaluates an XPath over XML text. An element prints as indented markup, a text or attribute
// node as its value, and a function such as count() as its result. Only the first node is used.
func xPath(doc, expr any, _ *raymond.Options) any {
	if !xmlguard.DepthOK([]byte(text(doc))) {
		fail("xPath: the XML is nested more than %d levels deep", xmlguard.MaxDepth)
	}
	root, err := xmlquery.Parse(strings.NewReader(text(doc)))
	if err != nil {
		fail("xPath: the input is not valid XML: %v", err)
	}
	compiled, err := xpath.Compile(text(expr))
	if err != nil {
		return inlineError("%s is not a valid XPath expression", text(expr))
	}
	switch v := compiled.Evaluate(xmlquery.CreateXPathNavigator(root)).(type) {
	case *xpath.NodeIterator:
		if !v.MoveNext() {
			return safe("")
		}
		nav := v.Current()
		if nav.NodeType() == xpath.ElementNode || nav.NodeType() == xpath.RootNode {
			if n, ok := nav.(*xmlquery.NodeNavigator); ok {
				return safe(xmlMarkup(n.Current()))
			}
		}
		return safe(nav.Value())
	case float64:
		return safe(strconv.FormatFloat(v, 'f', -1, 64))
	case bool:
		return safe(strconv.FormatBool(v))
	case string:
		return safe(v)
	default:
		return safe("")
	}
}

// xmlMarkup writes an element as indented markup, as WireMock prints a node.
func xmlMarkup(n *xmlquery.Node) string {
	if n.Type == xmlquery.DocumentNode { // the document, without its XML declaration
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xmlquery.ElementNode {
				n = c
				break
			}
		}
	}
	out, err := prettyXML(n.OutputXML(true), false)
	if err != nil {
		return n.OutputXML(true)
	}
	return out
}

// urlEncode implements {{urlEncode text}} as Java's URLEncoder does (a space is +), or decodes with
// decode=true.
func urlEncode(s any, o *raymond.Options) any {
	in := text(s)
	if truthy(o.HashProp("decode")) {
		out, err := url.QueryUnescape(in)
		if err != nil {
			fail("urlEncode: %v", err)
		}
		return safe(out)
	}
	var b strings.Builder
	for _, c := range []byte(in) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '*', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteString("%" + strings.ToUpper(strconv.FormatInt(int64(c)+256, 16)[1:]))
		}
	}
	return safe(b.String())
}

const badPosition = "position must be greater than or equal to 0 and less than or equal to the size of the list"

// position reads position= as an index into a list of n items: start (0), end (n), or a number.
func position(o *raymond.Options, n, dflt int) (int, any) {
	switch p := o.HashProp("position").(type) {
	case nil:
		return dflt, nil
	case int:
		if p < 0 || p > n {
			return 0, inlineError(badPosition)
		}
		return p, nil
	default:
		switch text(p) {
		case "start":
			return 0, nil
		case "end":
			return n, nil
		}
		return 0, inlineError("position must be 'start', 'end' or an integer")
	}
}

// arrayAdd implements (arrayAdd list item position=start|end|n): a new list with the item added.
func arrayAdd(items, item any, o *raymond.Options) any {
	in := asList(items)
	at, bad := position(o, len(in), len(in))
	if bad != nil {
		return bad
	}
	out := make(list, 0, len(in)+1)
	out = append(out, in[:at]...)
	out = append(out, item)
	return append(out, in[at:]...)
}

// arrayRemove implements (arrayRemove list position=start|end|n): a new list without that item;
// the last one by default.
func arrayRemove(items any, o *raymond.Options) any {
	in := asList(items)
	if len(in) == 0 {
		fail("arrayRemove: the list is empty")
	}
	at, bad := position(o, len(in), len(in)-1)
	if bad != nil {
		return bad
	}
	if at == len(in) && o.HashStr("position") == "end" {
		at--
	}
	if at >= len(in) {
		return inlineError(badPosition)
	}
	out := make(list, 0, len(in)-1)
	out = append(out, in[:at]...)
	return append(out, in[at+1:]...)
}

// arrayJoin implements {{arrayJoin ',' list prefix='[' suffix=']'}}; the prefix and the suffix
// go around the whole result.
func arrayJoin(sep, items any, o *raymond.Options) any {
	in := asList(items)
	parts := make([]string, len(in))
	for i, x := range in {
		parts[i] = javaString(x)
	}
	return safe(o.HashStr("prefix") + strings.Join(parts, text(sep)) + o.HashStr("suffix"))
}

// rangeHelper implements (range start end): the whole numbers from start to end, both included.
func rangeHelper(from, to any, _ *raymond.Options) any {
	a, ok1 := number(from)
	b, ok2 := number(to)
	if !ok1 || !ok2 || b < a || b-a > 100000 {
		fail("range: %v to %v is not a usable range", from, to)
	}
	out := make(list, 0, int(b)-int(a)+1)
	for i := int(a); i <= int(b); i++ {
		out = append(out, i)
	}
	return out
}

var indexedKey = regexp.MustCompile(`^(.+)\.(\d+)$`)

// lookup implements {{lookup object key}}: a field of an object, or an item of a list by its index.
// As in WireMock, a key such as ids[].1 names the second value of the field ids[].
func lookup(obj, key any, _ *raymond.Options) any {
	v := reflect.ValueOf(obj)
	switch {
	case v.Kind() == reflect.Map:
		return lookupField(v, text(key))
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Array:
		if n, ok := number(key); ok {
			return item(obj, int(n))
		}
	}
	return safe("")
}

// lookupField reads a field of a map, or the n-th value of the field when the key ends in .n.
func lookupField(m reflect.Value, name string) any {
	if found := m.MapIndex(reflect.ValueOf(name)); found.IsValid() {
		return found.Interface()
	}
	sub := indexedKey.FindStringSubmatch(name)
	if sub == nil {
		return safe("")
	}
	found := m.MapIndex(reflect.ValueOf(sub[1]))
	n, err := strconv.Atoi(sub[2])
	if !found.IsValid() || err != nil {
		return safe("")
	}
	return item(found.Interface(), n)
}

// item returns the n-th item of a list; a single value is its own item 0.
func item(v any, n int) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		if n >= 0 && n < rv.Len() {
			return rv.Index(n).Interface()
		}
		return safe("")
	}
	if n == 0 {
		return v
	}
	return safe("")
}
