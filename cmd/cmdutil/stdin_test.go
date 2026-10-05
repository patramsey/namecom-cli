package cmdutil

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestExpandStdinArgs pins #244: `domain check -` treated "-" as a domain
// name. It now stands for the names on stdin, one per line, spliced in where
// the "-" was; blank lines and # comments are skipped, and a Windows line
// ending is trimmed.
func TestExpandStdinArgs(t *testing.T) {
	for _, tc := range []struct {
		name, stdin string
		args, want  []string
	}{
		{"alone", "a.com\nb.com\n", []string{"-"}, []string{"a.com", "b.com"}},
		{"in place", "b.com\n", []string{"a.com", "-", "c.com"}, []string{"a.com", "b.com", "c.com"}},
		{"comments and blanks", "# my domains\n\n  a.com  \nb.com # renew soon\n#c.com\n", []string{"-"}, []string{"a.com", "b.com"}},
		{"CRLF and no final newline", "a.com\r\nb.com", []string{"-"}, []string{"a.com", "b.com"}},
		{"no dash leaves stdin alone", "x.com\n", []string{"a.com"}, []string{"a.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			stdin := strings.NewReader(tc.stdin)
			cmd.SetIn(stdin)
			got, err := ExpandStdinArgs(cmd, tc.args)
			if err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("ExpandStdinArgs(%q) = %q, %v; want %q", tc.args, got, err, tc.want)
			}
			if !slices.Contains(tc.args, "-") && stdin.Len() != len(tc.stdin) {
				t.Error("stdin was read although no '-' was given")
			}
		})
	}
}

// TestExpandStdinArgs_Refusals: stdin can be read once, and an empty one is
// not an empty success.
func TestExpandStdinArgs_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name, stdin, want string
		args              []string
	}{
		{"two dashes", "a.com\n", "only once", []string{"-", "-"}},
		{"empty stdin", "", "stdin held no names", []string{"-"}},
		{"only comments", "# nothing yet\n\n", "stdin held no names", []string{"-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			cmd.SetIn(strings.NewReader(tc.stdin))
			_, err := ExpandStdinArgs(cmd, tc.args)
			if _, ok := errors.AsType[*UsageError](err); !ok || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ExpandStdinArgs(%q) = %v, want a usage error containing %q", tc.args, err, tc.want)
			}
		})
	}
}

// TestToggleArgs_SeveralDomains covers #244: the toggles take several
// domains, with on|off first or last, "-" for stdin, and a repeat dropped.
func TestToggleArgs_SeveralDomains(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"off", "a.com", "B.com", "a.com"}, "a.com,b.com"},
		{[]string{"a.com", "b.com", "OFF"}, "a.com,b.com"},
		{[]string{"off", "-"}, "b.com,a.com"},
		{[]string{"a.com", "-", "off"}, "a.com,b.com"},
	} {
		cmd := &cobra.Command{}
		cmd.SetIn(strings.NewReader("b.com\n# a comment\n\na.com\n"))
		on, d, err := ToggleArgs(cmd, tc.args)
		if err != nil || on || strings.Join(d, ",") != tc.want {
			t.Errorf("ToggleArgs(%q) = %v, %q, %v; want off, %s", tc.args, on, d, err, tc.want)
		}
	}
	if _, _, err := ToggleArgs(&cobra.Command{}, []string{"-", "maybe"}); err == nil || !strings.Contains(err.Error(), `got "maybe"`) {
		t.Errorf("ToggleArgs(-, maybe) = %v, want an error naming \"maybe\"", err)
	}
}
