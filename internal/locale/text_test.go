package locale

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTextReadsStringsOrLanguagesAndFallsBack(t *testing.T) {
	var v struct{ A, B, C Text }
	if err := json.Unmarshal([]byte(`{"A": "Effects", "B": {"en": "Effects", "zh": "音效"}, "C": {"zh": "只有中文"}}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.In("zh") != "Effects" || v.B.In("zh") != "音效" || v.B.In("fr") != "Effects" || v.C.In("en") != "只有中文" {
		t.Fatalf("%+v", v)
	}
	if out, _ := json.Marshal(v.A); string(out) != `"Effects"` {
		t.Fatalf("English-only text written as %s", out)
	}
	if err := json.Unmarshal([]byte(`{"A": 3}`), &v); err == nil {
		t.Fatal("a number was accepted as text")
	}
}

func TestJSONErrorNamesTheLine(t *testing.T) {
	data := []byte("{\n  \"a\": 1,\n  \"b\": ]\n}")
	var v any
	err := JSONError(data, json.Unmarshal(data, &v))
	if err == nil || !strings.HasPrefix(err.Error(), "line 3, column 8") {
		t.Fatalf("%v", err)
	}
}
