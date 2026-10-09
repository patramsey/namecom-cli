package cmdutil

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
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
			hint := strayBoolHint(cmd, args[n:])
			if hint == "" {
				hint = "usage: " + cmd.UseLine()
			}
			return NewUsageErrorHint(fmt.Errorf("too many arguments — expected: %s", joinNames(names)), hint)
		}
		// One or more missing — name only the ones still needed.
		missing := names
		if len(args) < len(names) {
			missing = names[len(args):]
		}
		return missingArgs(cmd, missing)
	}
}

// missingArgs is the usage error for positional arguments left out: what is
// missing in the message, and the usage line in the hint, where the
// extra-argument and unknown-flag errors put theirs (#313). The usage line
// used to be in the message, so a script reading error.hint got nothing.
func missingArgs(cmd *cobra.Command, missing []string) error {
	return NewUsageErrorHint(errors.New(needsMessage(missing)), "usage: "+cmd.UseLine())
}

// NoArgs is a drop-in for cobra.NoArgs. Cobra reports an argument to a
// command that takes none as an unknown subcommand, which `domain list --all
// false` is not.
func NoArgs(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	hint := strayBoolHint(cmd, args)
	if hint == "" {
		hint = "usage: " + cmd.UseLine()
	}
	return NewUsageErrorHint(fmt.Errorf("%s takes no arguments, got %q", cmd.CommandPath(), args[0]), hint)
}

// strayBoolHint explains a "true" or "false" among extra arguments when a
// boolean flag was passed: `--autorenew false` sets --autorenew to true and
// leaves "false" as an argument, so `domain update x.com --autorenew false`
// failed with only "too many arguments" (#236). Empty when that is not what
// happened.
func strayBoolHint(cmd *cobra.Command, extra []string) string {
	val := ""
	for _, a := range extra {
		if l := strings.ToLower(a); l == "true" || l == "false" {
			val = l
			break
		}
	}
	if val == "" {
		return ""
	}
	// The command's own flags first: a global --yes is a less likely culprit.
	var local, inherited []string
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if f.Value.Type() != "bool" {
			return
		}
		if cmd.LocalNonPersistentFlags().Lookup(f.Name) != nil {
			local = append(local, f.Name)
		} else {
			inherited = append(inherited, f.Name)
		}
	})
	names := append(local, inherited...)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("a true/false value must be joined to its flag with '=': --%s=%s", names[0], val)
}

// BoolValue is a flag annotation for a boolean whose false is worth passing,
// such as `domain update --autorenew=false`. Help shows it as
// --autorenew=true|false, since `--autorenew false` does not do that.
const BoolValue = "namecom_bool_value"

// MarkBoolValue annotates the named boolean flags of fs with BoolValue.
func MarkBoolValue(fs *pflag.FlagSet, names ...string) {
	for _, n := range names {
		_ = fs.SetAnnotation(n, BoolValue, []string{"true"})
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
		return missingArgs(cmd, missing)
	}
}

// MaximumNArgs is a drop-in for cobra.MaximumNArgs, which said "accepts at
// most 1 arg(s), received 2" where ExactArgs says "too many arguments" (#327).
// The names come from the Use string's <required> and [optional] tokens.
func MaximumNArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) <= n {
			return nil
		}
		var names []string
		for _, part := range strings.Fields(cmd.Use)[1:] {
			if part == "[flags]" {
				continue
			}
			if name := strings.Trim(part, "<>[]."); name != "" && name != part {
				names = append(names, name)
			}
		}
		return NewUsageErrorHint(fmt.Errorf("too many arguments — expected: %s", joinNames(names)), "usage: "+cmd.UseLine())
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
		return UnknownCommand(args[0], c.CommandPath(), c.SuggestionsFor(args[0]))
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
func FlagError(cmd *cobra.Command, err error) error { return FlagErrorArgs(cmd, err, nil) }

// FlagErrorArgs is FlagError knowing the command line, args, so that an
// unknown flag given a value — `--years 3`, `--years=3` — is not offered a
// boolean flag such as --yes, which takes none (#324).
func FlagErrorArgs(cmd *cobra.Command, err error, args []string) error {
	if bad, ok := errors.AsType[*pflag.InvalidValueError](err); ok {
		if plain := invalidValue(bad); plain != nil {
			return NewUsageError(plain)
		}
	}
	msg := err.Error()
	name, ok := strings.CutPrefix(msg, "unknown flag: --")
	if !ok {
		return NewUsageError(err)
	}
	usage := "usage: " + cmd.UseLine()
	if s := flagSuggestions(cmd, name, givenValue(name, args)); len(s) > 0 {
		return NewUsageErrorHint(err, "did you mean "+strings.Join(s, " or ")+"? "+usage)
	}
	return NewUsageErrorHint(err, usage+" — run '"+cmd.CommandPath()+" --help' for its flags")
}

// invalidValue restates a built-in flag type's parse failure without Go's
// internals: pflag said `invalid argument "abc" for "--limit" flag:
// strconv.ParseInt: parsing "abc": invalid syntax` (#324). It returns nil for
// a failure it does not recognize, such as a custom flag type's own error,
// which already says what is wrong.
func invalidValue(e *pflag.InvalidValueError) error {
	name, value := "--"+e.GetFlag().Name, e.GetValue()
	if numErr, ok := errors.AsType[*strconv.NumError](e); ok {
		if errors.Is(numErr.Err, strconv.ErrRange) {
			return fmt.Errorf("%s is out of range, got %q", name, value)
		}
		switch numErr.Func {
		case "ParseInt", "ParseUint", "Atoi":
			return fmt.Errorf("%s must be a whole number, got %q", name, value)
		case "ParseFloat":
			return fmt.Errorf("%s must be a number, got %q", name, value)
		case "ParseBool":
			return fmt.Errorf("%s takes true or false, got %q", name, value)
		}
		return nil
	}
	if e.GetFlag().Value.Type() == "duration" {
		return fmt.Errorf("%s must be a duration such as 30s or 2m, got %q", name, value)
	}
	return nil
}

// givenValue reports whether the unknown flag --name was given a value in
// args: `--name=value`, or `--name` followed by a word that is not a flag.
// The word may instead be a positional argument; then a boolean suggestion
// is lost, and the hint still names the command's --help.
func givenValue(name string, args []string) bool {
	for i, a := range args {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, "--"+name+"=") {
			return true
		}
		if a == "--"+name {
			return i+1 < len(args) && !strings.HasPrefix(args[i+1], "-")
		}
	}
	return false
}

// flagSuggestions returns cmd's visible flags that name was probably meant to
// be: within cobra's suggestion distance, a prefix of the flag, or listed in
// its SuggestFlagFor annotation. With hasValue, boolean flags are left out.
func flagSuggestions(cmd *cobra.Command, name string, hasValue bool) []string {
	dist := cmd.SuggestionsMinimumDistance
	if dist <= 0 {
		dist = 2
	}
	name = strings.ToLower(name)
	var out []string
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Hidden || (hasValue && f.NoOptDefVal != "") {
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

// NearWord reports whether typed is likely a misspelling of name, by cobra's
// rule for command suggestions: within dist edits, ignoring case, or a prefix
// of name. For commands cobra's SuggestionsFor leaves out, such as help topics.
func NearWord(typed, name string, dist int) bool {
	typed, name = strings.ToLower(typed), strings.ToLower(name)
	return levenshtein(typed, name) <= dist || strings.HasPrefix(name, typed)
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
