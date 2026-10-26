package ui

import (
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
		}
	}
}
