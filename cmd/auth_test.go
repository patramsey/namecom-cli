package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/patramsey/namecom-cli/internal/output"
	"gopkg.in/yaml.v3"
)

// TestAuthStatus_StructuredOutputIsParseable guards the canonical CI
// credential check.
//
// runAuthStatus called out.Success (which now emits a JSON envelope in
// structured modes) and then out.KVTable, which has no format guard at all — so
// `namecom auth status -o json` produced a JSON object followed by an ASCII
// table. Anything piping it to jq fails on the second document.
//
// This is the command a deploy script runs to verify credentials before doing
// real work, so unparseable output is worse here than almost anywhere else.
func TestAuthStatus_StructuredOutputIsParseable(t *testing.T) {
	// Rendering is exercised directly: runAuthStatus needs a live API round
	// trip for the Hello call, which is not the property under test.
	rows := [][]string{
		{"Profile", "prod"},
		{"Username", "alice"},
		{"Environment", "production"},
		{"Config file", "/tmp/config.yaml"},
	}

	t.Run("json", func(t *testing.T) {
		var buf bytes.Buffer
		out := &output.Config{Format: output.FormatJSON, Color: output.ColorNever,
			Writer: &buf, EWriter: &bytes.Buffer{}}
		renderAuthStatus(out, rows)

		var env map[string]any
		if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("auth status -o json is not parseable: %v\noutput:\n%s", err, buf.String())
		}
		if env["profile"] != "prod" || env["username"] != "alice" {
			t.Errorf("structured output lost fields: %#v", env)
		}
	})

	t.Run("yaml", func(t *testing.T) {
		var buf bytes.Buffer
		out := &output.Config{Format: output.FormatYAML, Color: output.ColorNever,
			Writer: &buf, EWriter: &bytes.Buffer{}}
		renderAuthStatus(out, rows)

		var env map[string]any
		if err := yaml.Unmarshal(buf.Bytes(), &env); err != nil {
			t.Fatalf("auth status -o yaml is not parseable: %v\noutput:\n%s", err, buf.String())
		}
		if env["environment"] != "production" {
			t.Errorf("structured output lost fields: %#v", env)
		}
	})

	t.Run("table keeps the human view", func(t *testing.T) {
		var buf bytes.Buffer
		out := &output.Config{Format: output.FormatTable, Color: output.ColorNever,
			Writer: &buf, EWriter: &bytes.Buffer{}}
		renderAuthStatus(out, rows)
		got := buf.String()
		if !strings.Contains(got, "Profile") || !strings.Contains(got, "alice") {
			t.Errorf("table output should show the detail rows, got:\n%s", got)
		}
	})
}

// TestKVTable_SuppressedInStructuredModes guards the shared helper rather than
// each caller. KVTable writes an ASCII table to stdout unconditionally; every
// other renderer (Success, Hint, Step, Count, Empty, Title) gates on format.
// One unguarded helper is enough to corrupt any command that forgets a switch.
func TestKVTable_SuppressedInStructuredModes(t *testing.T) {
	for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			c := &output.Config{Format: format, Color: output.ColorNever,
				Writer: &buf, EWriter: &bytes.Buffer{}}
			c.KVTable([][]string{{"Profile", "prod"}})
			if buf.Len() != 0 {
				t.Errorf("KVTable emitted plain text in %s mode, corrupting the stream:\n%s",
					format, buf.String())
			}
		})
	}
}
