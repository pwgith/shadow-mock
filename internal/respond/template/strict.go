package template

import (
	"fmt"

	"github.com/mailgun/raymond/v2/ast"
	"github.com/mailgun/raymond/v2/parser"
)

// raymond prints nothing for a helper it does not know, as if it were a missing variable. WireMock
// fails the response instead, and a template that silently renders blanks hides the mistake, so
// the engine checks every call that has arguments against the helpers it has.
//
// A bare name such as {{foo}} stays a variable lookup, as in Handlebars.

// isBuiltin reports whether name is a helper Handlebars itself provides.
func isBuiltin(name string) bool {
	switch name {
	case "if", "unless", "each", "with", "lookup", "log":
		return true
	}
	return false
}

// checkHelpers parses plain Handlebars and returns an error naming the first call to a helper that
// does not exist.
func checkHelpers(plain string, known map[string]bool) error {
	prog, err := parser.Parse(plain)
	if err != nil {
		return nil //nolint:nilerr // raymond reports the syntax error itself, with a better message
	}
	if name := firstUnknown(prog, known); name != "" {
		return fmt.Errorf("unknown helper %q", name)
	}
	return nil
}

// firstUnknown returns the first unknown helper called under n, or "".
func firstUnknown(n ast.Node, known map[string]bool) string {
	switch t := n.(type) {
	case *ast.Program:
		if t == nil {
			return ""
		}
		return firstOf(known, t.Body...)
	case *ast.MustacheStatement:
		return unknownInExpression(t.Expression, known)
	case *ast.BlockStatement:
		return firstOf(known, t.Expression, t.Program, t.Inverse)
	case *ast.PartialStatement:
		if name := firstOf(known, t.Params...); name != "" {
			return name
		}
		return unknownInHash(t.Hash, known)
	case *ast.SubExpression:
		return unknownInExpression(t.Expression, known)
	case *ast.Expression:
		return unknownInExpression(t, known)
	}
	return ""
}

func firstOf(known map[string]bool, nodes ...ast.Node) string {
	for _, n := range nodes {
		if name := firstUnknown(n, known); name != "" {
			return name
		}
	}
	return ""
}

func unknownInExpression(e *ast.Expression, known map[string]bool) string {
	if e == nil {
		return ""
	}
	hasHash := e.Hash != nil && len(e.Hash.Pairs) > 0
	if p, ok := e.Path.(*ast.PathExpression); ok && (len(e.Params) > 0 || hasHash) {
		if !p.Data && len(p.Parts) == 1 && !known[p.Original] && !isBuiltin(p.Original) {
			return p.Original
		}
	}
	if name := firstOf(known, e.Params...); name != "" {
		return name
	}
	return unknownInHash(e.Hash, known)
}

func unknownInHash(h *ast.Hash, known map[string]bool) string {
	if h == nil {
		return ""
	}
	for _, pair := range h.Pairs {
		if name := firstUnknown(pair.Val, known); name != "" {
			return name
		}
	}
	return ""
}
