package jsonpath

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type parser struct {
	in  []rune
	pos int
}

func (p *parser) done() bool { return p.pos >= len(p.in) }

func (p *parser) peek() rune {
	if p.done() {
		return 0
	}
	return p.in[p.pos]
}

func (p *parser) peekNext() rune {
	if p.pos+1 >= len(p.in) {
		return 0
	}
	return p.in[p.pos+1]
}

func (p *parser) skipSpace() {
	for !p.done() && unicode.IsSpace(p.in[p.pos]) {
		p.pos++
	}
}

func (p *parser) expect(r rune) error {
	p.skipSpace()
	if p.peek() != r {
		return fmt.Errorf("expected %q at %d", string(r), p.pos)
	}
	p.pos++
	return nil
}

// path parses $ or @ followed by its steps. It stops at the first character that cannot continue
// a path, so the same code reads a whole expression and an operand inside a filter.
func (p *parser) path() (*Path, error) {
	if c := p.peek(); c != '$' && c != '@' {
		return nil, fmt.Errorf("a path must start with $ or @ at %d", p.pos)
	}
	p.pos++
	out := &Path{definite: true}
	for !p.done() {
		more, err := p.next(out)
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
	}
	return out, nil
}

// next reads one step of a path. It returns false when the path has ended.
func (p *parser) next(out *Path) (bool, error) {
	var t token
	var err error
	switch {
	case p.peek() == '.' && p.peekNext() == '.':
		p.pos += 2
		out.definite = false
		if t, err = p.step(true, out); err == nil {
			t = deepScan{next: t}
		}
	case p.peek() == '.':
		p.pos++
		if p.done() {
			return false, errors.New("a path cannot end with a dot")
		}
		if p.peek() == '[' {
			return true, nil
		}
		t, err = p.step(false, out)
		if err == nil && out.function != nil {
			return false, nil // a function ends the path
		}
	case p.peek() == '[':
		t, err = p.bracket(out)
	default:
		return false, nil
	}
	if err != nil {
		return false, err
	}
	out.tokens = append(out.tokens, t)
	return true, nil
}

// step reads what follows a dot: *, a property name, or a function call. A function call is stored on
// the path and step returns a nil token.
func (p *parser) step(scan bool, out *Path) (token, error) {
	if p.peek() == '[' {
		return p.bracket(out)
	}
	if p.peek() == '*' {
		p.pos++
		out.definite = false
		return wildcard{}, nil
	}
	name := p.name()
	if name == "" {
		return nil, fmt.Errorf("expected a property name at %d", p.pos)
	}
	if p.peek() == '(' {
		if scan {
			return nil, errors.New("a function cannot follow a double dot")
		}
		fn, err := p.functionCall(name)
		if err != nil {
			return nil, err
		}
		out.function = fn
		return wildcard{}, nil // not used: the path ends at the function
	}
	return names{name}, nil
}

func (p *parser) name() string {
	start := p.pos
	for !p.done() {
		c := p.peek()
		if unicode.IsSpace(c) || strings.ContainsRune(".[]()',\"?&|=!<>/", c) {
			break
		}
		p.pos++
	}
	return string(p.in[start:p.pos])
}

func (p *parser) functionCall(name string) (*function, error) {
	p.pos++ // (
	fn := &function{name: name}
	p.skipSpace()
	for p.peek() != ')' {
		arg, err := p.operand()
		if err != nil {
			return nil, err
		}
		fn.args = append(fn.args, arg)
		p.skipSpace()
		if p.peek() == ',' {
			p.pos++
			p.skipSpace()
		} else if p.peek() != ')' {
			return nil, fmt.Errorf("expected , or ) at %d", p.pos)
		}
	}
	p.pos++
	if !knownFunction(name) {
		return nil, fmt.Errorf("unknown function %s()", name)
	}
	return fn, nil
}

// bracket reads [ ... ]: a wildcard, a filter, quoted names, indexes or a slice.
func (p *parser) bracket(out *Path) (token, error) {
	p.pos++ // [
	p.skipSpace()
	switch p.peek() {
	case '*':
		p.pos++
		out.definite = false
		return wildcard{}, p.expect(']')
	case '?':
		p.pos++
		out.definite = false
		if err := p.expect('('); err != nil {
			return nil, err
		}
		e, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		if err := p.expect(')'); err != nil {
			return nil, err
		}
		return filter{test: e}, p.expect(']')
	case '\'', '"':
		var list names
		for {
			s, err := p.quoted()
			if err != nil {
				return nil, err
			}
			list = append(list, s)
			p.skipSpace()
			if p.peek() != ',' {
				break
			}
			p.pos++
			p.skipSpace()
		}
		if len(list) > 1 {
			out.definite = false
		}
		return list, p.expect(']')
	}
	return p.indexes(out)
}

func (p *parser) indexes(out *Path) (token, error) {
	start := p.pos
	for !p.done() && p.peek() != ']' {
		p.pos++
	}
	if p.done() {
		return nil, errors.New("unclosed [")
	}
	body := strings.TrimSpace(string(p.in[start:p.pos]))
	p.pos++ // ]
	if body == "" {
		return nil, errors.New("empty []")
	}
	if strings.Contains(body, ":") {
		out.definite = false
		return parseSlice(body)
	}
	var list indexes
	for _, part := range strings.Split(body, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("bad index %q", body)
		}
		list = append(list, n)
	}
	if len(list) > 1 {
		out.definite = false
	}
	return list, nil
}

// parseSlice reads start:end. A step (start:end:step) is read and ignored, as Jayway does.
func parseSlice(body string) (token, error) {
	parts := strings.Split(body, ":")
	if len(parts) > 3 || (len(parts) == 3 && strings.TrimSpace(parts[0]) == "" && strings.TrimSpace(parts[1]) == "") {
		return nil, fmt.Errorf("bad slice %q", body)
	}
	var s slice
	for i, part := range parts[:min(len(parts), 2)] {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("bad slice %q", body)
		}
		if i == 0 {
			s.start = &n
		} else {
			s.end = &n
		}
	}
	return s, nil
}

// quoted reads a 'single' or "double" quoted string, with backslash escapes.
func (p *parser) quoted() (string, error) {
	q := p.peek()
	p.pos++
	var b strings.Builder
	for !p.done() {
		c := p.peek()
		p.pos++
		switch c {
		case q:
			return b.String(), nil
		case '\\':
			if p.done() {
				return "", errors.New("unterminated string")
			}
			e := p.peek()
			p.pos++
			switch e {
			case 'n':
				b.WriteRune('\n')
			case 't':
				b.WriteRune('\t')
			default:
				b.WriteRune(e)
			}
		default:
			b.WriteRune(c)
		}
	}
	return "", errors.New("unterminated string")
}

// orExpr is the filter grammar: || binds loosest, then &&, then ! and parentheses.
func (p *parser) orExpr() (expr, error) {
	left, err := p.andExpr()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.peek() == '|' && p.peekNext() == '|' {
			p.pos += 2
			right, err := p.andExpr()
			if err != nil {
				return nil, err
			}
			left = orExpr{left, right}
			continue
		}
		return left, nil
	}
}

func (p *parser) andExpr() (expr, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for {
		p.skipSpace()
		if p.peek() == '&' && p.peekNext() == '&' {
			p.pos += 2
			right, err := p.unary()
			if err != nil {
				return nil, err
			}
			left = andExpr{left, right}
			continue
		}
		return left, nil
	}
}

func (p *parser) unary() (expr, error) {
	p.skipSpace()
	switch {
	case p.peek() == '!' && p.peekNext() != '=':
		p.pos++
		e, err := p.unary()
		if err != nil {
			return nil, err
		}
		return notExpr{e}, nil
	case p.peek() == '(':
		p.pos++
		e, err := p.orExpr()
		if err != nil {
			return nil, err
		}
		return e, p.expect(')')
	}
	left, err := p.operand()
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	op := p.operator()
	if op == "" {
		po, ok := left.(pathOperand)
		if !ok {
			return nil, errors.New("only a path can be used as a condition")
		}
		return existsExpr{po}, nil
	}
	right, err := p.operand()
	if err != nil {
		return nil, err
	}
	return cmpExpr{left: left, op: op, right: right}, nil
}

func (p *parser) operator() string {
	symbolOps := []string{"==", "!=", "<>", "<=", ">=", "=~", "<", ">"}
	wordOps := []string{"nin", "in", "subsetof", "anyof", "noneof", "contains", opSize, "empty"}
	rest := string(p.in[p.pos:])
	for _, op := range symbolOps {
		if strings.HasPrefix(rest, op) {
			p.pos += len(op)
			if op == "<>" {
				return "!="
			}
			return op
		}
	}
	for _, op := range wordOps {
		if strings.HasPrefix(rest, op) && (len(rest) == len(op) || !isNameRune(rune(rest[len(op)]))) {
			p.pos += len(op)
			return op
		}
	}
	return ""
}

func isNameRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// operand reads a value in a filter: a path, string, number, true, false, null, array or regex.
func (p *parser) operand() (operand, error) {
	p.skipSpace()
	switch c := p.peek(); {
	case c == '@' || c == '$':
		path, err := p.path()
		if err != nil {
			return nil, err
		}
		return pathOperand{path: path, relative: c == '@'}, nil
	case c == '\'' || c == '"':
		s, err := p.quoted()
		return literal{s}, err
	case c == '/':
		return p.regex()
	case c == '[':
		return p.arrayLiteral()
	case c == '-' || unicode.IsDigit(c):
		return p.numberLiteral()
	}
	word := p.name()
	switch word {
	case "true":
		return literal{true}, nil
	case "false":
		return literal{false}, nil
	case "null":
		return literal{nil}, nil
	}
	return nil, fmt.Errorf("unexpected %q at %d", word, p.pos)
}

func (p *parser) numberLiteral() (operand, error) {
	start := p.pos
	for !p.done() && (unicode.IsDigit(p.peek()) || strings.ContainsRune("-+.eE", p.peek())) {
		p.pos++
	}
	f, err := strconv.ParseFloat(string(p.in[start:p.pos]), 64)
	if err != nil {
		return nil, fmt.Errorf("bad number at %d", start)
	}
	return literal{f}, nil
}

func (p *parser) arrayLiteral() (operand, error) {
	p.pos++ // [
	list := []any{}
	p.skipSpace()
	for p.peek() != ']' {
		o, err := p.operand()
		if err != nil {
			return nil, err
		}
		lit, ok := o.(literal)
		if !ok {
			return nil, errors.New("an array literal holds only values")
		}
		list = append(list, lit.v)
		p.skipSpace()
		if p.peek() == ',' {
			p.pos++
		} else if p.peek() != ']' {
			return nil, fmt.Errorf("expected , or ] at %d", p.pos)
		}
		p.skipSpace()
	}
	p.pos++
	return literal{list}, nil
}

// compileRegex reads the flags after the closing slash and compiles the pattern, anchored.
func (p *parser) compileRegex(pattern string) (operand, error) {
	var flags strings.Builder
	for p.peek() != 0 && strings.ContainsRune("isxmudl", p.peek()) {
		if strings.ContainsRune("ism", p.peek()) {
			flags.WriteRune(p.peek())
		}
		p.pos++
	}
	pat := "^(?:" + pattern + ")$"
	if flags.Len() > 0 {
		pat = "(?" + flags.String() + ")" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, fmt.Errorf("bad regular expression: %w", err)
	}
	return regexOperand{re: re}, nil
}

// regex reads /pattern/flags. Jayway matches the pattern against the whole value.
func (p *parser) regex() (operand, error) {
	p.pos++ // /
	var b strings.Builder
	for !p.done() {
		c := p.peek()
		p.pos++
		if c == '\\' && p.peek() == '/' {
			b.WriteRune('/')
			p.pos++
			continue
		}
		if c == '/' {
			return p.compileRegex(b.String())
		}
		b.WriteRune(c)
	}
	return nil, errors.New("unterminated regular expression")
}
