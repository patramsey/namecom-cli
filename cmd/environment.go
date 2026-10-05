package cmd

import "github.com/spf13/cobra"

// environmentCmd is the `namecom help environment` topic (#237). The
// variables were documented only in the README, and --sandbox did not name
// its own. TestEnvironmentTopic fails when a NAMECOM_* variable read anywhere
// in the source is missing from it.
//
// It has no Run, which makes it a help topic: `namecom help environment` and
// `namecom environment` both print it, and root help lists it under "Help
// Topics" rather than with the commands.
var environmentCmd = &cobra.Command{
	Use:   "environment",
	Short: "Environment variables namecom reads",
	Long: `Environment variables namecom reads. A flag beats its variable, and a variable
beats the config file.

  NAMECOM_USERNAME            API username. --username overrides it.
  NAMECOM_TOKEN               API token. --token overrides it.
  NAMECOM_PROFILE             Profile to use from the config file. --profile
                              overrides it.
  NAMECOM_SANDBOX             true for the sandbox API (api.dev.name.com), false
                              for production, whatever the profile says.
                              --sandbox overrides it. Accepts true/false, yes/no,
                              on/off and 1/0; anything else is an error.
  NAMECOM_CONFIG              Config file to use instead of the default location.
                              When it is set, no other location is read.
  NAMECOM_NO_UPDATE_NOTIFIER  Set to 1 to never print the new-release notice.

Other variables:

  NO_COLOR                    Set to anything to turn colour off. --color
                              overrides it.
  CLICOLOR_FORCE              Set to 1 to keep colour on when output is not a
                              terminal. --color overrides it.
  BROWSER                     The browser 'namecom open' starts.

Run 'namecom auth status' to see which config file and profile are in use.`,
}

func init() {
	rootCmd.AddCommand(environmentCmd)
}
