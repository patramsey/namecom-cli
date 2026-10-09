package cmdutil

import (
	"strings"

	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

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

// RawOutputAnnotation marks a command that prints what it received as it
// received it, `namecom api`, where --quiet does not apply. Help leaves it
// out of the command's global flags (#293).
const RawOutputAnnotation = "namecom_raw_output"

// MarkRawOutput marks cmd as printing its output as received.
func MarkRawOutput(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[RawOutputAnnotation] = "true"
}

// Kind returns cmd's kind: KindWrite, KindList, or "" for a read.
func Kind(cmd *cobra.Command) string { return cmd.Annotations[KindAnnotation] }

// FlagSection is the flag annotation naming the section of root help a
// global flag is listed under, such as "Output". A flag without one is
// listed first, under "Flags".
const FlagSection = "namecom_flag_section"

// ResultAnnotation is the command annotation naming the keys of the JSON
// document a write prints once it is made, comma-separated. See SetResult.
const ResultAnnotation = "namecom_result"

// SetResult names the keys of the document cmd, a write, prints when the
// write is made: the keys -o json prints and --fields picks from. A write
// without it prints what output.Config.Success and Results print, whose keys
// are output.ResultKeys.
//
// The keys are known before anything is sent, so CheckResultFields can refuse
// a mistyped --fields then. Found after the write, it could only be warned
// about, since the change had been made (#325).
func SetResult(cmd *cobra.Command, keys ...string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[ResultAnnotation] = strings.Join(keys, ",")
}

// ResultKeys returns the keys of what cmd, a write, prints once it is made.
func ResultKeys(cmd *cobra.Command) []string {
	if v, ok := cmd.Annotations[ResultAnnotation]; ok {
		return strings.Split(v, ",")
	}
	return output.ResultKeys()
}

// CheckResultFields refuses, as a usage error, a --fields that names a key a
// write's result does not have, before the command runs and so before any
// request is sent. A dry run prints the request instead, which EndFilter
// checks the fields against; `api` prints whatever the API returned, whose
// keys are not known in advance.
func CheckResultFields(cmd *cobra.Command) error {
	if Kind(cmd) != KindWrite || cmd.Annotations[RawOutputAnnotation] != "" || IsDryRun(cmd) {
		return nil
	}
	if err := Out(cmd).CheckFields(ResultKeys(cmd)); err != nil {
		return NewUsageError(err)
	}
	return nil
}
