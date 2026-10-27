package match

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"

	"github.com/pwgith/shadow-mock/internal/model"
	"github.com/pwgith/shadow-mock/internal/xmlguard"
)

// xmlOptions are the equalToXml settings that sit beside the expected document.
type xmlOptions struct {
	EnablePlaceholders              bool     `json:"enablePlaceholders"`
	PlaceholderOpeningDelimiterRE   string   `json:"placeholderOpeningDelimiterRegex"`
	PlaceholderClosingDelimiterRE   string   `json:"placeholderClosingDelimiterRegex"`
	IgnoreWhitespace                *bool    `json:"ignoreWhitespace"`
	IgnoreComments                  *bool    `json:"ignoreComments"`
	ExemptedComments                []string `json:"exemptedComments"`
	ExemptedComparisons             []string `json:"exemptedComparisons"`
	placeholderOpen, placeholderEnd *regexp.Regexp
}

// compileEqualToXML implements equalToXml: the request body is the same XML document as the
// expected one, ignoring whitespace between elements, comments, attribute order and the
// choice of namespace prefix. Placeholders such as ${xmlunit.ignore} are optional.
func compileEqualToXML(p model.ValuePattern) (ContentMatcher, error) {
	var expected string
	if err := json.Unmarshal(p["equalToXml"], &expected); err != nil {
		return nil, fmt.Errorf("equalToXml must be a string: %w", err)
	}
	var o xmlOptions
	for key, dst := range map[string]any{
		"enablePlaceholders": &o.EnablePlaceholders, "placeholderOpeningDelimiterRegex": &o.PlaceholderOpeningDelimiterRE,
		"placeholderClosingDelimiterRegex": &o.PlaceholderClosingDelimiterRE, "ignoreWhitespace": &o.IgnoreWhitespace,
		"ignoreComments": &o.IgnoreComments, "exemptedComments": &o.ExemptedComments,
		"exemptedComparisons": &o.ExemptedComparisons,
	} {
		if raw, ok := p[key]; ok {
			if err := json.Unmarshal(raw, dst); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	if o.EnablePlaceholders {
		var err error
		if o.placeholderOpen, err = delimiter(o.PlaceholderOpeningDelimiterRE, `\$\{`); err != nil {
			return nil, fmt.Errorf("placeholderOpeningDelimiterRegex: %w", err)
		}
		if o.placeholderEnd, err = delimiter(o.PlaceholderClosingDelimiterRE, `\}`); err != nil {
			return nil, fmt.Errorf("placeholderClosingDelimiterRegex: %w", err)
		}
	}
	want, err := xmlquery.Parse(strings.NewReader(expected))
	if err != nil {
		return nil, fmt.Errorf("equalToXml: the expected document is not valid XML: %w", err)
	}
	return contentFunc(func(c Content) Result {
		if !xmlguard.DepthOK(c.Bytes) {
			return miss()
		}
		got, err := xmlquery.Parse(bytes.NewReader(c.Bytes))
		if err != nil {
			return miss()
		}
		if o.equal(want, got) {
			return hit()
		}
		return Result{Distance: textDistance(canonical(got), canonical(want))}
	}), nil
}

func delimiter(custom, fallback string) (*regexp.Regexp, error) {
	if custom == "" {
		custom = fallback
	}
	re, err := regexp.Compile(custom)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression: %w", err)
	}
	return re, nil
}

// equal compares two documents.
func (o *xmlOptions) equal(want, got *xmlquery.Node) bool {
	return o.sameChildren(significant(want, o), significant(got, o))
}

// significant lists a node's children that take part in the comparison: elements and text,
// with whitespace-only text and (unless exempted) comments dropped.
func significant(n *xmlquery.Node, o *xmlOptions) []*xmlquery.Node {
	var out []*xmlquery.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xmlquery.ElementNode:
			out = append(out, c)
		case xmlquery.TextNode, xmlquery.CharDataNode:
			if strings.TrimSpace(c.Data) != "" {
				out = append(out, c)
			}
		case xmlquery.CommentNode:
			if o.keepsComment(c.Data) {
				out = append(out, c)
			}
		default: // declarations and the like take no part in the comparison
		}
	}
	return out
}

func (o *xmlOptions) keepsComment(text string) bool {
	if o.IgnoreComments != nil && !*o.IgnoreComments {
		return true
	}
	for _, e := range o.ExemptedComments {
		if strings.TrimSpace(text) == strings.TrimSpace(e) {
			return true
		}
	}
	return false
}

func (o *xmlOptions) sameChildren(want, got []*xmlquery.Node) bool {
	// A lone placeholder as the content of an element stands for any content.
	if len(want) == 1 && want[0].Type != xmlquery.ElementNode && o.placeholder(want[0].Data) == "ignore" {
		return true
	}
	if len(want) != len(got) {
		return false
	}
	if slices.Contains(o.ExemptedComparisons, "CHILD_NODELIST_SEQUENCE") {
		return o.sameUnordered(want, got)
	}
	for i := range want {
		if !o.sameNode(want[i], got[i]) {
			return false
		}
	}
	return true
}

// sameUnordered reports whether each expected node has a distinct equal node in got, in any order.
func (o *xmlOptions) sameUnordered(want, got []*xmlquery.Node) bool {
	used := make([]bool, len(got))
	for _, w := range want {
		found := false
		for i, g := range got {
			if !used[i] && o.sameNode(w, g) {
				used[i], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (o *xmlOptions) sameNode(want, got *xmlquery.Node) bool {
	if want.Type != got.Type {
		return false
	}
	switch want.Type {
	case xmlquery.ElementNode:
		return want.Data == got.Data && namespace(want) == namespace(got) &&
			o.sameAttrs(want, got) && o.sameChildren(significant(want, o), significant(got, o))
	case xmlquery.CommentNode:
		return strings.TrimSpace(want.Data) == strings.TrimSpace(got.Data)
	default:
		return o.sameText(want.Data, got.Data)
	}
}

func namespace(n *xmlquery.Node) string { return n.NamespaceURI }

// sameAttrs compares attributes as sets, leaving out namespace declarations.
func (o *xmlOptions) sameAttrs(want, got *xmlquery.Node) bool {
	w, g := attrs(want), attrs(got)
	if len(w) != len(g) {
		return false
	}
	for k, wv := range w {
		gv, ok := g[k]
		if !ok || !o.sameText(wv, gv) {
			return false
		}
	}
	return true
}

func attrs(n *xmlquery.Node) map[string]string {
	out := map[string]string{}
	for _, a := range n.Attr {
		if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
			continue
		}
		out[a.NamespaceURI+" "+a.Name.Local] = a.Value
	}
	return out
}

// sameText compares text with surrounding whitespace ignored, honouring placeholders.
func (o *xmlOptions) sameText(want, got string) bool {
	want, got = strings.TrimSpace(want), strings.TrimSpace(got)
	switch p := o.placeholder(want); {
	case p == "ignore":
		return true
	case p == "isNumber":
		_, err := strconv.ParseFloat(got, 64)
		return err == nil
	case p == "isDateTime":
		return isDateTime(got)
	case strings.HasPrefix(p, "matchesRegex(") && strings.HasSuffix(p, ")"):
		re, err := regexp.Compile("^(?:" + p[len("matchesRegex("):len(p)-1] + ")$")
		return err == nil && re.MatchString(got)
	}
	return collapse(want) == collapse(got)
}

// placeholder returns the name inside a whole-text placeholder such as ${xmlunit.ignore}.
func (o *xmlOptions) placeholder(s string) string {
	if !o.EnablePlaceholders {
		return ""
	}
	loc := o.placeholderOpen.FindStringIndex(s)
	if len(loc) == 0 || loc[0] != 0 {
		return ""
	}
	rest := s[loc[1]:]
	end := o.placeholderEnd.FindStringIndex(rest)
	if len(end) == 0 || loc[1]+end[1] != len(s) {
		return ""
	}
	return strings.TrimPrefix(rest[:end[0]], "xmlunit.")
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// canonical prints a document without insignificant whitespace, for a distance measure.
func canonical(n *xmlquery.Node) string {
	var b strings.Builder
	var walk func(*xmlquery.Node)
	walk = func(n *xmlquery.Node) {
		switch n.Type {
		case xmlquery.ElementNode:
			b.WriteString("<" + n.Data)
			names := make([]string, 0, len(n.Attr))
			vals := map[string]string{}
			for _, a := range n.Attr {
				names = append(names, a.Name.Local)
				vals[a.Name.Local] = a.Value
			}
			sort.Strings(names)
			for _, k := range names {
				b.WriteString(" " + k + "=" + vals[k])
			}
			b.WriteString(">")
		case xmlquery.TextNode, xmlquery.CharDataNode:
			b.WriteString(collapse(n.Data))
		default:
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// compileXPath implements matchesXPath: a bare expression matches when it selects something;
// with a sub-matcher, what it selects is matched as text.
func compileXPath(p model.ValuePattern, o Options) (ContentMatcher, error) {
	expr, sub, err := xpathPattern(p, o)
	if err != nil {
		return nil, err
	}
	var ns map[string]string
	if raw, ok := p["xPathNamespaces"]; ok {
		if uerr := json.Unmarshal(raw, &ns); uerr != nil {
			return nil, fmt.Errorf("xPathNamespaces must map prefixes to namespace URIs: %w", uerr)
		}
	}
	compiled, err := xpath.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("matchesXPath: invalid expression %q: %w", expr, err)
	}
	return contentFunc(func(c Content) Result {
		if !xmlguard.DepthOK(c.Bytes) {
			return miss()
		}
		doc, err := xmlquery.Parse(bytes.NewReader(c.Bytes))
		if err != nil {
			return miss()
		}
		usePrefixes(doc, ns)
		values, found := selectXPath(compiled, doc)
		if sub == nil {
			return boolResult(found)
		}
		best := miss()
		for _, v := range values {
			res := sub.MatchContent(ContentOf([]byte(v)))
			if res.Matched {
				return res
			}
			best.Distance = min(best.Distance, res.Distance)
		}
		if len(values) == 0 {
			return sub.MatchContent(ContentOf(nil))
		}
		return best
	}), nil
}

// usePrefixes renames the prefixes in a document to the ones the expression uses, matching
// them by namespace URI, so an expression's prefixes need not be the document's own.
func usePrefixes(doc *xmlquery.Node, ns map[string]string) {
	if len(ns) == 0 {
		return
	}
	byURI := make(map[string]string, len(ns))
	for prefix, uri := range ns {
		byURI[uri] = prefix
	}
	var walk func(*xmlquery.Node)
	walk = func(n *xmlquery.Node) {
		if n.Type == xmlquery.ElementNode {
			if p, ok := byURI[n.NamespaceURI]; ok {
				n.Prefix = p
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
}

// selectXPath evaluates an expression and returns the text of each result, and whether the
// expression selected anything (a node, a true boolean, or a non-empty string or number).
func selectXPath(e *xpath.Expr, doc *xmlquery.Node) (values []string, found bool) {
	// The XPath library panics on some documents, for example when a comparison reads text that
	// is not a number. A request body must not be able to do that to a request, so it counts as
	// selecting nothing.
	defer func() {
		if recover() != nil {
			values, found = nil, false
		}
	}()
	switch v := e.Evaluate(xmlquery.CreateXPathNavigator(doc)).(type) {
	case *xpath.NodeIterator:
		var out []string
		for v.MoveNext() {
			nav, ok := v.Current().(*xmlquery.NodeNavigator)
			if !ok {
				out = append(out, v.Current().Value())
				continue
			}
			if nav.NodeType() == xpath.ElementNode {
				out = append(out, nav.Current().OutputXML(true))
			} else {
				out = append(out, v.Current().Value())
			}
		}
		return out, len(out) > 0
	case bool:
		return []string{strconv.FormatBool(v)}, v
	case float64:
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}, true
	case string:
		return []string{v}, v != ""
	default:
		return nil, false
	}
}

// isDateTime reports whether s is an ISO 8601 date-time, with or without a zone.
func isDateTime(s string) bool {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}

// xpathPattern reads the expression and the optional sub-matcher of a matchesXPath pattern,
// which is either the expression as a string or an object holding it beside the sub-matcher.
func xpathPattern(p model.ValuePattern, o Options) (string, ContentMatcher, error) {
	var expr string
	if err := json.Unmarshal(p["matchesXPath"], &expr); err == nil {
		return expr, nil, nil
	}
	var obj model.ValuePattern
	if err := json.Unmarshal(p["matchesXPath"], &obj); err != nil {
		return "", nil, fmt.Errorf("matchesXPath must be a string or an object: %w", err)
	}
	if err := json.Unmarshal(obj["expression"], &expr); err != nil {
		return "", nil, fmt.Errorf("matchesXPath needs an expression: %w", err)
	}
	delete(obj, "expression")
	sub, err := CompileContent(obj, o)
	if err != nil {
		return "", nil, fmt.Errorf("matchesXPath %s: %w", expr, err)
	}
	return expr, sub, nil
}
