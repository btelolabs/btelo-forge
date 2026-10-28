package ui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// messages collects every literal passed to t, tf or msgs, and the messages of knownErrors.
func messages(t *testing.T) map[string]string {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "i18n_zh.go" {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		literal := func(e ast.Expr) {
			if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				s, _ := strconv.Unquote(lit.Value)
				found[s] = fset.Position(lit.Pos()).String()
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				name := ""
				switch fn := n.Fun.(type) {
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				case *ast.Ident:
					name = fn.Name
				}
				switch name {
				case "t", "tf":
					if len(n.Args) > 0 {
						literal(n.Args[0])
						if m, ok := n.Args[0].(*ast.IndexExpr); ok {
							if c, ok := m.X.(*ast.CompositeLit); ok {
								for _, e := range c.Elts {
									literal(e.(*ast.KeyValueExpr).Value)
								}
							}
						}
					}
				case "msgs":
					for _, arg := range n.Args {
						literal(arg)
					}
				case "tc", "msgsIn":
					// A message in a context is keyed context|message.
					if len(n.Args) < 2 {
						break
					}
					ctxLit, ok := n.Args[0].(*ast.BasicLit)
					if !ok || ctxLit.Kind != token.STRING {
						break
					}
					context, _ := strconv.Unquote(ctxLit.Value)
					for _, arg := range n.Args[1:] {
						if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							s, _ := strconv.Unquote(lit.Value)
							found[context+"|"+s] = fset.Position(lit.Pos()).String()
						}
					}
				}
			case *ast.ValueSpec:
				if len(n.Names) == 1 && n.Names[0].Name == "knownErrors" {
					ast.Inspect(n, func(m ast.Node) bool {
						if c, ok := m.(*ast.CompositeLit); ok && len(c.Elts) == 2 {
							literal(c.Elts[1])
						}
						return true
					})
				}
			}
			return true
		})
	}
	return found
}

func TestEveryMessageIsTranslated(t *testing.T) {
	used := messages(t)
	digits := func(s string) bool { return strings.Trim(s, "0123456789p") == "" }
	var missing, stale []string
	for s, at := range used {
		if _, ok := zh[s]; !ok && !digits(s) && !slices.Contains(codecShortNames, s) {
			missing = append(missing, at+": "+strconv.Quote(s))
		}
	}
	for s := range zh {
		if _, ok := used[s]; !ok {
			stale = append(stale, strconv.Quote(s))
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("messages without a Chinese translation:\n%s", strings.Join(missing, "\n"))
	}
	if len(stale) > 0 {
		t.Errorf("translations nothing uses any more:\n%s", strings.Join(stale, "\n"))
	}
	for s, z := range zh {
		if strings.Count(s, "%") != strings.Count(z, "%") {
			t.Errorf("%q and its translation %q have different format verbs", s, z)
			continue
		}
		// Filled in with values of the kinds the English asks for, the translation must format cleanly, also where
		// it takes them in another order.
		if args := sampleArgs(s); len(args) > 0 {
			if out := fmt.Sprintf(z, args...); strings.Contains(out, "%!") {
				t.Errorf("the translation %q of %q formats as %q", z, s, out)
			}
		}
	}
}

// sampleArgs are values of the kinds a format's verbs take, in order.
func sampleArgs(format string) []any {
	var args []any
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		j := i + 1
		for j < len(format) && strings.IndexByte("+-# 0123456789.[]*", format[j]) >= 0 {
			j++
		}
		if j >= len(format) {
			break
		}
		switch format[j] {
		case '%':
		case 'd', 'x', 'c':
			args = append(args, 1)
		case 'f', 'g', 'e':
			args = append(args, 1.5)
		default:
			args = append(args, "x")
		}
		i = j
	}
	return args
}
