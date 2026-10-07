package cmd

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	helpHeading  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15"))  // bright white
	helpCmd      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("75"))  // cornflower blue
	helpCmdDesc  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // gray
	helpFlag     = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))             // cornflower blue
	helpFlagDesc = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))              // normal white
	helpUsage    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))              // gray
	helpBrand    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111")) // periwinkle
)

// rootFlagSections is the order root help lists its flag sections in. A flag
// is placed by its cmdutil.FlagSection annotation; one without is listed
// first, under "Flags".
var rootFlagSections = []string{"", "Output", "Credentials", "Advanced"}

// styledHelp is a cobra help function that renders styled output using Lip Gloss.
func styledHelp(cmd *cobra.Command, _ []string) {
	w := cmd.OutOrStdout()
	if showGettingStarted(cmd) {
		printGettingStarted(w)
		return
	}
	// Help runs without PersistentPreRunE, so it used to take the TTY default
	// and ignore --color: `--help --color=never` still printed escapes (#237).
	// The flags are parsed by now; a bad value falls back to the default here
	// and is reported by any real command.
	out, _, err := buildOutputConfig()
	if err != nil {
		out = output.DefaultConfig()
	}
	printHelpWidth(w, cmd, out.ColorEnabled(), helpWidth(out))
}

// helpCommand replaces cobra's `help`, which answered an unknown topic with
// "Unknown help topic", cobra's unstyled usage template and exit 0, and
// `help domain bogus` with the domain help and exit 0 (#313). An unknown word
// is a usage error, with suggestions, as `namecom domain bogus` is under
// GroupCmd. Words after a leaf command are ignored, as cobra's did:
// `help dns list example.com` shows the help for dns list.
var helpCommand = &cobra.Command{
	Use:   "help [command]",
	Short: "Help about any command",
	Long: `Help provides help for any command in the application.
Simply type namecom help [path to command] for full details.`,
	ValidArgsFunction: completeHelp,
	RunE:              runHelp,
}

func runHelp(c *cobra.Command, args []string) error {
	parent := c.Root()
	for _, word := range args {
		next := findSubcommand(parent, word)
		if next == nil {
			if !parent.HasAvailableSubCommands() {
				break
			}
			return unknownHelpTopic(parent, word)
		}
		parent = next
	}
	// Cobra's help command passes its context down; the help text uses none
	// today, but a command reached here should not see a nil one.
	if parent.Context() == nil {
		parent.SetContext(c.Context())
	}
	parent.InitDefaultHelpFlag()
	parent.InitDefaultVersionFlag()
	return parent.Help()
}

// findSubcommand is the subcommand of c named, or aliased, word, or nil.
func findSubcommand(c *cobra.Command, word string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == word || sub.HasAlias(word) {
			return sub
		}
	}
	return nil
}

// unknownHelpTopic is the usage error for word, not a subcommand of parent.
// Suggestions are given as help commands, `namecom help dns list`, since
// that is what was being typed.
func unknownHelpTopic(parent *cobra.Command, word string) error {
	if parent.SuggestionsMinimumDistance <= 0 {
		parent.SuggestionsMinimumDistance = 2
	}
	root := parent.Root()
	path := root.CommandPath() + " help"
	if rel := strings.TrimPrefix(parent.CommandPath(), root.CommandPath()); rel != "" {
		path += rel
	}
	var suggestions []string
	if parent == root {
		// `help records` is as likely as `namecom records`; rootSuggestFor's
		// "help environment" is already a help topic.
		if s, ok := rootSuggestFor[strings.ToLower(word)]; ok {
			suggestions = append(suggestions, strings.TrimPrefix(s, "help "))
		}
	}
	for _, s := range parent.SuggestionsFor(word) {
		if !slices.Contains(suggestions, s) {
			suggestions = append(suggestions, s)
		}
	}
	e := &cmdutil.UnknownCommandError{Word: word, Path: path}
	hint := fmt.Sprintf("run '%s' to list the commands", path)
	if len(suggestions) > 0 {
		quoted := make([]string, len(suggestions))
		for i, s := range suggestions {
			e.Suggestions = append(e.Suggestions, path+" "+s)
			quoted[i] = "'" + path + " " + s + "'"
		}
		hint = "did you mean " + strings.Join(quoted, " or ") + "?"
	}
	return cmdutil.NewUsageErrorHint(e, hint)
}

// completeHelp completes `namecom help` with the subcommands of the command
// typed so far, as cobra's own help command did.
func completeHelp(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	parent := c.Root()
	for _, word := range args {
		if parent = findSubcommand(parent, word); parent == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
	var completions []cobra.Completion
	for _, sub := range parent.Commands() {
		if (sub.IsAvailableCommand() || sub == c) && strings.HasPrefix(sub.Name(), toComplete) {
			completions = append(completions, cobra.CompletionWithDesc(sub.Name(), sub.Short))
		}
	}
	return completions, cobra.ShellCompDirectiveNoFileComp
}

// helpWidth is the width help wraps to: the terminal's, or $COLUMNS when
// stdout is not a terminal and it is set. Zero means no wrapping.
func helpWidth(out *output.Config) int {
	if out.MaxWidth > 0 {
		return out.MaxWidth
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 {
		return n
	}
	return 0
}

// printHelp is printHelpWidth with no wrapping.
func printHelp(w io.Writer, cmd *cobra.Command, color bool) {
	printHelpWidth(w, cmd, color, 0)
}

// printHelpWidth renders cmd's help, wrapping its description, command and
// flag descriptions, and footer to width columns (#237): at 70 columns long
// flag help and the `api` description ran past the edge. Examples are left as
// written, so each stays one line that can be copied.
func printHelpWidth(w io.Writer, cmd *cobra.Command, color bool, width int) {
	style := func(s lipgloss.Style, text string) string {
		if color {
			return s.Render(text)
		}
		return text
	}
	isRoot := cmd == cmd.Root()

	// Description
	fmt.Fprintln(w)
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	desc = wrapBlock(desc, width)
	// Bold the binary name in the long description.
	if color && isRoot {
		desc = strings.Replace(desc, "namecom", style(helpBrand, "namecom"), 1)
	}
	fmt.Fprintln(w, desc)
	fmt.Fprintln(w)

	// A help topic (`namecom help environment`) is its text and nothing else:
	// it has no usage line and takes no flags.
	if cmd.IsAdditionalHelpTopicCommand() && !isRoot {
		return
	}

	// Usage line
	fmt.Fprintln(w, style(helpHeading, "Usage:"))
	fmt.Fprintf(w, "  %s\n\n", style(helpUsage, usageLine(cmd)))

	// Aliases and examples sit directly under the usage line. Examples used to
	// print last, below the flag tables and the "see all global options"
	// footer, which put the most-read part of a help page furthest down it.
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(w, "%s  %s\n\n", style(helpHeading, "Aliases:"), strings.Join(cmd.Aliases, ", "))
	}
	if cmd.Example != "" {
		fmt.Fprintln(w, style(helpHeading, "Examples:"))
		fmt.Fprintln(w, cmd.Example)
		fmt.Fprintln(w)
	}

	// Subcommands — rendered grouped when groups are defined, flat otherwise.
	var available, topics []*cobra.Command
	for _, c := range cmd.Commands() {
		switch {
		case c.IsAvailableCommand():
			available = append(available, c)
		case c.IsAdditionalHelpTopicCommand() && !c.Hidden:
			topics = append(topics, c)
		}
	}
	maxLen := 0
	for _, c := range append(available, topics...) {
		maxLen = max(maxLen, len(c.Name()))
	}
	printCmdLine := func(c *cobra.Command) {
		padding := strings.Repeat(" ", maxLen-len(c.Name()))
		col := 2 + maxLen + 3
		lines := wrapWords(c.Short, descWidth(width, col))
		fmt.Fprintf(w, "  %s%s   %s\n",
			style(helpCmd, c.Name()),
			padding,
			style(helpCmdDesc, lines[0]),
		)
		for _, l := range lines[1:] {
			fmt.Fprintln(w, strings.Repeat(" ", col)+style(helpCmdDesc, l))
		}
	}
	if len(available) > 0 {
		groups := cmd.Groups()
		if len(groups) > 0 {
			// Print each group header followed by its commands.
			for _, g := range groups {
				var grouped []*cobra.Command
				for _, c := range available {
					if c.GroupID == g.ID {
						grouped = append(grouped, c)
					}
				}
				if len(grouped) == 0 {
					continue
				}
				fmt.Fprintln(w, style(helpHeading, g.Title))
				for _, c := range grouped {
					printCmdLine(c)
				}
				fmt.Fprintln(w)
			}
			// Ungrouped commands (completion, help) are printed without a group header.
			var ungrouped []*cobra.Command
			for _, c := range available {
				if c.GroupID == "" {
					ungrouped = append(ungrouped, c)
				}
			}
			if len(ungrouped) > 0 {
				for _, c := range ungrouped {
					printCmdLine(c)
				}
				fmt.Fprintln(w)
			}
		} else {
			fmt.Fprintln(w, style(helpHeading, "Available Commands:"))
			for _, c := range available {
				printCmdLine(c)
			}
			fmt.Fprintln(w)
		}
	}
	if len(topics) > 0 {
		fmt.Fprintln(w, style(helpHeading, "Help Topics:"))
		for _, c := range topics {
			printCmdLine(c)
		}
		fmt.Fprintln(w)
	}

	// Flags. The root's are its persistent flags, the global ones, under
	// headings; --help is listed there and nowhere else, since every page
	// that could list it is the answer to it.
	if isRoot {
		for _, section := range rootFlagSections {
			fs := sectionFlags(cmd.LocalFlags(), section)
			if !hasVisibleFlags(fs) {
				continue
			}
			heading := "Flags:"
			if section != "" {
				heading = section + " Flags:"
			}
			fmt.Fprintln(w, style(helpHeading, heading))
			printFlags(w, fs, width, style)
			fmt.Fprintln(w)
		}
	} else if local := withoutHelp(cmd.LocalFlags()); hasVisibleFlags(local) {
		fmt.Fprintln(w, style(helpHeading, "Flags:"))
		printFlags(w, local, width, style)
		fmt.Fprintln(w)
	}

	// A command that runs shows the global flags that apply to it (#237). A
	// group runs nothing, so it shows none.
	group := cmd.HasAvailableSubCommands()
	if !isRoot && !group && cmd.HasAvailableInheritedFlags() {
		fs := pickFlags(cmd.InheritedFlags(), globalFlagNames(cmd))
		if hasVisibleFlags(fs) {
			fmt.Fprintln(w, style(helpHeading, "Global Flags:"))
			printFlags(w, fs, width, style)
			fmt.Fprintln(w)
		}
	}

	// One footer, as gh has, where there used to be two.
	var more []string
	switch {
	case isRoot:
		more = []string{
			`Use "namecom <command> --help" for more information about a command.`,
			`Use "namecom help environment" for the environment variables namecom reads.`,
		}
	case group:
		more = []string{`Use "` + cmd.CommandPath() + ` <command> --help" for more information about a command.`}
	default:
		more = []string{`Use "namecom --help" for every global flag.`}
	}
	fmt.Fprintln(w, style(helpHeading, "Learn More:"))
	for _, line := range more {
		for _, l := range wrapWords(line, descWidth(width, 2)) {
			fmt.Fprintln(w, "  "+style(helpCmdDesc, l))
		}
	}
	fmt.Fprintln(w)
}

// minWrap is the narrowest column help wraps text into. Below it, a
// description wrapped beside a long flag name would be a word per line, and
// running past the edge reads better.
const minWrap = 24

// descWidth is the room left for text starting at column col of a width-wide
// terminal, or 0 (no wrapping) when width is unknown or too narrow.
func descWidth(width, col int) int {
	if width <= 0 || width-col < minWrap {
		return 0
	}
	return width - col
}

// wrapWords splits s into lines of at most width columns, breaking at spaces.
// A word longer than width gets a line of its own. Width 0 returns s whole.
func wrapWords(s string, width int) []string {
	if width <= 0 || utf8.RuneCountInString(s) <= width {
		return []string{s}
	}
	var lines []string
	cur, n := "", 0
	for _, word := range strings.Fields(s) {
		wl := utf8.RuneCountInString(word)
		if n > 0 && n+1+wl > width {
			lines = append(lines, cur)
			cur, n = "", 0
		}
		if n > 0 {
			cur += " "
			n++
		}
		cur += word
		n += wl
	}
	return append(lines, cur)
}

// wrapBlock wraps a description to width. Descriptions are written wrapped
// near 80 columns, so wrapping each line alone left every other line a word
// long; instead each paragraph is reflowed, and only when one of its lines
// does not fit, so a wide terminal shows the text as written.
//
// A paragraph is a run of lines at one indentation, or an indented
// two-column row ("  NAMECOM_TOKEN   API token.") with its continuations
// under its second column. Indented prose reflows like unindented prose
// (#293): `help formatting` is indented under its headings, and wrapping
// each of its lines alone left a word or two on every other line. An
// indented command line ("    namecom ...") or "# comment" is a paragraph of
// its own and is never wrapped, as Examples are not, so it can be copied.
// An indented "- " starts a list item, which continues under its text.
func wrapBlock(s string, width int) string {
	if width <= 0 {
		return s
	}
	type para struct {
		lead string   // first-line prefix: indentation, plus the first column of a row
		hang int      // indentation of continuation lines
		row  bool     // an indented two-column row
		code bool     // a command line or comment, left as written
		text string   // the words to wrap
		raw  []string // the lines as written
	}
	isCode := func(body string, indent int) bool {
		return indent > 0 && (strings.HasPrefix(body, "namecom ") || strings.HasPrefix(body, "#"))
	}
	var paras []*para
	for _, line := range strings.Split(s, "\n") {
		body := strings.TrimLeft(line, " ")
		indent := len(line) - len(body)
		code := isCode(body, indent)
		bullet := indent > 0 && strings.HasPrefix(body, "- ")
		if n := len(paras); n > 0 && body != "" && !code && !bullet {
			p := paras[n-1]
			prose := !p.row && !p.code && p.hang == indent
			if p.text != "" && (prose || (p.row && indent == p.hang)) {
				p.text += " " + body
				p.raw = append(p.raw, line)
				continue
			}
		}
		p := &para{lead: line[:indent], text: body, raw: []string{line}, code: code}
		switch gap := strings.Index(body, "  "); {
		case bullet:
			p.lead += "- "
			p.text = body[2:]
		case indent > 0 && gap > 0 && !code:
			p.text = strings.TrimLeft(body[gap:], " ")
			p.lead += body[:len(body)-len(p.text)]
			p.row = true
		}
		p.hang = utf8.RuneCountInString(p.lead)
		paras = append(paras, p)
	}

	var out []string
	for _, p := range paras {
		fits := true
		for _, l := range p.raw {
			fits = fits && utf8.RuneCountInString(l) <= width
		}
		if fits || p.code {
			out = append(out, p.raw...)
			continue
		}
		for i, l := range wrapWords(p.text, descWidth(width, p.hang)) {
			if i == 0 {
				out = append(out, p.lead+l)
			} else {
				out = append(out, strings.Repeat(" ", p.hang)+l)
			}
		}
	}
	return strings.Join(out, "\n")
}

// usageLine renders the usage line, correcting it for command groups.
//
// UseLine() appends "[flags]" whenever a command has flags, so every group
// printed as "namecom domain [flags]" — an invocation that does nothing. A
// group's Use string is a bare name with no argument placeholders; when it also
// has subcommands, what it actually takes is one of them. The root command is
// left alone: "namecom [command] [flags]" is accurate there, since its
// persistent flags are the ones being documented.
func usageLine(cmd *cobra.Command) string {
	if cmd.HasAvailableSubCommands() && cmd.HasParent() && !strings.Contains(cmd.Use, " ") {
		return cmd.CommandPath() + " <command>"
	}
	return cmd.UseLine()
}

// globalFlagNames returns the global flags shown on cmd's help page: the ones
// that do something for that kind of command (#237). Every page used to show
// --dry-run and --yes, read-only `domain get` included, while no list showed
// --wide. The rest are a "namecom --help" away.
func globalFlagNames(cmd *cobra.Command) map[string]bool {
	switch cmdutil.Kind(cmd) {
	case cmdutil.KindWrite:
		// api prints the body as received, which -q cannot shorten (#293).
		quiet := cmd.Annotations[cmdutil.RawOutputAnnotation] == ""
		return map[string]bool{"output": true, "quiet": quiet, "yes": true, "dry-run": true}
	case cmdutil.KindList:
		return map[string]bool{"output": true, "quiet": true, "wide": true, "no-header": true}
	}
	return map[string]bool{"output": true}
}

// pickFlags returns the flags of fs named in allow.
func pickFlags(fs *pflag.FlagSet, allow map[string]bool) *pflag.FlagSet {
	return filterFlags(fs, func(f *pflag.Flag) bool { return allow[f.Name] })
}

// sectionFlags returns the flags of fs whose cmdutil.FlagSection is section.
func sectionFlags(fs *pflag.FlagSet, section string) *pflag.FlagSet {
	return filterFlags(fs, func(f *pflag.Flag) bool {
		got := ""
		if v := f.Annotations[cmdutil.FlagSection]; len(v) > 0 {
			got = v[0]
		}
		return got == section
	})
}

// withoutHelp returns fs less --help.
func withoutHelp(fs *pflag.FlagSet) *pflag.FlagSet {
	return filterFlags(fs, func(f *pflag.Flag) bool { return f.Name != "help" })
}

func filterFlags(fs *pflag.FlagSet, keep func(*pflag.Flag) bool) *pflag.FlagSet {
	filtered := pflag.NewFlagSet("filtered", pflag.ContinueOnError)
	fs.VisitAll(func(f *pflag.Flag) {
		if keep(f) {
			filtered.AddFlag(f)
		}
	})
	return filtered
}

func hasVisibleFlags(fs *pflag.FlagSet) bool {
	visible := false
	fs.VisitAll(func(f *pflag.Flag) { visible = visible || !f.Hidden })
	return visible
}

// printFlags lists fs with aligned descriptions, wrapped to width columns
// (0: no wrapping) under the description column.
func printFlags(w io.Writer, fs *pflag.FlagSet, width int, style func(lipgloss.Style, string) string) {
	// First pass: measure the longest name+type string for alignment.
	type flagEntry struct {
		nameType string
		usage    string
		defVal   string
	}
	var entries []flagEntry
	maxLen := 0
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		var name string
		if f.Shorthand != "" {
			name = fmt.Sprintf("-%s, --%s", f.Shorthand, f.Name)
		} else {
			name = fmt.Sprintf("    --%s", f.Name)
		}
		typHint := ""
		switch {
		case f.Value.Type() == "bool":
			// A boolean whose false means something, or whose default is
			// true, is shown with the only spelling that sets it (#236).
			if _, ok := f.Annotations[cmdutil.BoolValue]; ok || f.DefValue == "true" {
				typHint = "=true|false"
			}
		case f.Value.Type() == "stringArray", f.Value.Type() == "stringSlice":
			typHint = " strings"
		default:
			typHint = " " + f.Value.Type()
		}
		nameType := name + typHint
		if len(nameType) > maxLen {
			maxLen = len(nameType)
		}
		// Quote string defaults, print everything else bare. Quoting
		// uniformly rendered numbers as (default "1") and durations as
		// (default "30s"), which reads like the flag wants a quoted literal.
		defVal := ""
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "0s" && f.DefValue != "[]" {
			if f.Value.Type() == "string" {
				defVal = fmt.Sprintf(" (default %q)", f.DefValue)
			} else {
				defVal = fmt.Sprintf(" (default %s)", f.DefValue)
			}
		}
		entries = append(entries, flagEntry{nameType: nameType, usage: f.Usage, defVal: defVal})
	})

	// Second pass: print with aligned descriptions.
	col := 2 + maxLen + 3
	for _, e := range entries {
		pad := strings.Repeat(" ", maxLen-len(e.nameType))
		lines := wrapWords(e.usage+e.defVal, descWidth(width, col))
		// The default keeps its own style when it ends up whole on the last
		// line, as it does unwrapped.
		last := len(lines) - 1
		def := strings.TrimSpace(e.defVal)
		desc := func(i int) string {
			if i == last && def != "" && strings.HasSuffix(lines[i], def) {
				return style(helpFlagDesc, strings.TrimSuffix(lines[i], def)) + style(helpUsage, def)
			}
			return style(helpFlagDesc, lines[i])
		}
		fmt.Fprintf(w, "  %s%s   %s\n", style(helpFlag, e.nameType), pad, desc(0))
		for i := 1; i < len(lines); i++ {
			fmt.Fprintln(w, strings.Repeat(" ", col)+desc(i))
		}
	}
}
