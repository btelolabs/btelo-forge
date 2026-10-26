// Package locale holds text that configuration files give in several languages, and helpers for reading those files.
package locale

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Text is written in JSON as a plain string (English) or as an object of language codes, such as
// {"en": "Effects", "zh": "音效"}.
type Text map[string]string

// In returns the text for a language, falling back to English and then to any language given.
func (t Text) In(lang string) string {
	if s, ok := t[lang]; ok && s != "" {
		return s
	}
	if s := t["en"]; s != "" {
		return s
	}
	for _, k := range slices.Sorted(maps.Keys(t)) {
		if t[k] != "" {
			return t[k]
		}
	}
	return ""
}

// UnmarshalJSON accepts a string or an object of strings.
func (t *Text) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		*t = Text{"en": s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return errors.New("expected a string or an object of language codes to strings")
	}
	*t = m
	return nil
}

// MarshalJSON writes English-only text back as a plain string.
func (t Text) MarshalJSON() ([]byte, error) {
	if len(t) == 1 {
		if s, ok := t["en"]; ok {
			return json.Marshal(s)
		}
	}
	return json.Marshal(map[string]string(t))
}

// JSONError adds the line and column to an error from decoding data.
func JSONError(data []byte, err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	offset := int64(-1)
	switch {
	case errors.As(err, &syn):
		offset = max(syn.Offset-1, 0) // the offending byte was already read
	case errors.As(err, &typ):
		offset = typ.Offset
	}
	if offset < 0 || offset > int64(len(data)) {
		return err
	}
	line, col := 1, 1
	for _, b := range data[:offset] {
		if b == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return fmt.Errorf("line %d, column %d: %w", line, col, err)
}
