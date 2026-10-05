package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// selfMarshaling escapes HTML the way the SDK's MarshalJSON methods do: they
// call json.Marshal, which escapes before the encoder sees the bytes.
type selfMarshaling struct{ S string }

func (s selfMarshaling) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{"s": s.S})
}

// TestEncodeJSON_NoHTMLEscaping pins #240: <, > and & print as themselves,
// including inside a type that marshals itself, and a literal backslash
// followed by "u003c" is not mistaken for an escape.
func TestEncodeJSON_NoHTMLEscaping(t *testing.T) {
	var w bytes.Buffer
	c := &Config{Format: FormatJSON, Writer: &w}
	literal := string('\\') + "u003c" // a backslash, then the text u003c
	v := map[string]any{"plain": "a<b & c>d", "sdk": selfMarshaling{"x<y"}, "literal": literal}
	if err := c.JSON(v); err != nil {
		t.Fatal(err)
	}
	got := w.String()
	for _, want := range []string{`"a<b & c>d"`, `"x<y"`, `"` + string('\\') + literal + `"`} {
		if !strings.Contains(got, want) {
			t.Errorf("want %s in:\n%s", want, got)
		}
	}
	var back map[string]any
	if err := json.Unmarshal(w.Bytes(), &back); err != nil || back["literal"] != literal {
		t.Errorf("output does not decode back to the input: %v %v", err, back)
	}
}

// TestSuccessAndUnchanged pins the write result's "changed" field (#240).
func TestSuccessAndUnchanged(t *testing.T) {
	for _, tc := range []struct {
		emit    func(*Config, string)
		changed bool
	}{
		{(*Config).Success, true},
		{(*Config).Unchanged, false},
	} {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Writer: &w}
		tc.emit(c, "done")
		var got map[string]any
		if err := json.Unmarshal(w.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["success"] != true || got["changed"] != tc.changed || got["message"] != "done" {
			t.Errorf(`want {"success": true, "changed": %v, "message": "done"}, got %v`, tc.changed, got)
		}
	}
}

type withHint struct{ error }

func (withHint) UserHint() string { return "do the thing" }

// TestErrorWith_Envelope pins the envelope's shape: type (defaulting to
// "api"), status only when set, the hint inside "error" and, deprecated, beside
// it, and the warnings Warn kept back, which are then forgotten (#240).
func TestErrorWith_Envelope(t *testing.T) {
	var ew bytes.Buffer
	c := &Config{Format: FormatJSON, EWriter: &ew}
	c.Warn("careful")
	c.ErrorWith(withHint{errors.New("boom")}, ErrorInfo{Type: ErrorTypeNotFound, Status: 404})

	var got struct {
		Error    map[string]any `json:"error"`
		Hint     string         `json:"hint"`
		Warnings []string       `json:"warnings"`
	}
	if err := json.Unmarshal(ew.Bytes(), &got); err != nil {
		t.Fatalf("%v\n%s", err, ew.String())
	}
	e := got.Error
	if e["type"] != "not_found" || e["status"] != float64(404) || e["message"] != "boom" || e["hint"] != "do the thing" {
		t.Errorf("unexpected error object: %v", e)
	}
	if got.Hint != "do the thing" {
		t.Errorf("deprecated top-level hint = %q, want it kept for one release", got.Hint)
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "careful" {
		t.Errorf("warnings = %v, want [careful]", got.Warnings)
	}
	if left := c.TakeWarnings(); left != nil {
		t.Errorf("the envelope should have taken the warnings, %v left", left)
	}

	ew.Reset()
	c.Error(errors.New("plain"))
	if !strings.Contains(ew.String(), `"type": "api"`) || strings.Contains(ew.String(), `"status"`) {
		t.Errorf(`an unclassified error should be type "api" with no status, got:\n%s`, ew.String())
	}
}

// TestWarn_StructuredModesKeepStderrOneDocument pins #240: a warning in JSON
// mode is not a plain "! …" line but one {"warnings": [...]} document, printed
// by FlushWarnings, and nothing at all when there were none.
func TestWarn_StructuredModesKeepStderrOneDocument(t *testing.T) {
	var ew bytes.Buffer
	c := &Config{Format: FormatJSON, EWriter: &ew}
	c.FlushWarnings()
	if ew.Len() != 0 {
		t.Fatalf("no warnings should print nothing, got %q", ew.String())
	}
	c.Warn("one")
	c.Warn("two")
	if ew.Len() != 0 {
		t.Fatalf("Warn printed before the flush: %q", ew.String())
	}
	c.FlushWarnings()
	var got struct {
		Warnings []string `json:"warnings"`
	}
	dec := json.NewDecoder(&ew)
	if err := dec.Decode(&got); err != nil || dec.More() || len(got.Warnings) != 2 {
		t.Errorf(`want one {"warnings": ["one", "two"]} document, got %v (%v)`, got, err)
	}
}

// TestResults_OneDocument pins output.Results (#240): a write over several
// targets is one document whose "changed" is true when any target changed,
// one target keeps the plain write-result shape, and table mode prints a
// line per target as it goes.
func TestResults_OneDocument(t *testing.T) {
	var w bytes.Buffer
	c := &Config{Format: FormatJSON, Writer: &w}
	r := c.Results()
	r.Add(ResultItem{Domain: "a.com", Message: "already on"})
	r.Add(ResultItem{Domain: "b.com", Changed: true, Message: "enabled"})
	r.Print("enabled for 1 domain; already on for 1")
	var doc struct {
		Success, Changed bool
		Message          string
		Data             []ResultItem
	}
	dec := json.NewDecoder(&w)
	if err := dec.Decode(&doc); err != nil || dec.More() {
		t.Fatalf("want one document: %v", err)
	}
	if !doc.Success || !doc.Changed || len(doc.Data) != 2 || doc.Data[0].Changed || !doc.Data[1].Changed {
		t.Errorf("got %+v", doc)
	}

	w.Reset()
	r = c.Results()
	r.Add(ResultItem{Domain: "a.com", Message: "already on"})
	r.Print("unused")
	if got := strings.TrimSpace(w.String()); got != "{\n  \"success\": true,\n  \"changed\": false,\n  \"message\": \"already on\"\n}" {
		t.Errorf("one target should print the write result, got:\n%s", got)
	}

	w.Reset()
	c = &Config{Format: FormatTable, Writer: &w, Color: ColorNever}
	r = c.Results()
	r.Add(ResultItem{Message: "one"})
	r.Add(ResultItem{Message: "two", Changed: true})
	r.Print("unused")
	if got := w.String(); got != "✓ one\n✓ two\n" {
		t.Errorf("table mode = %q", got)
	}
}

// TestWithChanged adds "changed" after the object's own keys, and refuses
// anything but an object.
func TestWithChanged(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{map[string]int{"id": 1}, `{"id":1,"changed":false}`},
		{struct{}{}, `{"changed":false}`},
	} {
		got, err := WithChanged(tc.in, false)
		if err != nil || string(got) != tc.want {
			t.Errorf("WithChanged(%v) = %s, %v; want %s", tc.in, got, err, tc.want)
		}
	}
	if _, err := WithChanged([]int{1}, true); err == nil {
		t.Error("an array should be refused")
	}
}
