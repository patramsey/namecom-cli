package cmd

import (
	"fmt"
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

func runOpen(_ *cobra.Command, args []string) error {
	url := "https://www.name.com/account/domain/"
	if len(args) == 1 {
		url = "https://www.name.com/account/domain/details#?domain=" + args[0]
	}
	fmt.Println("Opening " + url)
	return openBrowser(url)
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
