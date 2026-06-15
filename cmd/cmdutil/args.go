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
			return fmt.Errorf("too many arguments — expected: %s", joinNames(names))
		}
		// One or more missing — name only the ones still needed.
		missing := names
		if len(args) < len(names) {
			missing = names[len(args):]
		}
		return fmt.Errorf("%s — try: %s", needsMessage(missing), cmd.UseLine())
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
		return fmt.Errorf("%s — try: %s", needsMessage(missing), cmd.UseLine())
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
