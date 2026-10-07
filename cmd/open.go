package cmd

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var openCmd = &cobra.Command{
	Use:   "open [domain]",
	Short: "Open name.com in your browser",
	Long:  "Open the name.com account dashboard, or the management page for a domain. With no browser to open, print the URL.",
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
	target, err := openTarget(args)
	if err != nil {
		return err
	}
	// --dry-run reports the URL and opens nothing: it reported "opened": true
	// and launched the browser (#292).
	if cmdutil.IsDryRun(cmd) {
		return renderOpenDryRun(cmdutil.Out(cmd), target)
	}
	return renderOpen(cmdutil.Out(cmd), target, openBrowser(target))
}

// renderOpenDryRun is renderOpen for a browser that was not launched: the
// URL, with "opened": false and "dryRun": true in JSON and YAML.
func renderOpenDryRun(out *output.Config, target string) error {
	res := openResult{URL: target, DryRun: true}
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(res)
	case output.FormatYAML:
		return out.YAML(res)
	}
	fmt.Fprintf(out.Writer, "%s would open %s\n", out.Amber("dry-run:"), target)
	return nil
}

// openResult is the JSON/YAML shape of `namecom open`. The URL is always in it:
// a script asking for the link should get it whether or not a browser opened.
type openResult struct {
	URL    string `json:"url"              yaml:"url"`
	Opened bool   `json:"opened"           yaml:"opened"`
	DryRun bool   `json:"dryRun,omitempty" yaml:"dryRun,omitempty"`
}

// renderOpen reports the outcome of handing target to a browser. Failing to
// open one is not an error: on a headless box, over SSH or in a container
// there is often nothing to open it with, and the URL itself is the useful
// result. launchErr is only shown, in table mode, as the reason.
func renderOpen(out *output.Config, target string, launchErr error) error {
	res := openResult{URL: target, Opened: launchErr == nil}
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(res)
	case output.FormatYAML:
		return out.YAML(res)
	}
	if res.Opened {
		// Hint, not bare fmt.Println: this is commentary, not data.
		out.Hint("Opening " + target)
		return nil
	}
	out.Warn("could not open a browser: " + launchErr.Error())
	fmt.Fprintln(out.Writer, "Open this URL in your browser: "+target)
	return nil
}

// startCommand runs name with args. With wait it runs in the foreground on
// this terminal and its exit status counts: a $BROWSER entry may be a terminal
// browser such as w3m, which needs the terminal and must finish before we
// exit. Without wait it is only started, as a desktop opener hands the URL off
// and a GUI browser may outlive us. A variable so tests can stand in for it:
// no test may launch a real browser.
var startCommand = func(wait bool, name string, args ...string) error {
	c := exec.Command(name, args...) //nolint:gosec // argv from $BROWSER or a literal opener; target built by openTarget from a validated domain
	if !wait {
		return c.Start()
	}
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// openBrowser hands target to $BROWSER, then to the platform's opener, and
// returns the last error if none of them starts. Callers must pass a URL they
// built themselves from validated input — see openTarget. No shell is
// involved, so there is no metacharacter injection here, but the leading-dash
// argument confusion above is real.
//
// $BROWSER follows the common convention: a list of commands separated like
// PATH, tried in order, with %s marking where the URL goes and the URL
// appended when there is no %s.
func openBrowser(target string) error {
	type launcher struct {
		argv []string
		wait bool
	}
	var cmds []launcher
	for _, entry := range filepath.SplitList(os.Getenv("BROWSER")) {
		argv := strings.Fields(entry)
		if len(argv) == 0 {
			continue
		}
		placed := false
		for i, a := range argv {
			if strings.Contains(a, "%s") {
				argv[i] = strings.ReplaceAll(a, "%s", target)
				placed = true
			}
		}
		if !placed {
			argv = append(argv, target)
		}
		cmds = append(cmds, launcher{argv, true})
	}
	switch runtime.GOOS {
	case "darwin":
		cmds = append(cmds, launcher{[]string{"open", target}, false})
	case "windows":
		cmds = append(cmds, launcher{[]string{"rundll32", "url.dll,FileProtocolHandler", target}, false})
	default:
		cmds = append(cmds, launcher{[]string{"xdg-open", target}, false})
	}
	var err error
	for _, l := range cmds {
		if err = startCommand(l.wait, l.argv[0], l.argv[1:]...); err == nil {
			return nil
		}
	}
	return err
}
