package cmd

import (
	"fmt"
	"io"
	"strings"

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
	color := output.DefaultConfig().ColorEnabled()
	printHelp(w, cmd, color)
}

func printHelp(w io.Writer, cmd *cobra.Command, color bool) {
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
		fmt.Fprintf(w, "  %s%s   %s\n",
			style(helpCmd, c.Name()),
			padding,
			style(helpCmdDesc, c.Short),
		)
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
			printFlags(w, fs, color, style)
			fmt.Fprintln(w)
		}
	} else if local := withoutHelp(cmd.LocalFlags()); hasVisibleFlags(local) {
		fmt.Fprintln(w, style(helpHeading, "Flags:"))
		printFlags(w, local, color, style)
		fmt.Fprintln(w)
	}

	// A command that runs shows the global flags that apply to it (#237). A
	// group runs nothing, so it shows none.
	group := cmd.HasAvailableSubCommands()
	if !isRoot && !group && cmd.HasAvailableInheritedFlags() {
		fs := pickFlags(cmd.InheritedFlags(), globalFlagNames(cmd))
		if hasVisibleFlags(fs) {
			fmt.Fprintln(w, style(helpHeading, "Global Flags:"))
			printFlags(w, fs, color, style)
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
		fmt.Fprintln(w, "  "+style(helpCmdDesc, line))
	}
	fmt.Fprintln(w)
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
		return map[string]bool{"output": true, "quiet": true, "yes": true, "dry-run": true}
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

func printFlags(w io.Writer, fs *pflag.FlagSet, _ bool, style func(lipgloss.Style, string) string) {
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
		case f.Value.Type() == "stringArray":
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
	for _, e := range entries {
		pad := strings.Repeat(" ", maxLen-len(e.nameType))
		fmt.Fprintf(w, "  %s%s   %s%s\n",
			style(helpFlag, e.nameType),
			pad,
			style(helpFlagDesc, e.usage),
			style(helpUsage, e.defVal),
		)
	}
}
