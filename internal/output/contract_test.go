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
