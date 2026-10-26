package library

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// A template is text with {expressions} that read values from a JSON response, as in "{title}" or
// "{genres|join: · }". An expression is one or more comma-separated paths (the first non-empty one is used) followed
// by filters separated by "|"; "{{" and "}}" are literal braces.
type template struct {
	source string
	parts  []part
}

type part struct {
	text    string // literal text when expr is nil
	expr    *expr
	literal bool
}

type expr struct {
	paths   [][]string // the first element of a path names a scope when it starts with "$"
	filters []filter
}

type filter struct {
	name string
	arg  string
}

// filters each take the value so far and return the next; lists are handled element by element unless noted.
var filterNames = map[string]bool{
	"path": true, "query": true, "map": true, "split": true, "join": true, "trim": true, "lower": true, "upper": true,
	"first": true, "take": true, "drop": true, "replace": true, "ext": true, "ifset": true, "ifunset": true,
	"default": true, "strip": true, "unsuffix": true, "quote": true, "words": true,
}

// filtersWithArgument must be given ":argument".
var filtersWithArgument = map[string]bool{"map": true, "split": true, "join": true, "take": true, "drop": true,
	"replace": true, "ifset": true, "ifunset": true, "default": true, "strip": true, "unsuffix": true}

func compile(s string, maps map[string]map[string]string) (template, error) {
	t := template{source: s}
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			t.parts = append(t.parts, part{text: lit.String(), literal: true})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '{' && i+1 < len(s) && s[i+1] == '{':
			lit.WriteByte('{')
			i++
		case c == '}' && i+1 < len(s) && s[i+1] == '}':
			lit.WriteByte('}')
			i++
		case c == '}':
			return t, fmt.Errorf("unmatched } at %d (write }} for a brace)", i)
		case c == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				return t, fmt.Errorf("unclosed { at %d", i)
			}
			e, err := parseExpr(s[i+1:i+end], maps)
			if err != nil {
				return t, fmt.Errorf("{%s}: %w", s[i+1:i+end], err)
			}
			flush()
			t.parts = append(t.parts, part{expr: &e})
			i += end
		default:
			lit.WriteByte(c)
		}
	}
	flush()
	return t, nil
}

func parseExpr(s string, maps map[string]map[string]string) (expr, error) {
	fields := strings.Split(s, "|")
	var e expr
	for _, alt := range strings.Split(fields[0], ",") {
		p := strings.TrimSpace(alt)
		if p == "" {
			return expr{}, fmt.Errorf("empty path")
		}
		path := strings.Split(p, ".")
		if strings.HasPrefix(p, "$") && !scopeNames[path[0]] {
			return expr{}, fmt.Errorf("unknown scope %s", path[0])
		}
		if path[0] == "$let" && len(path) < 2 {
			return expr{}, fmt.Errorf("$let needs a name, as in $let.file")
		}
		e.paths = append(e.paths, path)
	}
	for _, f := range fields[1:] {
		name, arg, has := strings.Cut(f, ":")
		name = strings.TrimSpace(name)
		if !filterNames[name] {
			return expr{}, fmt.Errorf("unknown filter %q", name)
		}
		if filtersWithArgument[name] && !has {
			return expr{}, fmt.Errorf("filter %s needs an argument (%s:…)", name, name)
		}
		switch name {
		case "map":
			if _, ok := maps[arg]; !ok {
				return expr{}, fmt.Errorf("no map named %q", arg)
			}
		case "take":
			if n, err := strconv.Atoi(arg); err != nil || n < 0 {
				return expr{}, fmt.Errorf("take needs a count, not %q", arg)
			}
		case "replace":
			if !strings.Contains(arg, ",") {
				return expr{}, fmt.Errorf("replace needs old,new")
			}
		}
		e.filters = append(e.filters, filter{name, arg})
	}
	return e, nil
}

// scopeNames are the roots an expression can start from besides the current item.
var scopeNames = map[string]bool{"$query": true, "$page": true, "$offset": true, "$limit": true, "$fetch": true,
	"$parent": true, "$doc": true, "$let": true}

// scope is what expressions read: the current result item, the search hit it was expanded from, the whole
// expanded response, values picked with "let" and the request variables.
type scope struct {
	item, parent, doc any
	lets              map[string]any
	vars              map[string]string
	maps              map[string]map[string]string
}

// value is a rendered value: a single string, or a list when the source was a JSON array or after split.
type value struct {
	list   []string
	isList bool
	one    string
}

func (v value) String() string {
	if v.isList {
		return strings.Join(v.list, ", ")
	}
	return v.one
}

func (v value) empty() bool {
	if v.isList {
		for _, s := range v.list {
			if s != "" {
				return false
			}
		}
		return true
	}
	return v.one == ""
}

func (t template) render(s *scope) string {
	if len(t.parts) == 1 && t.parts[0].literal {
		return t.parts[0].text
	}
	var b strings.Builder
	for _, p := range t.parts {
		if p.literal {
			b.WriteString(p.text)
			continue
		}
		b.WriteString(p.expr.eval(s).String())
	}
	return b.String()
}

func (e *expr) eval(s *scope) value {
	var v value
	for _, p := range e.paths {
		if v = toValue(lookup(s, p)); !v.empty() {
			break
		}
	}
	for _, f := range e.filters {
		v = f.apply(v, s)
	}
	return v
}

func lookup(s *scope, p []string) any {
	var root any
	rest := p
	switch p[0] {
	case "$query", "$page", "$offset", "$limit", "$fetch":
		return s.vars[p[0][1:]]
	case "$parent":
		root, rest = s.parent, p[1:]
	case "$doc":
		root, rest = s.doc, p[1:]
	case "$let":
		root, rest = s.lets[p[1]], p[2:]
	default:
		root = s.item
	}
	return walk(root, rest)
}

// walk follows a dotted path through decoded JSON; numbers index arrays.
func walk(v any, p []string) any {
	for _, key := range p {
		switch x := v.(type) {
		case map[string]any:
			v = x[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(x) {
				return nil
			}
			v = x[i]
		default:
			return nil
		}
	}
	return v
}

func scalar(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func toValue(v any) value {
	if list, ok := v.([]any); ok {
		out := value{isList: true}
		for _, x := range list {
			out.list = append(out.list, scalar(x))
		}
		return out
	}
	return value{one: scalar(v)}
}

func (v value) each(f func(string) string) value {
	if !v.isList {
		return value{one: f(v.one)}
	}
	out := value{isList: true, list: make([]string, len(v.list))}
	for i, s := range v.list {
		out.list[i] = f(s)
	}
	return out
}

func (f filter) apply(v value, s *scope) value {
	switch f.name {
	case "path":
		return v.each(url.PathEscape)
	case "query":
		return v.each(url.QueryEscape)
	case "map":
		m := s.maps[f.arg]
		return v.each(func(x string) string { return m[x] })
	case "trim":
		return v.each(strings.TrimSpace)
	case "lower":
		return v.each(strings.ToLower)
	case "upper":
		return v.each(strings.ToUpper)
	case "strip":
		return v.each(func(x string) string { return strings.TrimPrefix(x, f.arg) })
	case "unsuffix":
		return v.each(func(x string) string {
			if len(x) >= len(f.arg) && strings.EqualFold(x[len(x)-len(f.arg):], f.arg) {
				return x[:len(x)-len(f.arg)]
			}
			return x
		})
	case "quote":
		return v.each(strconv.Quote)
	case "ext":
		return v.each(func(x string) string {
			x, _, _ = strings.Cut(x, "?")
			return strings.ToLower(strings.TrimPrefix(path.Ext(x), "."))
		})
	case "replace":
		old, repl, _ := strings.Cut(f.arg, ",")
		return v.each(func(x string) string { return strings.ReplaceAll(x, old, repl) })
	case "split":
		var out []string
		for _, x := range listOf(v) {
			out = append(out, strings.Split(x, f.arg)...)
		}
		return value{isList: true, list: out}
	case "words":
		var out []string
		for _, x := range listOf(v) {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
		return value{isList: true, list: out}
	case "join":
		return value{one: strings.Join(nonEmpty(listOf(v)), f.arg)}
	case "first":
		for _, x := range listOf(v) {
			if x != "" {
				return value{one: x}
			}
		}
		return value{}
	case "take":
		n, _ := strconv.Atoi(f.arg)
		list := listOf(v)
		return value{isList: true, list: list[:min(n, len(list))]}
	case "drop":
		skip := map[string]bool{}
		for _, w := range strings.Split(f.arg, ",") {
			skip[strings.ToLower(strings.TrimSpace(w))] = true
		}
		var out []string
		for _, x := range listOf(v) {
			if !skip[strings.ToLower(strings.TrimSpace(x))] {
				out = append(out, x)
			}
		}
		return value{isList: true, list: out}
	case "ifset":
		if v.empty() {
			return value{}
		}
		return value{one: strings.ReplaceAll(f.arg, "%s", v.String())}
	case "ifunset":
		if v.empty() {
			return value{one: f.arg}
		}
		return value{}
	case "default":
		if v.empty() {
			return value{one: f.arg}
		}
		return v
	}
	return v
}

func listOf(v value) []string {
	if v.isList {
		return v.list
	}
	return []string{v.one}
}

func nonEmpty(list []string) []string {
	var out []string
	for _, s := range list {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// glob matches s against a pattern where * stands for any run of characters, ignoring case; "*" alone requires a
// non-empty value.
func glob(pattern, s string) bool {
	if pattern == "*" {
		return s != ""
	}
	p, s := strings.ToLower(pattern), strings.ToLower(s)
	parts := strings.Split(p, "*")
	if len(parts) == 1 {
		return p == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, mid := range parts[1 : len(parts)-1] {
		i := strings.Index(s, mid)
		if i < 0 {
			return false
		}
		s = s[i+len(mid):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
