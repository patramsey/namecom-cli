package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
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
	printFlags(&buf, fs, 0, noStyle)
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

// renderHelp renders the real help page for a command path, without colour.
func renderHelp(t *testing.T, path ...string) string {
	t.Helper()
	c, _, err := rootCmd.Find(path)
	if err != nil {
		t.Fatalf("namecom %s: %v", strings.Join(path, " "), err)
	}
	var buf bytes.Buffer
	printHelp(&buf, c, false)
	return buf.String()
}

// section returns the lines of a help page under heading, up to the next
// blank line.
func section(page, heading string) string {
	_, after, ok := strings.Cut(page, "\n"+heading+"\n")
	if !ok {
		return ""
	}
	body, _, _ := strings.Cut(after, "\n\n")
	return body
}

// TestHelp_GlobalFlagsByKind pins #237: every leaf listed --dry-run and
// --yes, read-only `domain get` included, while no list listed --wide.
// --dry-run and --yes stay on writes deliberately: they gate destructive
// actions, and these are the pages where someone is about to use one.
func TestHelp_GlobalFlagsByKind(t *testing.T) {
	for _, tc := range []struct {
		path      string
		want, not []string
	}{
		{"dns create", []string{"--dry-run", "--yes", "--output", "--quiet"}, []string{"--wide", "--token"}},
		{"domain register", []string{"--dry-run", "--yes"}, []string{"--wide"}},
		{"api", []string{"--dry-run", "--yes"}, nil},
		{"dns list", []string{"--wide", "--no-header", "--quiet", "--output"}, []string{"--dry-run", "--yes"}},
		{"domain get", []string{"--output"}, []string{"--dry-run", "--yes", "--quiet", "--wide"}},
	} {
		got := section(renderHelp(t, strings.Fields(tc.path)...), "Global Flags:")
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("namecom %s: Global Flags lacks %s:\n%s", tc.path, w, got)
			}
		}
		for _, n := range tc.not {
			if strings.Contains(got, n) {
				t.Errorf("namecom %s: Global Flags shows %s, which does nothing there:\n%s", tc.path, n, got)
			}
		}
	}
}

// TestWritesAreMarked keeps --dry-run and --yes on the help of every command
// that honours them. Help learns that from cmdutil.MarkWrite, so a write
// added without it would hide both; any leaf named like a write must carry it.
func TestWritesAreMarked(t *testing.T) {
	writeVerbs := map[string]bool{
		"create": true, "update": true, "delete": true, "import": true, "register": true,
		"renew": true, "lock": true, "autorenew": true, "privacy": true, "set-ns": true,
		"set": true, "refund": true, "cancel": true, "cancel-outbound": true,
		"internal-in": true, "resend": true, "verify": true, "login": true, "logout": true,
		"use": true, "api": true, "sync": true,
	}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if writeVerbs[c.Name()] && c.Runnable() && cmdutil.Kind(c) != cmdutil.KindWrite {
			t.Errorf("%s is a write but is not marked with cmdutil.MarkWrite", c.CommandPath())
		}
	}
	walk(rootCmd)
}

// TestHelp_GroupPage pins #237: a group's page had an -h flags block, the
// global flags, and two footers. It runs nothing, so it has no flags.
func TestHelp_GroupPage(t *testing.T) {
	got := renderHelp(t, "dns")
	for _, unwanted := range []string{"Flags:", "-h, --help", "--dry-run", "\n\n\n"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("dns help contains %q:\n%s", unwanted, got)
		}
	}
	if n := strings.Count(got, "Learn More:"); n != 1 || !strings.Contains(got, `"namecom dns <command> --help"`) {
		t.Errorf("dns help should end with one footer naming 'namecom dns <command> --help':\n%s", got)
	}
}

// TestRootHelp_FlagSections pins #237: root help listed 19 global flags in one
// block, mostly advanced. Every flag has a section, and the few left under
// plain "Flags:" are the ones meant to be there, so a new global flag must be
// placed deliberately.
func TestRootHelp_FlagSections(t *testing.T) {
	got := renderHelp(t)
	for heading, flags := range map[string][]string{
		"Flags:":             {"--dry-run", "--yes", "--help", "--version"},
		"Output Flags:":      {"--output", "--quiet", "--no-header", "--wide", "--color"},
		"Credentials Flags:": {"--profile", "--username", "--token", "--sandbox"},
		"Advanced Flags:":    {"--timeout", "--debug", "--debug-file", "--idempotency-key", "--base-url"},
	} {
		body := section(got, heading)
		lines := strings.Count(body, "\n") + 1
		if body == "" || lines != len(flags) {
			t.Errorf("%s has %d lines, want %d (%v):\n%s", heading, lines, len(flags), flags, body)
		}
		for _, f := range flags {
			if !strings.Contains(body, f+" ") && !strings.HasSuffix(body, f) {
				t.Errorf("%s does not list %s:\n%s", heading, f, body)
			}
		}
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
		printFlags(&buf, fs, 0, noStyle)
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

// The exit codes were cited as a "documented table" in exitCode, but no help
// text showed them. Root help now lists each code exitCode can return.
func TestRootHelp_ListsExitCodes(t *testing.T) {
	for _, want := range []string{
		"Exit codes:",
		"0  success",
		"1  API or other runtime error",
		"2  usage error",
		"3  authentication",
		"4  not found",
		"5  rate limited",
		"6  write outcome unknown",
	} {
		if !strings.Contains(rootCmd.Long, want) {
			t.Errorf("root help does not contain %q:\n%s", want, rootCmd.Long)
		}
	}
}

// TestRootHelp_FlagsAfterHelp pins #209: `namecom --help --output json` exited
// 2 with `unknown command "json"`. Cobra adds the help flag only after it has
// resolved the command, so at that point --help was an unknown flag assumed to
// take a value: it swallowed --output, and json was left as a subcommand name.
func TestRootHelp_FlagsAfterHelp(t *testing.T) {
	for _, args := range [][]string{
		{"--help", "--output", "json"},
		{"--help", "--color", "never"},
		{"-h", "-o", "yaml"},
		{"--output", "json", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// A parsed --help stays set on the shared root; clear it so the
			// next test that runs the root is not served help instead.
			t.Cleanup(func() { _ = rootCmd.Flags().Set("help", "false") })
			if err := executeRoot(t, args...); err != nil {
				t.Errorf("namecom %s: %v (exit %d), want root help and exit 0",
					strings.Join(args, " "), err, exitCode(cmdutil.ClassifyCobraUsage(err)))
			}
		})
	}
}

// TestHelp_BoolValueFlags pins #236: a boolean whose false means something is
// shown as --flag=true|false, the only spelling that passes false, while a
// plain switch stays bare.
func TestHelp_BoolValueFlags(t *testing.T) {
	root := &cobra.Command{Use: "namecom"}
	cmd := &cobra.Command{Use: "update <domain>", Run: func(*cobra.Command, []string) {}}
	cmd.Flags().Bool("autorenew", false, "enable/disable auto-renewal")
	cmd.Flags().Bool("all", false, "fetch every page")
	cmdutil.MarkBoolValue(cmd.Flags(), "autorenew")
	root.AddCommand(cmd)

	var buf bytes.Buffer
	printHelp(&buf, cmd, false)
	got := buf.String()
	if !strings.Contains(got, "--autorenew=true|false") {
		t.Errorf("help does not show --autorenew=true|false:\n%s", got)
	}
	if strings.Contains(got, "--all=") {
		t.Errorf("a plain switch was shown with a value:\n%s", got)
	}
}

// TestHelp_HonoursColorFlag pins #237: help ran without PersistentPreRunE, so
// `--help --color=never` still printed escapes where colour was otherwise on,
// and --color=always did nothing in a pipe.
func TestHelp_HonoursColorFlag(t *testing.T) {
	prof := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(prof) })
	for _, tc := range []struct {
		color, force string
		escapes      bool
	}{
		{"never", "1", false}, // CLICOLOR_FORCE would turn it on
		{"always", "", true},  // a test's stdout is not a terminal
	} {
		t.Run(tc.color, func(t *testing.T) {
			t.Setenv("CLICOLOR_FORCE", tc.force)
			var buf bytes.Buffer
			rootCmd.SetOut(&buf)
			t.Cleanup(func() {
				rootCmd.SetOut(nil)
				_ = urlHelpFlag(t).Set("help", "false")
			})
			if err := executeRoot(t, "url", "list", "--help", "--color", tc.color); err != nil {
				t.Fatal(err)
			}
			if got := strings.ContainsRune(buf.String(), '\x1b'); got != tc.escapes {
				t.Errorf("--color %s: escapes = %v, want %v:\n%q", tc.color, got, tc.escapes, buf.String())
			}
		})
	}
}

func urlHelpFlag(t *testing.T) *pflag.FlagSet {
	t.Helper()
	c, _, err := rootCmd.Find([]string{"url", "list"})
	if err != nil {
		t.Fatal(err)
	}
	return c.Flags()
}

// TestHelp_WrapsToWidth pins #237: at 70 columns, long flag help and the
// `api` description ran past the edge. Everything but the examples and the
// usage line, which must stay one copyable line each, fits.
func TestHelp_WrapsToWidth(t *testing.T) {
	const width = 70
	for _, path := range []string{"", "api", "dns create", "environment", "dns"} {
		c, _, err := rootCmd.Find(strings.Fields(path))
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		printHelpWidth(&buf, c, false, width)
		got := buf.String()
		for _, line := range strings.Split(got, "\n") {
			if utf8.RuneCountInString(line) > width && !strings.Contains(c.Example, line) && !strings.Contains(line, c.UseLine()) {
				t.Errorf("namecom %s: line runs past %d columns:\n%s", path, width, line)
			}
		}
		// Nothing is lost: the unwrapped page has the same words.
		var plain bytes.Buffer
		printHelp(&plain, c, false)
		if strings.Join(strings.Fields(got), " ") != strings.Join(strings.Fields(plain.String()), " ") {
			t.Errorf("namecom %s: wrapping changed the text", path)
		}
	}

	// A two-column row continues under its second column.
	got := wrapBlock("  NAMECOM_TOKEN   the API token, which --token overrides when both are set", 50)
	want := "  NAMECOM_TOKEN   the API token, which --token\n                  overrides when both are set"
	if got != want {
		t.Errorf("wrapBlock row:\n%s\nwant:\n%s", got, want)
	}
}
