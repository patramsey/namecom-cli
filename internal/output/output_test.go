package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
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
