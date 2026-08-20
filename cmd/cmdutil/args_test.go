package cmdutil

import (
	"bytes"
	"errors"
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

// TestGroupCmd guards the unknown-subcommand check on command groups.
//
// Cobra's legacyArgs only rejects unknown commands on the ROOT command — for
// any parent that itself has a parent it returns nil, so cobra fell through to
// "not runnable", printed help, and returned no error. `namecom domain
// regsiter example.com` therefore exited 0, and
// `namecom domain regsiter foo.com && deploy` deployed.
func TestGroupCmd(t *testing.T) {
	newGroup := func() *cobra.Command {
		root := &cobra.Command{Use: "namecom"}
		group := GroupCmd(&cobra.Command{Use: "domain"})
		group.AddCommand(&cobra.Command{Use: "register", Run: func(*cobra.Command, []string) {}})
		group.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
		root.AddCommand(group)
		return group
	}

	t.Run("unknown subcommand is a usage error", func(t *testing.T) {
		group := newGroup()
		err := group.Args(group, []string{"regsiter", "example.com"})
		if err == nil {
			t.Fatal("unknown subcommand accepted; it would print help and exit 0")
		}
		var u *UsageError
		if !errors.As(err, &u) {
			t.Errorf("error is not a UsageError, so it would exit 1 not 2: %v", err)
		}
		if !strings.Contains(err.Error(), `unknown command "regsiter"`) {
			t.Errorf("error does not name the typo: %v", err)
		}
		if !strings.Contains(err.Error(), "register") {
			t.Errorf("error carries no suggestion: %v", err)
		}
	})

	t.Run("no args is allowed so the group can print help", func(t *testing.T) {
		group := newGroup()
		if err := group.Args(group, nil); err != nil {
			t.Errorf("bare group name rejected: %v", err)
		}
	})

	t.Run("bare group prints help rather than erroring", func(t *testing.T) {
		group := newGroup()
		var buf bytes.Buffer
		group.SetOut(&buf)
		if err := group.RunE(group, nil); err != nil {
			t.Fatalf("group RunE returned an error: %v", err)
		}
		if !strings.Contains(buf.String(), "register") {
			t.Errorf("bare group did not print its subcommands:\n%s", buf.String())
		}
	})

	t.Run("suggestion distance is set", func(t *testing.T) {
		// Cobra defaults this to 2 only inside the root-command path being
		// replaced here. Left at zero, SuggestionsFor matches nothing.
		group := newGroup()
		if group.SuggestionsMinimumDistance <= 0 {
			t.Error("SuggestionsMinimumDistance not set; every typo loses its hint")
		}
	})
}

// TestNextPage guards the paginated-walk stopping conditions.
//
// The old condition was `nextPage == nil || *nextPage == 0` alone, which trusts
// the server to eventually stop saying "there is more". Against one that kept
// answering nextPage:2, `dns list --all` walked forever at the full client rate
// limit — confirmed by running it: still going after 20 seconds. domain list
// escaped only because it bounded on lastPage; the other seven list commands
// and record-ID completion did not.
func TestNextPage(t *testing.T) {
	p := func(v int32) *int32 { return &v }

	tests := []struct {
		name     string
		current  int32
		next     *int32
		last     *int32
		wantPage int32
		wantOK   bool
	}{
		{"advances normally", 1, p(2), p(9), 2, true},
		{"nil next ends the walk", 3, nil, p(9), 3, false},
		{"zero next ends the walk", 3, p(0), p(9), 3, false},
		{"a next that repeats the current page ends the walk", 2, p(2), p(99), 2, false},
		{"a next that goes backwards ends the walk", 5, p(3), p(99), 5, false},
		{"a next beyond lastPage ends the walk", 9, p(10), p(9), 9, false},
		{"reaching lastPage exactly is allowed", 8, p(9), p(9), 9, true},
		{"an unknown lastPage still allows advancing", 1, p(2), nil, 2, true},
		{"a zero lastPage is treated as unknown", 1, p(2), p(0), 2, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPage, gotOK := NextPage(tt.current, tt.next, tt.last)
			if gotPage != tt.wantPage || gotOK != tt.wantOK {
				t.Errorf("NextPage(%d, %v, %v) = (%d, %v), want (%d, %v)",
					tt.current, tt.next, tt.last, gotPage, gotOK, tt.wantPage, tt.wantOK)
			}
		})
	}

	t.Run("a stuck server terminates the walk", func(t *testing.T) {
		// The exact shape that hung: nextPage pinned at 2 forever.
		page, steps := int32(1), 0
		for {
			next, ok := NextPage(page, p(2), p(99))
			if !ok {
				break
			}
			page = next
			if steps++; steps > 100 {
				t.Fatal("walk did not terminate against a non-advancing nextPage")
			}
		}
		if steps != 1 {
			t.Errorf("walk took %d steps, want 1 (page 1 -> 2, then stop)", steps)
		}
	})
}
