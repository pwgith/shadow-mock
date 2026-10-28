package template

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mailgun/raymond/v2"

	"github.com/pwgith/shadow-mock/internal/xmlguard"
)

// Helpers that write variables, and the block forms of helpers that read the text between their
// tags (FR-10.3). The rewrite step renames {{#assign}} to assignBlock and so on, because raymond
// fixes the number of parameters a helper takes.

// blockHelpers returns the helpers that need the render's variable store or a block body.
func blockHelpers() map[string]any {
	return map[string]any{
		"assign":          assignHelper,
		"assignBlock":     assignBlock,
		"parseJsonBlock":  parseJSONBlock,
		"formatJsonBlock": func(o *raymond.Options) any { return formatJSON(blockText(o), o) },
		"formatXml":       formatXML,
		"formatXmlBlock":  func(o *raymond.Options) any { return formatXML(blockText(o), o) },
		"trimBlock":       func(o *raymond.Options) any { return safe(strings.TrimSpace(blockText(o))) },
		"jsonArrayAddBlock": func(arr any, o *raymond.Options) any {
			return jsonArrayAdd(arr, blockText(o), o)
		},
		"jsonMergeBlock": func(base any, o *raymond.Options) any { return jsonMerge(base, blockText(o), o) },
	}
}

// blockText renders the text between a block helper's tags, as plain text.
func blockText(o *raymond.Options) string { return unmark(o.Fn()) }

// setVar stores a variable for the rest of the render.
func setVar(o *raymond.Options, name string, v any) {
	vars, ok := o.DataFrame().Get(varsKey).(map[string]any)
	if !ok {
		fail("cannot set the variable %q here", name)
	}
	vars[name] = v
}

// assignHelper implements {{assign 'name' value}}. WireMock's assign always stores text.
func assignHelper(name, v any, o *raymond.Options) any {
	setVar(o, text(name), Safe(v))
	return safe("")
}

// assignBlock implements {{#assign 'name'}}text{{/assign}}.
func assignBlock(name any, o *raymond.Options) any {
	setVar(o, text(name), raymond.SafeString(strings.TrimSpace(blockText(o))))
	return safe("")
}

// parseJSONBlock implements {{#parseJson 'name'}}json{{/parseJson}}. Without a name it returns the
// parsed value.
func parseJSONBlock(name any, o *raymond.Options) any {
	v := wrap(document(blockText(o)))
	if text(name) == "" {
		return v
	}
	setVar(o, text(name), v)
	return safe("")
}

// formatXML implements {{formatXml xml format='pretty'|'compact'}}; pretty is the default.
func formatXML(doc any, o *raymond.Options) any {
	out, err := prettyXML(text(doc), o.HashStr("format") == "compact")
	if err != nil {
		fail("formatXml: %v", err)
	}
	return safe(out)
}

// prettyXML lays out XML text with 2-space indents and a trailing newline, or with no layout.
func prettyXML(src string, compact bool) (string, error) {
	if !xmlguard.DepthOK([]byte(src)) {
		return "", fmt.Errorf("the XML is nested more than %d levels deep", xmlguard.MaxDepth)
	}
	root, err := parseXMLTree(src)
	if err != nil {
		return "", fmt.Errorf("the input is not valid XML: %w", err)
	}
	var b strings.Builder
	for i, n := range root.children {
		if !compact && i > 0 {
			b.WriteString("\n")
		}
		n.write(&b, 0, compact)
	}
	if !compact {
		b.WriteString("\n")
	}
	return b.String(), nil
}

// xmlNode is an element, comment, processing instruction or text.
type xmlNode struct {
	kind     int
	name     string // element name as written, or the text of a comment or instruction
	attrs    []xml.Attr
	children []*xmlNode
}

const (
	kindElement = iota
	kindText
	kindComment
	kindProcInst
)

func parseXMLTree(src string) (*xmlNode, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.Strict = false
	root := &xmlNode{}
	stack := []*xmlNode{root}
	for {
		tok, err := dec.RawToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read XML: %w", err)
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xmlNode{kind: kindElement, name: qualified(t.Name), attrs: t.Attr}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			if s := string(t); strings.TrimSpace(s) != "" {
				top.children = append(top.children, &xmlNode{kind: kindText, name: strings.TrimSpace(s)})
			}
		case xml.Comment:
			top.children = append(top.children, &xmlNode{kind: kindComment, name: string(t)})
		case xml.ProcInst:
			top.children = append(top.children, &xmlNode{kind: kindProcInst, name: t.Target + " " + string(t.Inst)})
		}
	}
	if len(stack) != 1 || len(root.children) == 0 {
		return nil, errors.New("the document is empty or not closed")
	}
	return root, nil
}

func qualified(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}

func (n *xmlNode) write(b *strings.Builder, depth int, compact bool) {
	pad := ""
	if !compact {
		pad = strings.Repeat("  ", depth)
	}
	switch n.kind {
	case kindText:
		b.WriteString(pad + escapeXMLText(n.name))
	case kindComment:
		b.WriteString(pad + "<!--" + n.name + "-->")
	case kindProcInst:
		b.WriteString(pad + "<?" + n.name + "?>")
	default:
		n.writeElement(b, pad, depth, compact)
	}
}

func (n *xmlNode) writeElement(b *strings.Builder, pad string, depth int, compact bool) {
	b.WriteString(pad + "<" + n.name)
	for _, a := range n.attrs {
		b.WriteString(" " + qualified(a.Name) + `="` + escapeXMLText(a.Value) + `"`)
	}
	switch {
	case len(n.children) == 0:
		b.WriteString("/>")
	case len(n.children) == 1 && n.children[0].kind == kindText:
		b.WriteString(">" + escapeXMLText(n.children[0].name) + "</" + n.name + ">")
	default:
		b.WriteString(">")
		for _, c := range n.children {
			if !compact {
				b.WriteString("\n")
			}
			c.write(b, depth+1, compact)
		}
		if !compact {
			b.WriteString("\n")
		}
		b.WriteString(pad + "</" + n.name + ">")
	}
}

func escapeXMLText(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return s
	}
	return b.String()
}

// prettyJSON writes JSON text with Jackson's default pretty printer, which WireMock uses: objects
// are spread over lines, arrays stay on one line, and a colon has a space on each side. Key order is
// kept, so it reads the text, not a parsed map.
func prettyJSON(src string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(src))
	dec.UseNumber()
	var b strings.Builder
	if err := writeValue(dec, &b, 0, true); err != nil {
		return "", err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("unexpected text after the JSON value")
	}
	return b.String(), nil
}

// compactJSON writes JSON text without any layout, keeping key order.
func compactJSON(src string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(src))
	dec.UseNumber()
	var b strings.Builder
	if err := writeValue(dec, &b, 0, false); err != nil {
		return "", err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("unexpected text after the JSON value")
	}
	return b.String(), nil
}

func writeValue(dec *json.Decoder, b *strings.Builder, depth int, pretty bool) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}
	return writeToken(dec, b, tok, depth, pretty)
}

func writeToken(dec *json.Decoder, b *strings.Builder, tok json.Token, depth int, pretty bool) error {
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			return writeObject(dec, b, depth, pretty)
		}
		return writeArray(dec, b, depth, pretty)
	case string:
		b.WriteString(jsonString(t))
	case json.Number:
		b.WriteString(t.String())
	case bool:
		fmt.Fprint(b, t)
	case nil:
		b.WriteString("null")
	}
	return nil
}

func jsonString(s string) string {
	out, err := marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return string(out)
}

// endDelim reads the closing } or ] the decoder has already counted as ended.
func endDelim(dec *json.Decoder) error {
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("read JSON: %w", err)
	}
	return nil
}

func writeObject(dec *json.Decoder, b *strings.Builder, depth int, pretty bool) error {
	if !dec.More() {
		if pretty {
			b.WriteString("{ }")
		} else {
			b.WriteString("{}")
		}
		return endDelim(dec)
	}
	b.WriteString("{")
	for first := true; dec.More(); first = false {
		if !first {
			b.WriteString(",")
		}
		if pretty {
			b.WriteString("\n" + strings.Repeat("  ", depth+1))
		}
		key, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read JSON: %w", err)
		}
		b.WriteString(jsonString(fmt.Sprint(key)))
		if pretty {
			b.WriteString(" : ")
		} else {
			b.WriteString(":")
		}
		if err := writeValue(dec, b, depth+1, pretty); err != nil {
			return err
		}
	}
	if pretty {
		b.WriteString("\n" + strings.Repeat("  ", depth))
	}
	b.WriteString("}")
	return endDelim(dec)
}

func writeArray(dec *json.Decoder, b *strings.Builder, depth int, pretty bool) error {
	if !dec.More() {
		if pretty {
			b.WriteString("[ ]")
		} else {
			b.WriteString("[]")
		}
		return endDelim(dec)
	}
	b.WriteString("[")
	if pretty {
		b.WriteString(" ")
	}
	for first := true; dec.More(); first = false {
		if !first {
			b.WriteString(",")
			if pretty {
				b.WriteString(" ")
			}
		}
		if err := writeValue(dec, b, depth, pretty); err != nil {
			return err
		}
	}
	if pretty {
		b.WriteString(" ")
	}
	b.WriteString("]")
	return endDelim(dec)
}
