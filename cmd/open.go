package cmd

import (
	"net/url"
	"os/exec"
	"runtime"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open [domain]",
	Short: "Open name.com in your browser",
	Long:  "Opens the name.com account dashboard, or the management page for a specific domain.",
	Example: `  namecom open
  namecom open example.com`,
	Args:              cobra.MaximumNArgs(1),
	RunE:              runOpen,
	ValidArgsFunction: cmdutil.CompleteDomains,
}

func init() {
	openCmd.GroupID = "utilities"
	rootCmd.AddCommand(openCmd)
}

// openTarget builds the URL to hand to the browser. Split out from runOpen so
// the validation below is reachable from a test without launching a browser.
//
// The argument is validated before interpolating, because it ends up as an
// argv element of `open`/`xdg-open`/`rundll32`. Those treat a leading `-` as a
// flag, so an unchecked argument is not merely a bad URL — it is an argument
// to somebody else's program. Escaping alone would not close that: QueryEscape
// leaves `-` untouched.
func openTarget(args []string) (string, error) {
	if len(args) == 0 {
		return "https://www.name.com/account/domain/", nil
	}
	domain := cmdutil.CanonicalDomain(args[0])
	if err := cmdutil.ValidDomainName(domain); err != nil {
		return "", err
	}
	return "https://www.name.com/account/domain/details#?domain=" + url.QueryEscape(domain), nil
}

func runOpen(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	target, err := openTarget(args)
	if err != nil {
		return err
	}
	// Route through the output config rather than bare fmt.Println: this is
	// commentary, not data, and writing it to raw stdout corrupts a pipe. Hint
	// already suppresses itself outside table mode.
	out.Hint("Opening " + target)
	return openBrowser(target)
}

// openBrowser hands target to the platform's browser opener. Callers must pass
// a URL they built themselves from validated input — see openTarget.
// exec.Command does not invoke a shell, so there is no metacharacter injection
// here, but the leading-dash argument confusion above is real.
func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target) //nolint:gosec // literal argv[0], target built by openTarget from a validated domain
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target) //nolint:gosec // literal argv[0], target built by openTarget from a validated domain
	default:
		cmd = exec.Command("xdg-open", target) //nolint:gosec // literal argv[0], target built by openTarget from a validated domain
	}
	return cmd.Start()
}
