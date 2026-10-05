package cmdutil

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
			// The usage line shows where the rest belongs: `set-ns D ns1 ns2`
			// is answered with "… set-ns <domain> --ns ns1…,ns2…" (#234).
			return NewUsageErrorHint(fmt.Errorf("too many arguments — expected: %s", joinNames(names)),
				"usage: "+cmd.UseLine())
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
		return unknownCommandError(fmt.Sprintf("unknown command %q for %q", args[0], c.CommandPath()),
			c.CommandPath(), c.SuggestionsFor(args[0]))
	}
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		return c.Help()
	}
	return cmd
}

// SuggestFlagFor is a flag annotation listing other names users type for it,
// so an unknown flag can be answered with the right one when the spelling is
// nothing alike: `--nameservers` for set-ns's `--ns`.
const SuggestFlagFor = "namecom_suggest_for"

// FlagError is the root command's flag-error function. Every flag-parse
// failure is a usage error (exit 2); an unknown flag also gets a hint with the
// nearest flags the command has and its usage line (#234). It used to be a
// bare "unknown flag: --nameservers".
func FlagError(cmd *cobra.Command, err error) error {
	msg := err.Error()
	name, ok := strings.CutPrefix(msg, "unknown flag: --")
	if !ok {
		return NewUsageError(err)
	}
	usage := "usage: " + cmd.UseLine()
	if s := flagSuggestions(cmd, name); len(s) > 0 {
		return NewUsageErrorHint(err, "did you mean "+strings.Join(s, " or ")+"? "+usage)
	}
	return NewUsageErrorHint(err, usage+" — run '"+cmd.CommandPath()+" --help' for its flags")
}

// flagSuggestions returns cmd's visible flags that name was probably meant to
// be: within cobra's suggestion distance, a prefix of the flag, or listed in
// its SuggestFlagFor annotation.
func flagSuggestions(cmd *cobra.Command, name string) []string {
	dist := cmd.SuggestionsMinimumDistance
	if dist <= 0 {
		dist = 2
	}
	name = strings.ToLower(name)
	var out []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}
		match := levenshtein(name, f.Name) <= dist ||
			(len(name) >= 2 && strings.HasPrefix(f.Name, name)) ||
			slices.Contains(f.Annotations[SuggestFlagFor], name)
		if match {
			out = append(out, "--"+f.Name)
		}
	})
	sort.Strings(out)
	return out
}

// levenshtein is the edit distance between a and b, as cobra computes it for
// command suggestions (its own is unexported).
func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// pageNumber is the set of types the two API clients use for page numbers.
type pageNumber interface {
	~int | ~int32
}

// NextPage decides whether a paginated walk continues, given the page just
// fetched and the nextPage/lastPage the API reported alongside it.
//
// The only stopping condition used to be `nextPage == nil || *nextPage == 0`,
// which trusts the server to eventually stop saying "there is more". A server
// that keeps answering `nextPage: 2` — a caching bug, a filter interaction, a
// proxy replaying a response — walked forever at the client's full rate limit.
// Every list command and record-ID completion had it; `dns list --all` against
// such a server never returned. `domain list` looked safe because its parallel
// path bounds on lastPage, but the sequential fallback it takes when lastPage
// is absent looped on nextPage alone until it too was routed through here.
//
// Two guards, both cheap: the page number must advance, and it must not run
// past lastPage when the API reports one.
//
// Generic over the page type because the two clients disagree about it: the
// generated client reports int32, the Core SDK reports int. The guards are the
// same either way, and inference means existing call sites are unchanged.
func NextPage[T pageNumber](current T, nextPage, lastPage *T) (T, bool) {
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
