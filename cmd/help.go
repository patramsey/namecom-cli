package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/namedotcom/namecom-cli/internal/output"
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

// styledHelp is a cobra help function that renders styled output using Lip Gloss.
func styledHelp(cmd *cobra.Command, _ []string) {
	w := cmd.OutOrStdout()
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

	// Description
	fmt.Fprintln(w)
	desc := cmd.Long
	if desc == "" {
		desc = cmd.Short
	}
	// Bold the binary name in the long description.
	if color && cmd == cmd.Root() {
		desc = strings.Replace(desc, "namecom", style(helpBrand, "namecom"), 1)
	}
	fmt.Fprintln(w, desc)
	fmt.Fprintln(w)

	// Usage line
	fmt.Fprintln(w, style(helpHeading, "Usage:"))
	fmt.Fprintf(w, "  %s\n\n", style(helpUsage, cmd.UseLine()))

	// Subcommands — rendered grouped when groups are defined, flat otherwise.
	cmds := cmd.Commands()
	available := make([]*cobra.Command, 0, len(cmds))
	for _, c := range cmds {
		if c.IsAvailableCommand() {
			available = append(available, c)
		}
	}
	if len(available) > 0 {
		maxLen := 0
		for _, c := range available {
			if l := len(c.Name()); l > maxLen {
				maxLen = l
			}
		}
		printCmdLine := func(c *cobra.Command) {
			padding := strings.Repeat(" ", maxLen-len(c.Name()))
			fmt.Fprintf(w, "  %s%s   %s\n",
				style(helpCmd, c.Name()),
				padding,
				style(helpCmdDesc, c.Short),
			)
		}

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

	// Local flags (non-inherited)
	if cmd.HasAvailableLocalFlags() {
		fmt.Fprintln(w, style(helpHeading, "Flags:"))
		printFlags(w, cmd.LocalFlags(), color, style)
		fmt.Fprintln(w)
	}

	// For non-root commands show only the most commonly-used global flags inline;
	// refer users to root --help for the full list.
	if cmd.HasAvailableInheritedFlags() && cmd != cmd.Root() {
		essential := essentialGlobalFlagNames()
		var hasEssential bool
		cmd.InheritedFlags().VisitAll(func(f *pflag.Flag) {
			if !f.Hidden && essential[f.Name] {
				hasEssential = true
			}
		})
		if hasEssential {
			fmt.Fprintln(w, style(helpHeading, "Global Flags:"))
			printFilteredFlags(w, cmd.InheritedFlags(), essential, color, style)
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, style(helpCmdDesc, `Run "namecom --help" to see all global options.`))
		fmt.Fprintln(w)
	}

	// Aliases
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(w, "%s  %s\n\n", style(helpHeading, "Aliases:"), strings.Join(cmd.Aliases, ", "))
	}

	// Examples
	if cmd.Example != "" {
		fmt.Fprintln(w, style(helpHeading, "Examples:"))
		fmt.Fprintln(w, cmd.Example)
		fmt.Fprintln(w)
	}

	// Footer hint
	if cmd.HasAvailableSubCommands() {
		hint := `Use "` + cmd.CommandPath() + ` [command] --help" for more information about a command.`
		fmt.Fprintln(w, style(helpCmdDesc, hint))
		fmt.Fprintln(w)
	}
}

// essentialGlobalFlagNames returns the subset of global flags shown on subcommand help pages.
// Noisy flags (--debug, --timeout, --token, etc.) are omitted; users can run "namecom --help"
// to see the full list.
func essentialGlobalFlagNames() map[string]bool {
	return map[string]bool{
		"output":  true,
		"quiet":   true,
		"yes":     true,
		"dry-run": true,
	}
}

func printFilteredFlags(w io.Writer, fs *pflag.FlagSet, allow map[string]bool, color bool, style func(lipgloss.Style, string) string) {
	filtered := pflag.NewFlagSet("filtered", pflag.ContinueOnError)
	fs.VisitAll(func(f *pflag.Flag) {
		if allow[f.Name] {
			filtered.AddFlag(f)
		}
	})
	printFlags(w, filtered, color, style)
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
		if f.Value.Type() != "bool" && f.Value.Type() != "stringArray" {
			typHint = " " + f.Value.Type()
		} else if f.Value.Type() == "stringArray" {
			typHint = " strings"
		}
		nameType := name + typHint
		if len(nameType) > maxLen {
			maxLen = len(nameType)
		}
		defVal := ""
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && f.DefValue != "0s" && f.DefValue != "[]" {
			defVal = fmt.Sprintf(" (default %q)", f.DefValue)
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
