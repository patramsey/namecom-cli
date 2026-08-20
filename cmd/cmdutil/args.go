package cmdutil

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// ExactArgs is a drop-in for cobra.ExactArgs that produces a human-readable
// error by parsing the <arg> placeholders from the command's Use string.
//
//	"list <domain>"             → "domain is required"
//	"update <domain> <id>"      → "domain and id are required"
//	"update example.com <id>"   → "id is required" (when domain is already supplied)
func ExactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == n {
			return nil
		}
		names := argNames(cmd.Use)
		if len(args) > n {
			return NewUsageError(fmt.Errorf("too many arguments — expected: %s", joinNames(names)))
		}
		// One or more missing — name only the ones still needed.
		missing := names
		if len(args) < len(names) {
			missing = names[len(args):]
		}
		return NewUsageError(fmt.Errorf("%s — try: %s", needsMessage(missing), cmd.UseLine()))
	}
}

// MinimumNArgs is a drop-in for cobra.MinimumNArgs with a readable error.
func MinimumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) >= n {
			return nil
		}
		names := argNames(cmd.Use)
		missing := names
		if len(args) < len(names) {
			missing = names[len(args):]
		}
		return NewUsageError(fmt.Errorf("%s — try: %s", needsMessage(missing), cmd.UseLine()))
	}
}

// argNames parses <placeholder> tokens from a cobra Use string.
// e.g. "update <domain> <id>" → ["domain", "id"]
func argNames(use string) []string {
	var names []string
	for _, part := range strings.Fields(use) {
		if strings.HasPrefix(part, "<") && strings.HasSuffix(part, ">") {
			names = append(names, part[1:len(part)-1])
		}
	}
	return names
}

func needsMessage(names []string) string {
	if len(names) == 0 {
		return "missing required argument"
	}
	verb := "is"
	if len(names) > 1 {
		verb = "are"
	}
	return joinNames(names) + " " + verb + " required"
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// GroupCmd wires a command group — a parent that exists only to hold
// subcommands — so a mistyped subcommand fails instead of succeeding quietly.
//
// Cobra only checks for unknown commands on the ROOT command: legacyArgs()
// returns nil for any parent that itself has a parent. So `namecom domian`
// errored with a suggestion, while `namecom domain regsiter example.com`
// printed the group's help and exited 0. In a script that reads as success —
// `namecom domain regsiter foo.com && deploy` ran deploy.
//
// Bare `namecom domain` keeps its old behavior of printing help and exiting 0,
// which is what a user typing a group name to browse it expects.
func GroupCmd(cmd *cobra.Command) *cobra.Command {
	// Cobra defaults this to 2, but only on the root command, inside the
	// unknown-command path we are replacing here. Left at zero, SuggestionsFor
	// matches nothing and every typo loses its "Did you mean" line.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	cmd.Args = func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return NewUsageError(fmt.Errorf("unknown command %q for %q%s",
			args[0], c.CommandPath(), suggestionHint(c.SuggestionsFor(args[0]))))
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return c.Help()
	}
	return cmd
}

// suggestionHint renders cobra's near-miss list the way cobra renders it on the
// root command, so a typo reads the same wherever in the tree it happens.
func suggestionHint(suggestions []string) string {
	if len(suggestions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nDid you mean this?\n")
	for _, s := range suggestions {
		fmt.Fprintf(&b, "\t%s\n", s)
	}
	return b.String()
}

// NextPage decides whether a paginated walk continues, given the page just
// fetched and the nextPage/lastPage the API reported alongside it.
//
// The only stopping condition used to be `nextPage == nil || *nextPage == 0`,
// which trusts the server to eventually stop saying "there is more". A server
// that keeps answering `nextPage: 2` — a caching bug, a filter interaction, a
// proxy replaying a response — walked forever at the client's full rate limit.
// `domain list` escaped it by bounding on lastPage; the other seven list
// commands and record-ID completion did not, and `dns list --all` against such
// a server never returned.
//
// Two guards, both cheap: the page number must advance, and it must not run
// past lastPage when the API reports one.
func NextPage(current int32, nextPage, lastPage *int32) (int32, bool) {
	if nextPage == nil || *nextPage == 0 {
		return current, false
	}
	next := *nextPage
	if next <= current {
		return current, false
	}
	if lastPage != nil && *lastPage > 0 && next > *lastPage {
		return current, false
	}
	return next, true
}
