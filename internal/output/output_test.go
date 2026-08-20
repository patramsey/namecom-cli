package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"
)

// noColor returns a Config with color disabled — tests the pure string logic.
func noColor() *Config { return &Config{Color: ColorNever} }

// ---- relativeTime ---------------------------------------------------------

func TestRelativeTime(t *testing.T) {
	tests := []struct {
		days float64
		want string
	}{
		{-3.0, "3 days ago"},
		{-1.0, "1 day ago"},
		{-0.5, "expired today"}, // past but < 1 day
		{0.0, "today"},
		{0.3, "today"},
		{1.4, "in 1 day"},
		{2.0, "in 2 days"},
		{14.0, "in 14 days"},
		{60.0, "in 60 days"},
	}
	for _, tt := range tests {
		if got := relativeTime(tt.days); got != tt.want {
			t.Errorf("relativeTime(%.1f) = %q, want %q", tt.days, got, tt.want)
		}
	}
}

// Rounding: values that land exactly on .5 should round to the nearer integer.
func TestRelativeTime_Rounding(t *testing.T) {
	// 6.9 days → rounds to 7
	if got := relativeTime(6.9); got != "in 7 days" {
		t.Errorf("relativeTime(6.9) = %q, want 'in 7 days'", got)
	}
	// -1.4 → rounds to 1 → "1 day ago"
	if got := relativeTime(-1.4); got != "1 day ago" {
		t.Errorf("relativeTime(-1.4) = %q, want '1 day ago'", got)
	}
}

// ---- BoolBadge ------------------------------------------------------------

func TestBoolBadge_NoColor(t *testing.T) {
	c := noColor()
	if got := c.BoolBadge(true); got != "yes" {
		t.Errorf("BoolBadge(true) = %q, want %q", got, "yes")
	}
	if got := c.BoolBadge(false); got != "no" {
		t.Errorf("BoolBadge(false) = %q, want %q", got, "no")
	}
}

func TestBoolBadge_Color(t *testing.T) {
	c := &Config{Color: ColorAlways}
	// Color output should contain the text and the indicator symbol.
	if got := c.BoolBadge(true); !strings.Contains(got, "yes") || !strings.Contains(got, "✓") {
		t.Errorf("BoolBadge(true) = %q, want ✓ and 'yes'", got)
	}
	if got := c.BoolBadge(false); !strings.Contains(got, "no") || !strings.Contains(got, "✗") {
		t.Errorf("BoolBadge(false) = %q, want ✗ and 'no'", got)
	}
}

// ---- AvailabilityBadge ----------------------------------------------------

func TestAvailabilityBadge_NoColor(t *testing.T) {
	c := noColor()
	if got := c.AvailabilityBadge(true); got != "✓ available" {
		t.Errorf("AvailabilityBadge(true) = %q", got)
	}
	if got := c.AvailabilityBadge(false); got != "✗ taken" {
		t.Errorf("AvailabilityBadge(false) = %q", got)
	}
}

// ---- StatusBadge ----------------------------------------------------------

func TestStatusBadge_NoColor(t *testing.T) {
	c := noColor()
	// No-color path returns the raw status string unchanged.
	for _, s := range []string{"active", "pending", "completed", "failed", "UNKNOWN"} {
		if got := c.StatusBadge(s); got != s {
			t.Errorf("StatusBadge(%q) = %q, want unchanged", s, got)
		}
	}
}

func TestStatusBadge_Color(t *testing.T) {
	c := &Config{Color: ColorAlways}
	// Color output should still contain the original status text.
	for _, s := range []string{"active", "pending", "completed", "failed", "canceled"} {
		if got := c.StatusBadge(s); !strings.Contains(got, s) {
			t.Errorf("StatusBadge(%q) = %q, want status text present", s, got)
		}
	}
}

func TestStatusBadge_PrefixMatch(t *testing.T) {
	// "pending_transfer" should resolve via the "pending" prefix.
	c := &Config{Color: ColorAlways}
	got := c.StatusBadge("pending_transfer")
	if !strings.Contains(got, "pending_transfer") {
		t.Errorf("StatusBadge(pending_transfer) = %q, want text present", got)
	}
}

// ---- TypeBadge ------------------------------------------------------------

func TestTypeBadge_NoColor(t *testing.T) {
	c := noColor()
	for _, typ := range []string{"A", "MX", "TXT", "CNAME", "NS", "SRV", "UNKNOWN"} {
		if got := c.TypeBadge(typ); got != typ {
			t.Errorf("TypeBadge(%q) = %q, want unchanged", typ, got)
		}
	}
}

func TestTypeBadge_Color_KnownType(t *testing.T) {
	c := &Config{Color: ColorAlways}
	for _, typ := range []string{"A", "AAAA", "MX", "TXT", "CNAME", "NS"} {
		got := c.TypeBadge(typ)
		if !strings.Contains(got, typ) {
			t.Errorf("TypeBadge(%q) = %q, want type name present", typ, got)
		}
	}
}

func TestTypeBadge_Color_LowercaseInput(t *testing.T) {
	c := &Config{Color: ColorAlways}
	// Lowercase input should match the uppercase palette key.
	got := c.TypeBadge("a")
	if !strings.Contains(got, "A") {
		t.Errorf("TypeBadge('a') = %q, want uppercased 'A'", got)
	}
}

// ---- ExpiryDate -----------------------------------------------------------

func TestExpiryDate_Nil(t *testing.T) {
	if got := noColor().ExpiryDate(nil); got != "" {
		t.Errorf("ExpiryDate(nil) = %q, want empty", got)
	}
}

func TestExpiryDate_NoColor(t *testing.T) {
	c := noColor()

	// Past date — output contains "ago".
	past := time.Now().Add(-72 * time.Hour)
	if got := c.ExpiryDate(&past); !strings.Contains(got, "ago") {
		t.Errorf("ExpiryDate(3 days ago) = %q, want 'ago'", got)
	}

	// Imminent (< 7 days) — output contains "in N days".
	soon := time.Now().Add(5 * 24 * time.Hour)
	if got := c.ExpiryDate(&soon); !strings.Contains(got, "in 5 days") {
		t.Errorf("ExpiryDate(5 days) = %q, want 'in 5 days'", got)
	}

	// Within 30 days — output contains "in N days".
	medium := time.Now().Add(20 * 24 * time.Hour)
	if got := c.ExpiryDate(&medium); !strings.Contains(got, "in 20 days") {
		t.Errorf("ExpiryDate(20 days) = %q, want 'in 20 days'", got)
	}

	// Far future (≥ 30 days) — output contains "in N days".
	far := time.Now().Add(60 * 24 * time.Hour)
	if got := c.ExpiryDate(&far); !strings.Contains(got, "in 60 days") {
		t.Errorf("ExpiryDate(60 days) = %q, want 'in 60 days'", got)
	}
}

func TestExpiryDate_IncludesFormattedDate(t *testing.T) {
	c := noColor()
	// The date portion "YYYY-MM-DD" should always be present.
	d := time.Date(2027, 3, 15, 0, 0, 0, 0, time.UTC)
	got := c.ExpiryDate(&d)
	if !strings.Contains(got, "2027-03-15") {
		t.Errorf("ExpiryDate = %q, want formatted date '2027-03-15'", got)
	}
}

// ---- SandboxTag -------------------------------------------------------------

func TestSandboxTag_NoColor(t *testing.T) {
	c := &Config{Color: ColorNever, Sandbox: true}
	if got := c.SandboxTag(); got != "[sandbox] " {
		t.Errorf("SandboxTag() = %q, want %q", got, "[sandbox] ")
	}
}

func TestSandboxTag_NotSandbox(t *testing.T) {
	// Production (Sandbox: false) must render no tag at all, in either color mode.
	for _, color := range []ColorMode{ColorNever, ColorAlways} {
		c := &Config{Color: color, Sandbox: false}
		if got := c.SandboxTag(); got != "" {
			t.Errorf("SandboxTag() with Color=%v, Sandbox=false = %q, want empty", color, got)
		}
	}
}

func TestSandboxTag_Color(t *testing.T) {
	c := &Config{Color: ColorAlways, Sandbox: true}
	if got := c.SandboxTag(); !strings.Contains(got, "[sandbox]") {
		t.Errorf("SandboxTag() = %q, want it to contain '[sandbox]'", got)
	}
}

// ---- Success / Title sandbox tagging ----------------------------------------

func TestSuccess_SandboxTag(t *testing.T) {
	var buf bytes.Buffer
	c := &Config{Color: ColorNever, Writer: &buf, Sandbox: true}
	c.Success("Registered acme.io")
	if got := buf.String(); !strings.Contains(got, "[sandbox]") || !strings.Contains(got, "Registered acme.io") {
		t.Errorf("Success() output = %q, want it to contain '[sandbox]' and the message", got)
	}
}

func TestSuccess_NoSandboxTagInProduction(t *testing.T) {
	var buf bytes.Buffer
	c := &Config{Color: ColorNever, Writer: &buf, Sandbox: false}
	c.Success("Registered acme.io")
	if got := buf.String(); strings.Contains(got, "[sandbox]") {
		t.Errorf("Success() output = %q, want no '[sandbox]' tag in production", got)
	}
}

func TestTitle_SandboxTag(t *testing.T) {
	var buf bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &buf, Sandbox: true}
	c.Title("acme.io")
	if got := buf.String(); !strings.Contains(got, "[sandbox]") || !strings.Contains(got, "acme.io") {
		t.Errorf("Title() output = %q, want it to contain '[sandbox]' and the name", got)
	}
}

func TestTitle_NoSandboxTagInProduction(t *testing.T) {
	var buf bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &buf, Sandbox: false}
	c.Title("acme.io")
	if got := buf.String(); strings.Contains(got, "[sandbox]") {
		t.Errorf("Title() output = %q, want no '[sandbox]' tag in production", got)
	}
}

// TestError_StructuredFormats pins that errors are machine-readable in every
// structured output mode. Error() had a JSON branch but no YAML one, so
// `-o yaml` emitted a decorated plain-text line that no parser could consume —
// a silent asymmetry between two formats the CLI advertises equally.
func TestError_StructuredFormats(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		var ew bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
		c.Error(errors.New("something broke"))
		var env map[string]any
		if err := json.Unmarshal(ew.Bytes(), &env); err != nil {
			t.Fatalf("JSON error output is not parseable: %v\n%s", err, ew.String())
		}
		if env["error"] == nil {
			t.Errorf("expected an \"error\" key, got: %s", ew.String())
		}
	})

	// Use a message containing a colon — the overwhelmingly common shape, since
	// every wrapped error is fmt.Errorf("...: %w"). The plain-text fallback
	// emits a bare "error: <msg>" line, which parses as YAML only by accident
	// and stops doing so the moment the message itself contains ": ".
	realistic := errors.New("creating A www: Invalid answer (details: out of range)")

	t.Run("yaml", func(t *testing.T) {
		var ew bytes.Buffer
		c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
		c.Error(realistic)
		got := ew.String()
		var env map[string]any
		if err := yaml.Unmarshal(ew.Bytes(), &env); err != nil {
			t.Fatalf("YAML error output is not parseable: %v\n%s", err, got)
		}
		if env["error"] == nil {
			t.Errorf("expected an \"error\" key, got: %s", got)
		}
	})

	t.Run("json with colons", func(t *testing.T) {
		var ew bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &ew}
		c.Error(realistic)
		var env map[string]any
		if err := json.Unmarshal(ew.Bytes(), &env); err != nil {
			t.Fatalf("JSON error output is not parseable: %v\n%s", err, ew.String())
		}
	})
}

// TestSuccess_StructuredFormats guards a scripting bug. Success printed
// "✓ <msg>" to STDOUT unconditionally, and the mutating commands that have no
// format switch of their own — domain lock/autorenew/privacy/set-ns/contacts
// set, dns delete, dns import, dnssec delete, email delete, url delete,
// vanity-ns delete, transfer cancel, auth status, config use — call only
// Success. Since DefaultConfig() picks JSON whenever stdout is not a TTY,
// `namecom dns delete d.com 123 -y | jq .` received "✓ Deleted record 123"
// and failed to parse.
//
// Its siblings Hint, Step, Count, Empty and Spin all already guard on format;
// Success was the one that did not.
func TestSuccess_StructuredFormats(t *testing.T) {
	t.Run("json is parseable", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatJSON, Color: ColorNever, Writer: &w, EWriter: &bytes.Buffer{}}
		c.Success("Deleted record 123 from example.com")

		var env map[string]any
		if err := json.Unmarshal(w.Bytes(), &env); err != nil {
			t.Fatalf("success output is not parseable JSON: %v\n%s", err, w.String())
		}
		if env["success"] != true {
			t.Errorf(`expected "success": true, got: %s`, w.String())
		}
		if env["message"] != "Deleted record 123 from example.com" {
			t.Errorf("message not preserved, got: %s", w.String())
		}
	})

	t.Run("yaml is parseable", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &w, EWriter: &bytes.Buffer{}}
		c.Success("Deleted record 123")

		var env map[string]any
		if err := yaml.Unmarshal(w.Bytes(), &env); err != nil {
			t.Fatalf("success output is not parseable YAML: %v\n%s", err, w.String())
		}
		if env["success"] != true {
			t.Errorf(`expected success: true, got: %s`, w.String())
		}
	})

	t.Run("table keeps the human form", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, Writer: &w, EWriter: &bytes.Buffer{}}
		c.Success("Deleted record 123")
		if !strings.Contains(w.String(), "✓ Deleted record 123") {
			t.Errorf("table mode should keep the checkmark line, got: %q", w.String())
		}
	})

	t.Run("quiet emits nothing", func(t *testing.T) {
		var w bytes.Buffer
		c := &Config{Format: FormatTable, Color: ColorNever, QuietMode: true, Writer: &w, EWriter: &bytes.Buffer{}}
		c.Success("Deleted record 123")
		if w.Len() != 0 {
			t.Errorf("--quiet should emit nothing on success, got: %q", w.String())
		}
	})
}

// ---- flag value parsing -----------------------------------------------------

// ParseFormat and ParseColorMode validate --output and --color. Both lowercase
// their input, so mixed case must be accepted — nothing previously proved that,
// and `--output JSON` silently erroring would be a poor way to find out.
func TestParseFormat(t *testing.T) {
	tests := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"table", FormatTable, false},
		{"json", FormatJSON, false},
		{"yaml", FormatYAML, false},
		{"JSON", FormatJSON, false},
		{"YaMl", FormatYAML, false},
		{"TABLE", FormatTable, false},
		{"", "", true},
		{"xml", "", true},
		{"jsonl", "", true},
		{" json", "", true}, // not trimmed: a stray space is a real mistake, not a synonym
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseFormat(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseFormat(%q) = %q, want an error", tt.in, got)
				}
				// The message has to name the valid choices; "unknown format"
				// alone leaves the user guessing.
				for _, want := range []string{"table", "json", "yaml"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q should list %q as a valid choice", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFormat(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseColorMode(t *testing.T) {
	tests := []struct {
		in      string
		want    ColorMode
		wantErr bool
	}{
		{"auto", ColorAuto, false},
		{"always", ColorAlways, false},
		{"never", ColorNever, false},
		{"ALWAYS", ColorAlways, false},
		{"Never", ColorNever, false},
		{"", "", true},
		{"yes", "", true},
		{"true", "", true},
		{"none", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseColorMode(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseColorMode(%q) = %q, want an error", tt.in, got)
				}
				for _, want := range []string{"auto", "always", "never"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q should list %q as a valid choice", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseColorMode(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseColorMode(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// ---- color helpers ----------------------------------------------------------

// Red and Amber must return the text unchanged when color is off. A helper that
// emits escape sequences regardless would corrupt piped output and any file
// written with --debug-file.
func TestRedAmber_NoColorReturnsPlainText(t *testing.T) {
	c := noColor()
	for _, tt := range []struct {
		name string
		got  string
	}{
		{"Red", c.Red("failed")},
		{"Amber", c.Amber("careful")},
	} {
		if strings.ContainsRune(tt.got, '\x1b') {
			t.Errorf("%s with color disabled returned an escape sequence: %q", tt.name, tt.got)
		}
	}
	if got := c.Red("failed"); got != "failed" {
		t.Errorf("Red = %q, want %q unchanged", got, "failed")
	}
	if got := c.Amber("careful"); got != "careful" {
		t.Errorf("Amber = %q, want %q unchanged", got, "careful")
	}
}

func TestRedAmber_ColorWrapsButPreservesText(t *testing.T) {
	c := &Config{Color: ColorAlways}
	if got := c.Red("failed"); !strings.Contains(got, "failed") {
		t.Errorf("Red = %q, want it to still contain %q", got, "failed")
	}
	if got := c.Amber("careful"); !strings.Contains(got, "careful") {
		t.Errorf("Amber = %q, want it to still contain %q", got, "careful")
	}
}

// ---- DefaultConfig ----------------------------------------------------------

// DefaultConfig decides the output format from whether stdout is a terminal.
// Under `go test` stdout is a pipe, which is the same situation as any script
// or agent invoking the CLI — so this asserts the machine-readable default that
// piping is supposed to produce.
func TestDefaultConfig_NonTTYDefaultsToJSON(t *testing.T) {
	c := DefaultConfig()
	if c.Format != FormatJSON {
		t.Errorf("Format = %q with a non-TTY stdout, want %q — piped output must be machine-readable", c.Format, FormatJSON)
	}
	if c.Color != ColorAuto {
		t.Errorf("Color = %q, want %q", c.Color, ColorAuto)
	}
	if c.Writer == nil || c.EWriter == nil {
		t.Error("DefaultConfig must populate both writers")
	}
}

// ---- ColorEnabled -----------------------------------------------------------

// NO_COLOR is presence-based by specification (https://no-color.org): an empty
// value still disables colour. Treating it as a boolean would re-enable colour
// for `NO_COLOR=` and `NO_COLOR=0`, which the spec explicitly forbids.
//
// Every presence case below ALSO sets CLICOLOR_FORCE=1. Without it these
// assertions are vacuous: under `go test` stdout is not a TTY, so ColorEnabled
// falls through to false no matter what NO_COLOR does, and a boolean reading of
// NO_COLOR passes anyway. With CLICOLOR_FORCE=1 the two readings diverge —
// correct code returns false because NO_COLOR is checked first, a boolean
// reading returns true — so the test can fail, and it simultaneously pins that
// NO_COLOR outranks CLICOLOR_FORCE.
func TestColorEnabled_ExplicitModesIgnoreEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if !(&Config{Color: ColorAlways}).ColorEnabled() {
		t.Error("ColorAlways must ignore NO_COLOR")
	}
	_ = os.Unsetenv("NO_COLOR")
	t.Setenv("CLICOLOR_FORCE", "1")
	if (&Config{Color: ColorNever}).ColorEnabled() {
		t.Error("ColorNever must ignore CLICOLOR_FORCE")
	}
}

func TestColorEnabled_NoColorIsPresenceBasedAndOutranksForce(t *testing.T) {
	for _, val := range []string{"1", "0", "", "false", "no"} {
		t.Run("NO_COLOR="+val, func(t *testing.T) {
			t.Setenv("CLICOLOR_FORCE", "1")
			t.Setenv("NO_COLOR", val)
			if (&Config{Color: ColorAuto}).ColorEnabled() {
				t.Errorf("NO_COLOR=%q must disable colour even with CLICOLOR_FORCE=1 — "+
					"presence disables, regardless of value", val)
			}
		})
	}
}

func TestColorEnabled_ForceEnablesWithoutNoColor(t *testing.T) {
	_ = os.Unsetenv("NO_COLOR")
	t.Setenv("CLICOLOR_FORCE", "1")
	if !(&Config{Color: ColorAuto}).ColorEnabled() {
		t.Error("CLICOLOR_FORCE=1 should enable colour when NO_COLOR is absent")
	}
}

func TestColorEnabled_AutoIsOffWhenPipedWithNoEnv(t *testing.T) {
	_ = os.Unsetenv("NO_COLOR")
	_ = os.Unsetenv("CLICOLOR_FORCE")
	if (&Config{Color: ColorAuto}).ColorEnabled() {
		t.Error("ColorAuto should be off when stdout is a pipe and no env forces it")
	}
}

// ---- YAMLList ---------------------------------------------------------------

// The pagination envelope is a documented output contract: nextPage is omitted
// when there is no next page, so a script can test for its presence rather than
// comparing it to zero.
func TestYAMLList_PaginationEnvelope(t *testing.T) {
	tests := []struct {
		name     string
		nextPage *int32
		total    int32
		wantNext bool
	}{
		{"no next page", nil, 3, false},
		{"explicit zero is not a next page", int32Ptr(0), 3, false},
		{"a real next page is included", int32Ptr(2), 30, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			c := &Config{Format: FormatYAML, Color: ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
			if err := c.YAMLList([]string{"a", "b"}, tt.nextPage, tt.total); err != nil {
				t.Fatalf("YAMLList: %v", err)
			}
			got := buf.String()
			if strings.Contains(got, "nextPage") != tt.wantNext {
				t.Errorf("nextPage present = %v, want %v, got:\n%s", !tt.wantNext, tt.wantNext, got)
			}
			if !strings.Contains(got, "data:") {
				t.Errorf("envelope should carry a data key, got:\n%s", got)
			}
		})
	}
}

func int32Ptr(i int32) *int32 { return &i }

// ---- Hint / WarnBox suppression --------------------------------------------

// Hint is commentary. Emitting it in JSON or YAML mode would corrupt the
// document that a caller is about to parse.
func TestHint_OnlyInTableMode(t *testing.T) {
	for _, f := range []Format{FormatJSON, FormatYAML} {
		var buf bytes.Buffer
		c := &Config{Format: f, Color: ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
		c.Hint("run something else")
		if buf.Len() != 0 {
			t.Errorf("Hint must be silent in %s mode, got: %q", f, buf.String())
		}
	}
	var buf bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &buf, EWriter: &bytes.Buffer{}}
	c.Hint("run something else")
	if !strings.Contains(buf.String(), "run something else") {
		t.Errorf("Hint should print in table mode, got: %q", buf.String())
	}
}

// WarnBox degrades to plain prefixed lines outside table mode rather than
// drawing a box into a pipe — but it must not go silent, because the warnings
// it carries are the ones that warrant extra weight.
func TestWarnBox_DegradesButStaysVisible(t *testing.T) {
	for _, f := range []Format{FormatJSON, FormatYAML} {
		var errBuf bytes.Buffer
		c := &Config{Format: f, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &errBuf}
		c.WarnBox("first line", "second line")
		got := errBuf.String()
		for _, want := range []string{"first line", "second line"} {
			if !strings.Contains(got, want) {
				t.Errorf("WarnBox lost %q in %s mode, got: %q", want, f, got)
			}
		}
		if strings.ContainsAny(got, "╭╰│") {
			t.Errorf("WarnBox must not draw a border in %s mode, got: %q", f, got)
		}
	}
}

// ---- Spinners ---------------------------------------------------------------

// Spinners animate on stderr. Under a pipe — every CI run, every script — they
// must be inert and their stop/update calls must stay safe to call anyway.
func TestSpinners_NoOpWhenNotATTY(t *testing.T) {
	var errBuf bytes.Buffer
	c := &Config{Format: FormatTable, Color: ColorNever, Writer: &bytes.Buffer{}, EWriter: &errBuf}

	stop := c.Spin("working…")
	if stop == nil {
		t.Fatal("Spin must return a callable stop function even when inert")
	}
	stop()
	stop() // stopping twice must not panic

	s := c.StartSpinner("working…")
	if s == nil {
		t.Fatal("StartSpinner must return a spinner even when inert")
	}
	s.Update("still working…")
	s.Stop()
	s.Stop()

	if errBuf.Len() != 0 {
		t.Errorf("spinners must write nothing to a non-TTY stderr, got: %q", errBuf.String())
	}
}

func TestSpin_SilentInQuietAndStructuredModes(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
	}{
		{"quiet", &Config{Format: FormatTable, QuietMode: true}},
		{"json", &Config{Format: FormatJSON}},
		{"yaml", &Config{Format: FormatYAML}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errBuf bytes.Buffer
			tc.cfg.Writer = &bytes.Buffer{}
			tc.cfg.EWriter = &errBuf
			tc.cfg.Spin("working…")()
			tc.cfg.StartSpinner("working…").Stop()
			if errBuf.Len() != 0 {
				t.Errorf("spinner should be silent, got: %q", errBuf.String())
			}
		})
	}
}

// ---- TTY predicates ---------------------------------------------------------

// These wrap term.IsTerminal. Under `go test` all three streams are pipes, so
// the assertion is that they agree with that rather than returning a constant.
func TestTTYPredicates_ReportNonTTYUnderTest(t *testing.T) {
	if IsStderrTTY() {
		t.Error("IsStderrTTY() = true under `go test`, where stderr is a pipe")
	}
	if isStdoutTTY() {
		t.Error("isStdoutTTY() = true under `go test`, where stdout is a pipe")
	}
	if IsInteractive() {
		t.Error("IsInteractive() = true under `go test`, where stdin is not a terminal")
	}
}

// TestRelativeTimeWidensUnit guards the unit-widening thresholds. Day counts
// are exact inside a quarter, where a renewal decision is actually pending;
// past that they widen, because "in 2750 days" told a reader nothing about a
// domain paid through 2034.
func TestRelativeTimeWidensUnit(t *testing.T) {
	tests := []struct {
		days float64
		want string
	}{
		{0.5, "today"},
		{-0.5, "expired today"},
		{1, "in 1 day"},
		{90, "in 90 days"},   // boundary: still exact days
		{91, "in 3 months"},  // first step up
		{194, "in 6 months"}, // a real expiry from `domain list`
		{729, "in 24 months"},
		{730, "in 2 years"}, // boundary: months give way to years
		{2750, "in 8 years"},
		{-3, "3 days ago"},
		{-1, "1 day ago"},
		{-758, "2 years ago"}, // a real expired domain from `domain list`
	}
	for _, tt := range tests {
		if got := relativeTime(tt.days); got != tt.want {
			t.Errorf("relativeTime(%.1f) = %q, want %q", tt.days, got, tt.want)
		}
	}
}

// TestTableFitsTerminalWidth guards the column-dropping behavior. Tables were
// rendered at natural width regardless of the terminal: `domain list` measured
// 113 columns against an 80-column pane, and the rounded borders came apart on
// wrap. Columns now drop from the right, which is the least-important end,
// and the footer names what went so nothing disappears silently.
func TestTableFitsTerminalWidth(t *testing.T) {
	headers := []string{"DOMAIN", "EXPIRES", "AUTO-RENEW", "LOCKED", "PRIVACY"}
	rows := [][]string{
		{"loadtest-ff7fb52b-b51b-46c8-b254-6a557f053321.com", "2027-03-01 (in 6 months)", "yes", "yes", "no"},
		{"beers.army", "2034-03-01 (in 8 years)", "yes", "yes", "yes"},
	}

	widest := func(s string) int {
		widest := 0
		for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
			if w := lipgloss.Width(line); w > widest {
				widest = w
			}
		}
		return widest
	}

	t.Run("drops columns to fit", func(t *testing.T) {
		var buf bytes.Buffer
		c := &Config{Format: FormatTable, Writer: &buf, EWriter: &buf, MaxWidth: 80}
		c.Table(headers, rows)
		got := buf.String()

		// The footer names hidden columns; measure only the table itself.
		var tableLines []string
		for _, line := range strings.Split(got, "\n") {
			if strings.ContainsAny(line, "│╭╰├") {
				tableLines = append(tableLines, line)
			}
		}
		if w := widest(strings.Join(tableLines, "\n")); w > 80 {
			t.Errorf("table rendered %d columns wide, want <= 80:\n%s", w, got)
		}
		if !strings.Contains(got, "hidden") {
			t.Errorf("dropped columns without telling the reader:\n%s", got)
		}
		if !strings.Contains(got, "DOMAIN") {
			t.Errorf("dropped the identifying column:\n%s", got)
		}
	})

	t.Run("--wide keeps every column", func(t *testing.T) {
		var buf bytes.Buffer
		c := &Config{Format: FormatTable, Writer: &buf, EWriter: &buf, MaxWidth: 80, Wide: true}
		c.Table(headers, rows)
		got := buf.String()
		for _, h := range headers {
			if !strings.Contains(got, h) {
				t.Errorf("--wide dropped %q:\n%s", h, got)
			}
		}
		if strings.Contains(got, "hidden") {
			t.Errorf("--wide should not print a hidden-column footer:\n%s", got)
		}
	})

	t.Run("no width constraint keeps every column", func(t *testing.T) {
		var buf bytes.Buffer
		c := &Config{Format: FormatTable, Writer: &buf, EWriter: &buf} // MaxWidth 0: piped
		c.Table(headers, rows)
		got := buf.String()
		for _, h := range headers {
			if !strings.Contains(got, h) {
				t.Errorf("piped output dropped %q:\n%s", h, got)
			}
		}
	})

	t.Run("wide enough drops nothing", func(t *testing.T) {
		var buf bytes.Buffer
		c := &Config{Format: FormatTable, Writer: &buf, EWriter: &buf, MaxWidth: 200}
		c.Table(headers, rows)
		if got := buf.String(); strings.Contains(got, "hidden") {
			t.Errorf("dropped columns that fit:\n%s", got)
		}
	})
}

// TestExpiryStyleThresholds pins the urgency thresholds. The expired and
// expiring-this-week cases were two byte-identical switch arms; merging them is
// only safe if something asserts both still resolve to red.
//
// It asserts on the style rather than on rendered output because lipgloss
// degrades to no-op styles off a TTY: an earlier version of this test compared
// ANSI prefixes, every expected prefix was the empty string, and all three
// cases passed without checking anything.
func TestExpiryStyleThresholds(t *testing.T) {
	tests := []struct {
		name string
		days float64
		want lipgloss.TerminalColor
	}{
		{"long expired", -758, acRed},
		{"expired today", -0.5, acRed},
		{"expiring inside a week", 3, acRed},
		{"the week boundary", 6.9, acRed},
		{"expiring inside a month", 20, acAmber},
		{"the month boundary", 29.9, acAmber},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expiryStyle(tt.days).GetForeground(); got != tt.want {
				t.Errorf("expiryStyle(%.1f) foreground = %v, want %v", tt.days, got, tt.want)
			}
		})
	}

	t.Run("far future is neither red nor amber", func(t *testing.T) {
		fg := expiryStyle(900).GetForeground()
		if fg == acRed || fg == acAmber {
			t.Errorf("expiryStyle(900) = %v, want the dim style", fg)
		}
	})

	t.Run("a far-future date still reads in years", func(t *testing.T) {
		at := time.Now().Add(900 * 24 * time.Hour)
		if got := noColor().ExpiryDate(&at); !strings.Contains(got, "in 2 years") {
			t.Errorf("ExpiryDate(900 days) = %q, want a year-scale relative time", got)
		}
	})
}
