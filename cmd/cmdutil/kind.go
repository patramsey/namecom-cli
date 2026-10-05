package cmdutil

import "github.com/spf13/cobra"

// KindAnnotation is the command annotation saying what kind of command it is,
// so help can show the global flags that apply to it (#237). Every leaf
// showed --dry-run and --yes, read-only `domain get` included, and no list
// showed --wide or --no-header.
const KindAnnotation = "namecom_kind"

// Command kinds. A command with neither is a read.
const (
	// KindWrite is a command that changes something and honours --dry-run
	// and --yes.
	KindWrite = "write"
	// KindList is a command that prints a table of many rows, where --quiet,
	// --wide and --no-header apply.
	KindList = "list"
)

// MarkWrite marks cmds as writes.
func MarkWrite(cmds ...*cobra.Command) { markKind(KindWrite, cmds) }

// MarkList marks cmds as lists. AddPageFlags does it for paged lists.
func MarkList(cmds ...*cobra.Command) { markKind(KindList, cmds) }

func markKind(kind string, cmds []*cobra.Command) {
	for _, c := range cmds {
		if c.Annotations == nil {
			c.Annotations = map[string]string{}
		}
		c.Annotations[KindAnnotation] = kind
	}
}

// Kind returns cmd's kind: KindWrite, KindList, or "" for a read.
func Kind(cmd *cobra.Command) string { return cmd.Annotations[KindAnnotation] }

// FlagSection is the flag annotation naming the section of root help a
// global flag is listed under, such as "Output". A flag without one is
// listed first, under "Flags".
const FlagSection = "namecom_flag_section"
