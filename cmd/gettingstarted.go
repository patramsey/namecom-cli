package cmd

import (
	"fmt"
	"io"

	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/spf13/cobra"
)

// showGettingStarted reports whether cmd is a bare `namecom`, run with no
// subcommand and no --help, by someone with no credentials. That person gets
// a short getting-started banner instead of the full command list, as `gh`
// does (#239): the list is all commands they cannot run yet.
//
// CalledAs is empty unless cobra executed cmd itself, so `namecom help`,
// which executes the help command, still prints the full help.
func showGettingStarted(cmd *cobra.Command) bool {
	if cmd != cmd.Root() || cmd.CalledAs() == "" {
		return false
	}
	if help, _ := cmd.Flags().GetBool("help"); help {
		return false
	}
	return !hasCredentials(cmd)
}

// hasCredentials reports whether some source supplies both a username and a
// token, resolved as API commands resolve them, but without running
// token_cmd: a configured helper counts. Anything it cannot decide counts as
// having credentials, so an odd config gets the full help, not the banner.
func hasCredentials(cmd *cobra.Command) bool {
	f, err := config.Load()
	if err != nil {
		return true
	}
	id, err := config.Identity(f, flagOverrides(cmd))
	if err != nil {
		return true
	}
	return id.Username != "" && id.Sources.Token != ""
}

// printGettingStarted writes the banner showGettingStarted asks for.
func printGettingStarted(w io.Writer) {
	fmt.Fprintf(w, `namecom — CLI for the name.com Core API

You are not logged in. To get started, run:
  namecom auth login              # create a token at %s
  namecom auth login --sandbox    # or try the sandbox, which has its own credentials

Run 'namecom --help' to see all commands.
`, apiSettingsURL)
}
