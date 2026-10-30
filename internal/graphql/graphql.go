// Package graphql compares GraphQL queries and matches GraphQL requests (FR-10.16). It stands in for
// WireMock's graphql-body-matcher extension: two queries are equal when they differ only in
// whitespace, comments, commas, and in the order of definitions, selections, arguments, directives,
// variable definitions and the fields of an object value. Only executable documents (operations and
// fragments) are read; a schema definition is a syntax error here, as a request body never holds one.
package graphql

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// maxDepth bounds how deeply selections, lists and objects may nest, so a hostile request cannot
// exhaust the stack.
const maxDepth = 100

// Canonical returns query in a form that is the same for every query Equal would call equal to it.
func Canonical(query string) (string, error) {
	toks, err := lex(query)
	if err != nil {
		return "", err
	}
	p := &parser{toks: toks}
	return p.document()
}

// Equal reports whether two queries are the same apart from the differences in the package comment.
func Equal(a, b string) (bool, error) {
	ca, err := Canonical(a)
	if err != nil {
		return false, err
	}
	cb, err := Canonical(b)
	if err != nil {
		return false, err
	}
	return ca == cb, nil
}

type kind int

const (
	punct kind = iota
	name
	number
	str
	eof
)

type token struct {
	kind kind
	text string // for a string, its value once escapes are read
	pos  int
}

func isNameStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }
func isNameChar(c byte) bool  { return isNameStart(c) || isDigit(c) }

func lex(src string) ([]token, error) {
	src = strings.TrimPrefix(src, string(rune(0xFEFF))) // a byte order mark
	var toks []token
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',':
			i++
		case c == '#':
			for i < len(src) && src[i] != '\n' && src[i] != '\r' {
				i++
			}
		case strings.HasPrefix(src[i:], "..."):
			toks = append(toks, token{punct, "...", i})
			i += 3
		case strings.IndexByte("!$&():=@[]{|}", c) >= 0:
			toks = append(toks, token{punct, string(c), i})
			i++
		case isNameStart(c):
			j := i + 1
			for j < len(src) && isNameChar(src[j]) {
				j++
			}
			toks = append(toks, token{name, src[i:j], i})
			i = j
		case c == '-' || isDigit(c):
			j, err := scanNumber(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{number, src[i:j], i})
			i = j
		case c == '"':
			value, j, err := scanString(src, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{str, value, i})
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q at offset %d", rune(c), i)
		}
	}
	return append(toks, token{eof, "", len(src)}), nil
}

// scanDigits returns the end of the run of digits that starts at j.
func scanDigits(src string, j int) int {
	for j < len(src) && isDigit(src[j]) {
		j++
	}
	return j
}

// scanNumber returns the end of the number starting at i.
func scanNumber(src string, i int) (int, error) {
	j, err := scanInteger(src, i)
	if err != nil {
		return 0, err
	}
	if j < len(src) && src[j] == '.' {
		if j, err = scanFraction(src, j+1, i); err != nil {
			return 0, err
		}
	}
	if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
		if j, err = scanExponent(src, j+1, i); err != nil {
			return 0, err
		}
	}
	if j < len(src) && (src[j] == '.' || isNameStart(src[j])) {
		return 0, fmt.Errorf("a number may not be followed by %q (offset %d)", rune(src[j]), j)
	}
	return j, nil
}

func scanInteger(src string, i int) (int, error) {
	j := i
	if src[j] == '-' {
		j++
	}
	switch end := scanDigits(src, j); {
	case end == j:
		return 0, fmt.Errorf("a number needs digits (offset %d)", i)
	case src[j] == '0' && end > j+1:
		return 0, fmt.Errorf("a number may not start with 0 (offset %d)", i)
	default:
		return end, nil
	}
}

func scanFraction(src string, j, at int) (int, error) {
	end := scanDigits(src, j)
	if end == j {
		return 0, fmt.Errorf("a decimal point needs digits after it (offset %d)", at)
	}
	return end, nil
}

func scanExponent(src string, j, at int) (int, error) {
	if j < len(src) && (src[j] == '+' || src[j] == '-') {
		j++
	}
	end := scanDigits(src, j)
	if end == j {
		return 0, fmt.Errorf("an exponent needs digits (offset %d)", at)
	}
	return end, nil
}

// scanString reads a string or block string starting at i and returns its value and where it ends.
func scanString(src string, i int) (string, int, error) {
	if strings.HasPrefix(src[i:], `"""`) {
		return scanBlockString(src, i)
	}
	var b strings.Builder
	for j := i + 1; j < len(src); j++ {
		switch c := src[j]; c {
		case '"':
			return b.String(), j + 1, nil
		case '\n', '\r':
			return "", 0, fmt.Errorf("a string may not span lines (offset %d)", i)
		case '\\':
			j++
			if j >= len(src) {
				return "", 0, fmt.Errorf("unterminated string (offset %d)", i)
			}
			r, width, err := escape(src, j)
			if err != nil {
				return "", 0, err
			}
			b.WriteRune(r)
			j += width - 1
		default:
			b.WriteByte(c)
		}
	}
	return "", 0, fmt.Errorf("unterminated string (offset %d)", i)
}

// escape reads the escape sequence whose letter is at src[j], and returns the rune and the letters used.
func escape(src string, j int) (rune, int, error) {
	switch src[j] {
	case '"', '\\', '/':
		return rune(src[j]), 1, nil
	case 'b':
		return '\b', 1, nil
	case 'f':
		return '\f', 1, nil
	case 'n':
		return '\n', 1, nil
	case 'r':
		return '\r', 1, nil
	case 't':
		return '\t', 1, nil
	case 'u':
		if j+4 >= len(src) {
			return 0, 0, fmt.Errorf("bad \\u escape (offset %d)", j)
		}
		var r rune
		for _, c := range src[j+1 : j+5] {
			v := hexDigit(c)
			if v < 0 {
				return 0, 0, fmt.Errorf("bad \\u escape (offset %d)", j)
			}
			r = r*16 + v
		}
		return r, 5, nil
	}
	return 0, 0, fmt.Errorf("bad escape \\%c (offset %d)", src[j], j)
}

// hexDigit is the value of a hexadecimal digit, or -1.
func hexDigit(c rune) rune {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return -1
}

// scanBlockString reads a """ string and applies the specification's rules for removing indentation.
func scanBlockString(src string, i int) (string, int, error) {
	var raw strings.Builder
	for j := i + 3; j < len(src); j++ {
		switch {
		case strings.HasPrefix(src[j:], `\"""`):
			raw.WriteString(`"""`)
			j += 3
		case strings.HasPrefix(src[j:], `"""`):
			return blockValue(raw.String()), j + 3, nil
		default:
			raw.WriteByte(src[j])
		}
	}
	return "", 0, fmt.Errorf("unterminated block string (offset %d)", i)
}

func blockValue(raw string) string {
	lines := strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(raw), "\n")
	common := -1
	for _, l := range lines[1:] {
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		if indent < len(l) && (common < 0 || indent < common) {
			common = indent
		}
	}
	if common > 0 {
		for i := 1; i < len(lines); i++ {
			lines[i] = lines[i][min(common, len(lines[i])):]
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

type parser struct {
	toks  []token
	pos   int
	depth int
}

var errSyntax = errors.New("syntax error")

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != eof {
		p.pos++
	}
	return t
}

func (p *parser) fail(want string) error {
	t := p.peek()
	got := "end of the query"
	if t.kind != eof {
		got = strconv.Quote(t.text)
	}
	return fmt.Errorf("%w: expected %s but found %s (offset %d)", errSyntax, want, got, t.pos)
}

func (p *parser) isPunct(s string) bool { t := p.peek(); return t.kind == punct && t.text == s }

func (p *parser) accept(s string) bool {
	if p.isPunct(s) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) expect(s string) error {
	if !p.accept(s) {
		return p.fail(strconv.Quote(s))
	}
	return nil
}

func (p *parser) name() (string, error) {
	if t := p.peek(); t.kind == name {
		p.pos++
		return t.text, nil
	}
	return "", p.fail("a name")
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return fmt.Errorf("%w: nested more than %d levels", errSyntax, maxDepth)
	}
	return nil
}

func (p *parser) leave() { p.depth-- }

func (p *parser) document() (string, error) {
	var defs []string
	for p.peek().kind != eof {
		d, err := p.definition()
		if err != nil {
			return "", err
		}
		defs = append(defs, d)
	}
	if len(defs) == 0 {
		return "", p.fail("a definition")
	}
	slices.Sort(defs)
	return strings.Join(defs, "\n"), nil
}

func (p *parser) definition() (string, error) {
	if p.isPunct("{") {
		sel, err := p.selectionSet()
		return "query" + sel, err
	}
	t := p.peek()
	if t.kind != name {
		return "", p.fail("an operation or fragment")
	}
	switch t.text {
	case "query", "mutation", "subscription":
		p.pos++
		return p.operation(t.text)
	case "fragment":
		p.pos++
		return p.fragment()
	}
	return "", p.fail("query, mutation, subscription or fragment")
}

func (p *parser) operation(op string) (string, error) {
	var b strings.Builder
	b.WriteString(op)
	if p.peek().kind == name {
		b.WriteString(" " + p.next().text)
	}
	if p.isPunct("(") {
		vars, err := p.variableDefinitions()
		if err != nil {
			return "", err
		}
		b.WriteString(vars)
	}
	dirs, err := p.directives()
	if err != nil {
		return "", err
	}
	sel, err := p.selectionSet()
	if err != nil {
		return "", err
	}
	return b.String() + dirs + sel, nil
}

func (p *parser) fragment() (string, error) {
	n, err := p.name()
	if err != nil {
		return "", err
	}
	if n == "on" {
		return "", p.fail("a fragment name other than on")
	}
	if on, onErr := p.name(); onErr != nil || on != "on" {
		return "", p.fail("on")
	}
	typ, err := p.name()
	if err != nil {
		return "", err
	}
	dirs, err := p.directives()
	if err != nil {
		return "", err
	}
	sel, err := p.selectionSet()
	if err != nil {
		return "", err
	}
	return "fragment " + n + " on " + typ + dirs + sel, nil
}

func (p *parser) variableDefinitions() (string, error) {
	if err := p.expect("("); err != nil {
		return "", err
	}
	var defs []string
	for !p.isPunct(")") {
		if err := p.expect("$"); err != nil {
			return "", err
		}
		n, err := p.name()
		if err != nil {
			return "", err
		}
		if expectErr := p.expect(":"); expectErr != nil {
			return "", expectErr
		}
		typ, err := p.typeRef()
		if err != nil {
			return "", err
		}
		def := "$" + n + ":" + typ
		if p.accept("=") {
			v, valErr := p.value()
			if valErr != nil {
				return "", valErr
			}
			def += "=" + v
		}
		dirs, err := p.directives()
		if err != nil {
			return "", err
		}
		defs = append(defs, def+dirs)
	}
	p.pos++
	if len(defs) == 0 {
		return "", p.fail("a variable definition")
	}
	slices.Sort(defs)
	return "(" + strings.Join(defs, ",") + ")", nil
}

func (p *parser) typeRef() (string, error) {
	if err := p.enter(); err != nil {
		return "", err
	}
	defer p.leave()
	var typ string
	if p.accept("[") {
		inner, err := p.typeRef()
		if err != nil {
			return "", err
		}
		if expectErr := p.expect("]"); expectErr != nil {
			return "", expectErr
		}
		typ = "[" + inner + "]"
	} else {
		n, err := p.name()
		if err != nil {
			return "", err
		}
		typ = n
	}
	if p.accept("!") {
		typ += "!"
	}
	return typ, nil
}

func (p *parser) directives() (string, error) {
	var ds []string
	for p.accept("@") {
		n, err := p.name()
		if err != nil {
			return "", err
		}
		args, err := p.arguments()
		if err != nil {
			return "", err
		}
		ds = append(ds, "@"+n+args)
	}
	slices.Sort(ds)
	return strings.Join(ds, ""), nil
}

func (p *parser) arguments() (string, error) {
	if !p.accept("(") {
		return "", nil
	}
	var args []string
	for !p.isPunct(")") {
		n, err := p.name()
		if err != nil {
			return "", err
		}
		if expectErr := p.expect(":"); expectErr != nil {
			return "", expectErr
		}
		v, err := p.value()
		if err != nil {
			return "", err
		}
		args = append(args, n+":"+v)
	}
	p.pos++
	if len(args) == 0 {
		return "", p.fail("an argument")
	}
	slices.Sort(args)
	return "(" + strings.Join(args, ",") + ")", nil
}

func (p *parser) selectionSet() (string, error) {
	if err := p.enter(); err != nil {
		return "", err
	}
	defer p.leave()
	if err := p.expect("{"); err != nil {
		return "", err
	}
	var sels []string
	for !p.isPunct("}") {
		s, err := p.selection()
		if err != nil {
			return "", err
		}
		sels = append(sels, s)
	}
	p.pos++
	if len(sels) == 0 {
		return "", p.fail("a selection")
	}
	slices.Sort(sels)
	return "{" + strings.Join(sels, ",") + "}", nil
}

func (p *parser) selection() (string, error) {
	if p.accept("...") {
		return p.spread()
	}
	n, err := p.name()
	if err != nil {
		return "", err
	}
	field := n
	if p.accept(":") {
		target, nameErr := p.name()
		if nameErr != nil {
			return "", nameErr
		}
		field = n + ":" + target
	}
	args, err := p.arguments()
	if err != nil {
		return "", err
	}
	dirs, err := p.directives()
	if err != nil {
		return "", err
	}
	field += args + dirs
	if p.isPunct("{") {
		sel, selErr := p.selectionSet()
		if selErr != nil {
			return "", selErr
		}
		field += sel
	}
	return field, nil
}

func (p *parser) spread() (string, error) {
	t := p.peek()
	if t.kind == name && t.text != "on" {
		p.pos++
		dirs, err := p.directives()
		return "..." + t.text + dirs, err
	}
	cond := ""
	if t.kind == name { // on Type
		p.pos++
		typ, err := p.name()
		if err != nil {
			return "", err
		}
		cond = "on " + typ
	}
	dirs, err := p.directives()
	if err != nil {
		return "", err
	}
	sel, err := p.selectionSet()
	if err != nil {
		return "", err
	}
	return "..." + cond + dirs + sel, nil
}

func (p *parser) value() (string, error) {
	if err := p.enter(); err != nil {
		return "", err
	}
	defer p.leave()
	t := p.peek()
	switch {
	case t.kind == number || t.kind == name:
		p.pos++
		return t.text, nil
	case t.kind == str:
		p.pos++
		return strconv.Quote(t.text), nil
	case t.kind == punct && t.text == "$":
		p.pos++
		n, err := p.name()
		return "$" + n, err
	case t.kind == punct && t.text == "[":
		p.pos++
		return p.list()
	case t.kind == punct && t.text == "{":
		p.pos++
		return p.object()
	}
	return "", p.fail("a value")
}

func (p *parser) list() (string, error) {
	var items []string
	for !p.isPunct("]") {
		v, err := p.value()
		if err != nil {
			return "", err
		}
		items = append(items, v)
	}
	p.pos++
	return "[" + strings.Join(items, ",") + "]", nil
}

func (p *parser) object() (string, error) {
	var fields []string
	for !p.isPunct("}") {
		n, err := p.name()
		if err != nil {
			return "", err
		}
		if expectErr := p.expect(":"); expectErr != nil {
			return "", expectErr
		}
		v, err := p.value()
		if err != nil {
			return "", err
		}
		fields = append(fields, n+":"+v)
	}
	p.pos++
	slices.Sort(fields)
	return "{" + strings.Join(fields, ",") + "}", nil
}
