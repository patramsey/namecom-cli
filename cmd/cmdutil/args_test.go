package cmdutil

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestArgNames(t *testing.T) {
	tests := []struct {
		use  string
		want []string
	}{
		{"list", nil},
		{"list <domain>", []string{"domain"}},
		{"update <domain> <id>", []string{"domain", "id"}},
		{"get <domain> <record-id>", []string{"domain", "record-id"}},
		// Literal token (no angle brackets) is not extracted.
		{"update example.com <id>", []string{"id"}},
	}
	for _, tt := range tests {
		t.Run(tt.use, func(t *testing.T) {
			got := argNames(tt.use)
			if len(got) != len(tt.want) {
				t.Fatalf("argNames(%q) = %v, want %v", tt.use, got, tt.want)
			}
			for i, v := range got {
				if v != tt.want[i] {
					t.Errorf("[%d] got %q, want %q", i, v, tt.want[i])
				}
			}
		})
	}
}

func TestJoinNames(t *testing.T) {
	tests := []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"domain"}, "domain"},
		{[]string{"domain", "id"}, "domain and id"},
		{[]string{"a", "b", "c"}, "a, b, and c"},
	}
	for _, tt := range tests {
		if got := joinNames(tt.names); got != tt.want {
			t.Errorf("joinNames(%v) = %q, want %q", tt.names, got, tt.want)
		}
	}
}

func TestNeedsMessage(t *testing.T) {
	tests := []struct {
		names []string
		want  string
	}{
		{nil, "missing required argument"},
		{[]string{"domain"}, "domain is required"},
		{[]string{"domain", "id"}, "domain and id are required"},
		{[]string{"a", "b", "c"}, "a, b, and c are required"},
	}
	for _, tt := range tests {
		if got := needsMessage(tt.names); got != tt.want {
			t.Errorf("needsMessage(%v) = %q, want %q", tt.names, got, tt.want)
		}
	}
}

func TestExactArgs(t *testing.T) {
	cmd := &cobra.Command{Use: "get <domain> <id>"}

	// Correct count — no error.
	if err := ExactArgs(2)(cmd, []string{"example.com", "42"}); err != nil {
		t.Errorf("2 args: unexpected error: %v", err)
	}

	// One arg supplied — error names only the missing one.
	err := ExactArgs(2)(cmd, []string{"example.com"})
	if err == nil {
		t.Fatal("1 arg: expected error")
	}
	if !strings.Contains(err.Error(), "id is required") {
		t.Errorf("1 arg error = %q, want 'id is required'", err.Error())
	}

	// Zero args — both names listed.
	err = ExactArgs(2)(cmd, nil)
	if err == nil {
		t.Fatal("0 args: expected error")
	}
	if !strings.Contains(err.Error(), "domain and id are required") {
		t.Errorf("0 args error = %q, want 'domain and id are required'", err.Error())
	}

	// Too many — "too many arguments" message.
	err = ExactArgs(2)(cmd, []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("3 args: expected error")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("too-many error = %q, want 'too many'", err.Error())
	}
}

func TestExactArgs_SingleArg(t *testing.T) {
	cmd := &cobra.Command{Use: "lock <domain>"}

	if err := ExactArgs(1)(cmd, []string{"example.com"}); err != nil {
		t.Errorf("1 arg: unexpected error: %v", err)
	}

	err := ExactArgs(1)(cmd, nil)
	if err == nil {
		t.Fatal("0 args: expected error")
	}
	if !strings.Contains(err.Error(), "domain is required") {
		t.Errorf("error = %q, want 'domain is required'", err.Error())
	}
}

func TestMinimumNArgs(t *testing.T) {
	cmd := &cobra.Command{Use: "check <domain>"}

	// Exactly the minimum.
	if err := MinimumNArgs(1)(cmd, []string{"a"}); err != nil {
		t.Errorf("1 arg: unexpected error: %v", err)
	}

	// More than minimum is fine.
	if err := MinimumNArgs(1)(cmd, []string{"a", "b", "c"}); err != nil {
		t.Errorf("3 args: unexpected error: %v", err)
	}

	// Below minimum.
	err := MinimumNArgs(1)(cmd, nil)
	if err == nil {
		t.Fatal("0 args: expected error")
	}
	if !strings.Contains(err.Error(), "domain is required") {
		t.Errorf("error = %q, want 'domain is required'", err.Error())
	}
}

// UseLine() includes the full command path which appears after "try:" in the error.
// Confirm the hint is present so users know the correct syntax.
func TestExactArgs_HintContainsUseLine(t *testing.T) {
	root := &cobra.Command{Use: "namecom"}
	sub := &cobra.Command{Use: "dns list <domain>"}
	root.AddCommand(sub)

	err := ExactArgs(1)(sub, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "try:") {
		t.Errorf("error = %q, missing 'try:' hint", err.Error())
	}
	if !strings.Contains(err.Error(), "namecom dns list") {
		t.Errorf("error = %q, missing command path in hint", err.Error())
	}
}
