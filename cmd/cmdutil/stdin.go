package cmdutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// StdinArg is the argument that stands for "read the rest from stdin", as in
// `namecom domain list -q | namecom domain check -`.
const StdinArg = "-"

// ExpandStdinArgs returns args with a "-" replaced, in place, by the names
// read from cmd's stdin. Args without a "-" are returned unchanged and stdin
// is not touched.
//
// Stdin holds one name per line. Blank lines are skipped and a '#' starts a
// comment, so a hand-kept list file can be piped in as it is. Surrounding
// whitespace, a Windows line ending included, is trimmed.
//
// Stdin can be read once, so a second "-" is a usage error, as is a stdin
// that yields no names: `check -` on an empty pipe checking nothing and
// exiting 0 would read as success.
func ExpandStdinArgs(cmd *cobra.Command, args []string) ([]string, error) {
	at := -1
	for i, a := range args {
		if a != StdinArg {
			continue
		}
		if at != -1 {
			return nil, usagef("'-' (read from stdin) can be given only once")
		}
		at = i
	}
	if at == -1 {
		return args, nil
	}
	names, err := readNames(cmd.InOrStdin())
	if err != nil {
		return nil, fmt.Errorf("reading names from stdin: %w", err)
	}
	if len(names) == 0 {
		return nil, NewUsageErrorHint(errors.New("'-' was given but stdin held no names"),
			"pipe in one name per line, e.g. 'namecom domain list -q | "+cmd.CommandPath()+" -'")
	}
	out := make([]string, 0, len(args)-1+len(names))
	out = append(out, args[:at]...)
	out = append(out, names...)
	return append(out, args[at+1:]...), nil
}

// readNames splits r into names: one per line, comments and blanks dropped.
func readNames(r io.Reader) ([]string, error) {
	var names []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, sc.Err()
}
