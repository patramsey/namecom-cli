package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// noStyle is the identity style function, so tests assert on the text
// printFlags produces rather than on lipgloss rendering.
func noStyle(_ lipgloss.Style, s string) string { return s }

// ansiRE strips SGR escape sequences so a coloured rendering can be compared
// for content rather than for bytes.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// These assert the STRUCTURE of help output, not its wording. Asserting exact
// phrasing would produce a change-detector: it would fail every time someone
// reworded a description and never when something actually broke. What is
// pinned here is what a user would notice missing — a command absent from the
// list, a hidden command leaking, flags losing their descriptions, colour
// escapes leaking into a pipe.

func helpFixture() *cobra.Command {
	root := &cobra.Command{
		Use:   "namecom",
		Short: "short description",
		Long:  "namecom is the command-line interface for name.com",
	}
	// Run matters: cobra's IsAvailableCommand reports false for a command with
	// no Run and no subcommands, so a fixture without it is excluded from help
	// for the wrong reason and the assertions below would pass vacuously.
	noop := func(*cobra.Command, []string) {}
	root.AddCommand(
		&cobra.Command{Use: "domain", Short: "manage domains", Run: noop},
		&cobra.Command{Use: "dns", Short: "manage DNS records", Run: noop},
		&cobra.Command{Use: "secret", Short: "hidden thing", Hidden: true, Run: noop},
	)
	root.Flags().StringP("output", "o", "table", "output format")
	root.Flags().Bool("dry-run", false, "print the request without sending it")
	root.Flags().String("internal", "", "not for users")
	_ = root.Flags().MarkHidden("internal")
	return root
}

func TestPrintHelp_ListsAvailableCommandsAndHidesHidden(t *testing.T) {
	var buf bytes.Buffer
	printHelp(&buf, helpFixture(), false)
	got := buf.String()

	for _, want := range []string{"domain", "dns"} {
		if !strings.Contains(got, want) {
			t.Errorf("help output should list the %q command, got:\n%s", want, got)
		}
	}
	// A hidden command appearing in help is a real defect: it advertises
	// something unsupported.
	if strings.Contains(got, "secret") {
		t.Errorf("help output must not list hidden commands, got:\n%s", got)
	}
	if strings.Contains(got, "hidden thing") {
		t.Errorf("help output must not list a hidden command's description, got:\n%s", got)
	}
}

func TestPrintHelp_IncludesDescriptionAndUsage(t *testing.T) {
	var buf bytes.Buffer
	cmd := helpFixture()
	printHelp(&buf, cmd, false)
	got := buf.String()

	if !strings.Contains(got, cmd.Long) {
		t.Errorf("help should include the long description, got:\n%s", got)
	}
	if !strings.Contains(got, cmd.UseLine()) {
		t.Errorf("help should include the usage line %q, got:\n%s", cmd.UseLine(), got)
	}
}

// Short is the fallback when a command has no Long. A command whose help
// renders with no description at all is the failure this prevents.
func TestPrintHelp_FallsBackToShortDescription(t *testing.T) {
	cmd := &cobra.Command{Use: "solo", Short: "the only description there is"}
	var buf bytes.Buffer
	printHelp(&buf, cmd, false)
	if !strings.Contains(buf.String(), "the only description there is") {
		t.Errorf("help should fall back to Short when Long is empty, got:\n%s", buf.String())
	}
}

// Help is routinely piped (`namecom --help | less`, into a file, into an
// agent). Escape sequences leaking through when colour is off corrupts all of
// those.
func TestPrintHelp_ColorFlagControlsEscapeSequences(t *testing.T) {
	var plain, colored bytes.Buffer
	printHelp(&plain, helpFixture(), false)
	printHelp(&colored, helpFixture(), true)

	if strings.ContainsRune(plain.String(), '\x1b') {
		t.Errorf("colour disabled must produce no escape sequences, got:\n%q", plain.String())
	}
	// Both renderings must still carry the same information.
	for _, want := range []string{"domain", "dns", "output"} {
		if !strings.Contains(stripANSI(colored.String()), want) {
			t.Errorf("coloured help lost %q", want)
		}
	}
}

// Commands with no subcommands and no flags must not panic or emit a stray
// empty "Commands:"/"Flags:" section.
func TestPrintHelp_LeafCommandDoesNotPanic(t *testing.T) {
	var buf bytes.Buffer
	printHelp(&buf, &cobra.Command{Use: "leaf", Short: "a leaf", Run: func(*cobra.Command, []string) {}}, false)
	if buf.Len() == 0 {
		t.Error("help for a leaf command produced no output at all")
	}
}

func TestStyledHelp_WritesToCommandOutput(t *testing.T) {
	cmd := helpFixture()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	styledHelp(cmd, nil)
	if !strings.Contains(buf.String(), "domain") {
		t.Errorf("styledHelp should write help to the command's output writer, got:\n%s", buf.String())
	}
}

// ---- flag rendering ---------------------------------------------------------

func TestPrintFlags_RendersNamesShorthandsAndUsage(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.StringP("output", "o", "table", "output format")
	fs.Bool("yes", false, "skip confirmation prompts")
	fs.String("secret", "", "should not appear")
	_ = fs.MarkHidden("secret")

	var buf bytes.Buffer
	printFlags(&buf, fs, false, noStyle)
	got := buf.String()

	for _, want := range []string{"--output", "-o", "output format", "--yes", "skip confirmation prompts"} {
		if !strings.Contains(got, want) {
			t.Errorf("flag output missing %q, got:\n%s", want, got)
		}
	}
	// A hidden flag in help advertises something unsupported.
	if strings.Contains(got, "--secret") {
		t.Errorf("hidden flags must not be rendered, got:\n%s", got)
	}
}

// Subcommand help shows a curated subset of global flags. --dry-run and --yes
// are pinned by name deliberately: they are the flags that gate destructive
// actions, and dropping either from subcommand help hides the safety controls
// from exactly the pages where someone is about to mutate something.
func TestEssentialGlobalFlagNames_IncludesTheSafetyFlags(t *testing.T) {
	essential := essentialGlobalFlagNames()
	for _, want := range []string{"dry-run", "yes", "output", "quiet"} {
		if !essential[want] {
			t.Errorf("--%s should be shown on subcommand help pages", want)
		}
	}
	// It is a filter, not a passthrough — if it ever returns everything, the
	// filtering below stops meaning anything.
	if essential["debug"] || essential["token"] {
		t.Error("noisy flags (--debug, --token) should be filtered out of subcommand help")
	}
}

func TestPrintFilteredFlags_ShowsOnlyAllowedFlags(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("output", "table", "output format")
	fs.Bool("dry-run", false, "print the request without sending it")
	fs.String("token", "", "API token")

	var buf bytes.Buffer
	printFilteredFlags(&buf, fs, map[string]bool{"output": true, "dry-run": true}, false, noStyle)
	got := buf.String()

	if !strings.Contains(got, "--output") || !strings.Contains(got, "--dry-run") {
		t.Errorf("allowed flags should be rendered, got:\n%s", got)
	}
	if strings.Contains(got, "--token") {
		t.Errorf("a flag outside the allow list must not be rendered, got:\n%s", got)
	}
}

// TestHelpLayout pins the three help-page fixes. None of them had a test, and
// all three are the kind of thing that silently reverts when someone edits the
// template for an unrelated reason.
func TestHelpLayout(t *testing.T) {
	noop := func(*cobra.Command, []string) {}

	t.Run("examples appear above the flag tables", func(t *testing.T) {
		// Examples used to print last — below Flags, below Global Flags, and
		// below the "see all global options" footer — which put the most-read
		// part of a help page furthest down it.
		root := &cobra.Command{Use: "namecom"}
		cmd := &cobra.Command{
			Use:     "create <domain>",
			Short:   "Create a DNS record",
			Example: "  namecom dns create example.com --type A --answer 1.2.3.4",
			Run:     noop,
		}
		cmd.Flags().String("answer", "", "record value")
		root.AddCommand(cmd)

		var buf bytes.Buffer
		printHelp(&buf, cmd, false)
		got := buf.String()

		examples := strings.Index(got, "Examples:")
		flags := strings.Index(got, "Flags:")
		if examples < 0 || flags < 0 {
			t.Fatalf("help is missing a section:\n%s", got)
		}
		if examples > flags {
			t.Errorf("Examples renders below Flags:\n%s", got)
		}
	})

	t.Run("a command group asks for a subcommand, not flags", func(t *testing.T) {
		// UseLine() appends "[flags]" to anything with flags, so every group
		// advertised `namecom domain [flags]` — an invocation that does nothing.
		root := &cobra.Command{Use: "namecom"}
		group := &cobra.Command{Use: "domain", Short: "Manage domains"}
		group.AddCommand(&cobra.Command{Use: "list", Short: "List domains", Run: noop})
		root.AddCommand(group)

		if got := usageLine(group); got != "namecom domain <command>" {
			t.Errorf("usageLine(group) = %q, want %q", got, "namecom domain <command>")
		}
		// A leaf command keeps cobra's own usage line, placeholders and all.
		leaf := &cobra.Command{Use: "get <domain>", Run: noop}
		root.AddCommand(leaf)
		if got := usageLine(leaf); !strings.Contains(got, "<domain>") {
			t.Errorf("usageLine(leaf) = %q, want it to keep its argument placeholder", got)
		}
		// The root keeps "[command] [flags]": its persistent flags are the
		// ones the page is documenting.
		if got := usageLine(root); strings.Contains(got, "<command>") {
			t.Errorf("usageLine(root) = %q, want cobra's own line", got)
		}
	})

	t.Run("only string defaults are quoted", func(t *testing.T) {
		// Quoting uniformly rendered (default "300") and (default "30s"),
		// which reads like the flag wants a quoted literal.
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		fs.String("host", "@", "hostname")
		fs.Int64("ttl", 300, "TTL in seconds")
		fs.Duration("timeout", 30*time.Second, "per-request timeout")

		var buf bytes.Buffer
		printFlags(&buf, fs, false, noStyle)
		got := buf.String()

		for _, want := range []string{`(default "@")`, `(default 300)`, `(default 30s)`} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %s in:\n%s", want, got)
			}
		}
		for _, unwanted := range []string{`(default "300")`, `(default "30s")`} {
			if strings.Contains(got, unwanted) {
				t.Errorf("non-string default rendered as %s:\n%s", unwanted, got)
			}
		}
	})
}
