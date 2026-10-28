package template

import (
	"crypto/rand"
	"encoding/binary"
	"math"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/antchfx/xmlquery"
	"github.com/mailgun/raymond/v2"

	"github.com/pwgith/shadow-mock/internal/xmlguard"
)

// moreHelpers are WireMock helpers beyond the ones in helpers(). Each behaves as WireMock 3.13.2
// does for the cases the corpus covers.
func moreHelpers() map[string]any {
	return map[string]any{
		"date":          formatDateHelper, // an alias of formatDate in WireMock
		"pickRandom":    pickRandom,
		"randomDecimal": randomDecimal,
		"soapXPath":     soapXPath,
		"formFields":    formFields, // the form {{formData body 'name'}} assigns; see rewrite
	}
}

// pickRandom implements {{pickRandom 'a' 'b' 'c'}} and {{pickRandom (array 'a' 'b')}}.
func pickRandom(items ...any) any {
	if len(items) == 1 {
		if l, ok := plain(items[0]).([]any); ok {
			items = l
		}
	}
	if len(items) == 0 {
		return safe("")
	}
	i, err := rand.Int(rand.Reader, big.NewInt(int64(len(items))))
	if err != nil {
		fail("pickRandom: %v", err)
	}
	return items[i.Int64()]
}

// randomDecimal implements {{randomDecimal lower=0 upper=1}}.
func randomDecimal(o *raymond.Options) any {
	lo, hi := 0.0, math.MaxFloat64/2
	if v, ok := number(o.HashProp("lower")); ok {
		lo = v
	}
	if v, ok := number(o.HashProp("upper")); ok {
		hi = v
	}
	if hi < lo {
		fail("randomDecimal: upper %v is below lower %v", hi, lo)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		fail("randomDecimal: %v", err)
	}
	unit := float64(binary.BigEndian.Uint64(b[:])>>11) / (1 << 53) // uniform in [0, 1)
	return safe(strconv.FormatFloat(lo+unit*(hi-lo), 'f', -1, 64))
}

// soapXPath implements {{soapXPath envelope '/Request/Id/text()'}}: the expression is evaluated
// against the first element inside the SOAP body, as if it were the whole document.
func soapXPath(doc, expr any, _ *raymond.Options) any {
	if !xmlguard.DepthOK([]byte(text(doc))) {
		fail("soapXPath: the XML is nested more than %d levels deep", xmlguard.MaxDepth)
	}
	root, err := xmlquery.Parse(strings.NewReader(text(doc)))
	if err != nil {
		fail("soapXPath: the input is not valid XML: %v", err)
	}
	var body *xmlquery.Node
	for _, n := range xmlquery.Find(root, "//*") {
		if n.Data == "Body" {
			body = n
			break
		}
	}
	if body == nil {
		return safe("")
	}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xmlquery.ElementNode {
			stripNamespaces(c)
			return xPath(c.OutputXML(true), expr, nil)
		}
	}
	return safe("")
}

// formFields parses a url-encoded form into the value {{formData body 'name'}} assigns: one value
// is a string, several are a list. The text is decoded only when decode is true, as in WireMock.
func formFields(body any, decode bool) any {
	out := map[string]any{}
	for _, pair := range strings.Split(text(body), "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		if decode {
			if dk, err := url.QueryUnescape(k); err == nil {
				k = dk
			}
			if dv, err := url.QueryUnescape(v); err == nil {
				v = dv
			}
		}
		switch cur := out[k].(type) {
		case nil:
			out[k] = safe(v)
		case []any:
			out[k] = append(cur, safe(v))
		default:
			out[k] = []any{cur, safe(v)}
		}
	}
	return jsonObject(out)
}

// stripNamespaces removes the namespace of an element and everything below it, so an expression
// can name elements by their local names, as WireMock's soapXPath allows.
func stripNamespaces(n *xmlquery.Node) {
	n.Prefix, n.NamespaceURI = "", ""
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if a.Name.Space != "xmlns" && a.Name.Local != "xmlns" {
			kept = append(kept, a)
		}
	}
	n.Attr = kept
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xmlquery.ElementNode {
			stripNamespaces(c)
		}
	}
}
