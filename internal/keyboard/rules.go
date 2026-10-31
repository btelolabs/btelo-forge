package keyboard

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/btelolabs/btelo-forge/internal/locale"
)

// DefaultJSON is the configuration with only examples, as comments.
//
//go:embed rules.json
var DefaultJSON []byte

// FormatGuide explains the configuration to whoever edits it, a person or an AI.
const FormatGuide = `The keyboard rules work in every program while the app runs, once the user has turned them on in the Keyboard
tab, opened from the start page. Only the user can do that: this configuration holds the rules, not the switch, so it can be written ahead of it.
The JSON has version 1 and "rules", a list of text lines; a blank line or one starting with # is skipped. Each line says
one thing:

  CapsLock -> Esc                 a key becomes another, held for as long as it is held
  F1 -> Ctrl+Z                    a key sends a combination
  Ctrl+J -> Down                  a combination sends a key; it repeats while held
  Ctrl+Alt+T -> open "wt.exe"     open a program, file or web address; text after the closing quote is passed to the program
  Ctrl+Alt+D -> type "{date}"     type text; {date} is today and {time} the time
  ;mail => me@example.com         typing the keys on the left replaces them with the text on the right, which may be
                                  quoted to keep its spaces

Keys: A to Z, 0 to 9, F1 to F24, Esc, Enter, Tab, Space, Backspace, Delete, Insert, Home, End, PageUp, PageDown, Left, Up,
Right, Down, CapsLock, NumLock, ScrollLock, PrintScreen, Pause, Menu, Num0 to Num9, Mute, VolumeUp, VolumeDown, PlayPause,
NextTrack, PreviousTrack and punctuation such as ; or /. Modifiers: Ctrl, Alt (Option), Shift, Win (Cmd); Left-Ctrl and
Right-Ctrl name one side, and so on for the others. A combination is joined with +.

An abbreviation is matched by the keys pressed, as on a US keyboard, so it works with any input method on; use characters a
US keyboard types. Keys cannot be remapped in programs run as administrator unless this app is too.`

// Config is the file of keyboard rules.
type Config struct {
	Version int      `json:"version"`
	Rules   []string `json:"rules"`
}

// ParseConfig reads the configuration. Its rules are checked apart, by Parse: a mistake in one is the user's to
// correct, and the file is kept meanwhile.
func ParseConfig(data []byte) (*Config, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, locale.JSONError(data, err)
	}
	if dec.More() {
		return nil, errors.New("unexpected text after the configuration")
	}
	if c.Version != 1 {
		return nil, fmt.Errorf("version must be 1, not %d", c.Version)
	}
	return &c, nil
}

// JSON is the configuration as its file keeps it.
func (c *Config) JSON() string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // -> is the heart of a rule
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		panic(err) // strings and numbers always encode
	}
	return b.String()
}

// Kind is what a rule does.
type Kind int

const (
	SendKeys Kind = iota // press To
	TypeText             // type Text
	OpenItem             // open Text, a program, file or address, with Args
)

// Rule is one line of the rules.
type Rule struct {
	Line int    // 1-based, in the list of lines given to Parse
	Abbr string // the keys to type, when it is an abbreviation; From otherwise
	From Combo
	Kind Kind
	To   Combo
	Text string
	Args string
}

// hold reports whether the rule makes one key stand for another, down for as long as the source is, rather than
// tapping a combination.
func (r *Rule) hold() bool {
	return r.Kind == SendKeys && r.From.plain() && r.To.plain()
}

func (c Combo) plain() bool { return !c.Ctrl && !c.Alt && !c.Shift && !c.Win }

// The problems a rule can have.
const (
	BadLine       = iota // no -> or =>
	BadKey               // Text is not a key or combination
	BadAbbr              // an abbreviation has a character no key types, or is too short
	BadText              // a quoted text is not closed
	EmptyAction          // nothing after the arrow
	DoubleTrigger        // Text is already used by the line Other
	Unavailable          // Text cannot be remapped on this system
	BadRemap             // a system remap has no ->
	NotOneKey            // Text in a system remap is not a single key the table can hold
)

// RuleError says which line of the rules cannot be used and why.
type RuleError struct {
	Line    int
	Problem int
	Text    string
	Other   int
}

func (e *RuleError) Error() string {
	var why string
	switch e.Problem {
	case BadLine:
		why = "write a key, -> and what it does, or an abbreviation, => and its text"
	case BadKey:
		why = fmt.Sprintf("%q is not a key or combination", e.Text)
	case BadAbbr:
		why = fmt.Sprintf("%q cannot be an abbreviation: use at least two characters a keyboard types", e.Text)
	case BadText:
		why = "the quoted text is not closed"
	case EmptyAction:
		why = "nothing follows the arrow"
	case DoubleTrigger:
		why = fmt.Sprintf("%s is already used on line %d", e.Text, e.Other)
	case Unavailable:
		why = fmt.Sprintf("%s cannot be remapped on this system", e.Text)
	case BadRemap:
		why = "write a key, -> and the key it becomes, or None"
	case NotOneKey:
		why = fmt.Sprintf("%q is not a single key the system can remap", e.Text)
	}
	return fmt.Sprintf("rule line %d: %s", e.Line, why)
}

// Parse reads the lines of the rules.
func Parse(lines []string) ([]Rule, error) {
	var rules []Rule
	seen := map[string]int{}
	for i, line := range lines {
		n := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r, perr := parseRule(line)
		if perr != nil {
			perr.Line = n
			return nil, perr
		}
		r.Line = n
		trigger := r.Abbr
		if trigger == "" {
			trigger = r.From.String()
		}
		if other, ok := seen[trigger]; ok {
			return nil, &RuleError{Line: n, Problem: DoubleTrigger, Text: trigger, Other: other}
		}
		seen[trigger] = n
		rules = append(rules, r)
	}
	return rules, nil
}

func cutArrow(line, arrow string) (left, right string, ok bool) {
	if left, right, ok = strings.Cut(line, " "+arrow+" "); ok {
		return left, right, true
	}
	return strings.Cut(line, arrow)
}

func parseRule(line string) (Rule, *RuleError) {
	if left, right, ok := cutArrow(line, "=>"); ok && !strings.Contains(left, "->") {
		left, right = strings.TrimSpace(left), strings.TrimSpace(right)
		return parseAbbr(left, right)
	}
	left, right, ok := cutArrow(line, "->")
	if !ok {
		return Rule{}, &RuleError{Problem: BadLine}
	}
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	var r Rule
	var valid bool
	if r.From, valid = ParseCombo(left); !valid {
		return Rule{}, &RuleError{Problem: BadKey, Text: left}
	}
	if right == "" {
		return Rule{}, &RuleError{Problem: EmptyAction}
	}
	if word, rest, found := cutWord(right); found {
		switch word {
		case "open":
			target, args, perr := quoted(rest)
			if perr != nil {
				return Rule{}, perr
			}
			r.Kind, r.Text, r.Args = OpenItem, target, strings.TrimSpace(args)
			if !strings.HasPrefix(strings.TrimSpace(rest), `"`) {
				r.Text, r.Args, _ = strings.Cut(strings.TrimSpace(rest), " ")
				r.Args = strings.TrimSpace(r.Args)
			}
			return r, nil
		case "type":
			text, _, perr := quoted(rest)
			if perr != nil {
				return Rule{}, perr
			}
			r.Kind, r.Text = TypeText, text
			return r, nil
		}
	}
	if r.To, valid = ParseCombo(right); !valid {
		return Rule{}, &RuleError{Problem: BadKey, Text: right}
	}
	return r, nil
}

func cutWord(s string) (word, rest string, ok bool) {
	i := strings.IndexFunc(s, func(c rune) bool { return unicode.IsSpace(c) || c == '"' })
	if i <= 0 {
		return s, "", false
	}
	return s[:i], s[i:], true
}

func parseAbbr(left, right string) (Rule, *RuleError) {
	r := Rule{Abbr: left, Kind: TypeText}
	if utf8.RuneCountInString(left) < 2 {
		return Rule{}, &RuleError{Problem: BadAbbr, Text: left}
	}
	for _, c := range left {
		if !typable(c) || c == ' ' {
			return Rule{}, &RuleError{Problem: BadAbbr, Text: left}
		}
	}
	if right == "" {
		return Rule{}, &RuleError{Problem: EmptyAction}
	}
	if strings.HasPrefix(right, `"`) {
		text, _, perr := quoted(right)
		if perr != nil {
			return Rule{}, perr
		}
		right = text
	}
	r.Text = right
	return r, nil
}

// quoted reads a text that starts s, in double quotes with \" \\ \n and \t, or else all of s, and what follows it.
func quoted(s string) (text, rest string, err *RuleError) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", &RuleError{Problem: EmptyAction}
	}
	if s[0] != '"' {
		return s, "", nil
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return b.String(), s[i+1:], nil
		case c == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i])
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", "", &RuleError{Problem: BadText}
}
