// Package config implements the `namecom config` command group.
package config

import (
	"fmt"
	"sort"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

// Cmd is the `namecom config` parent command.
var Cmd = &cobra.Command{
	Use:   "config",
	Short: "Manage CLI configuration and profiles",
}

var listProfilesCmd = &cobra.Command{
	Use:     "list-profiles",
	Short:   "List all configured credential profiles",
	Example: `  namecom config list-profiles`,
	Args:    cobra.NoArgs,
	RunE:    runListProfiles,
}

var useCmd = &cobra.Command{
	Use:   "use <profile>",
	Short: "Set the default credential profile",
	Example: `  namecom config use sandbox
  namecom config use default`,
	Args: cmdutil.ExactArgs(1),
	RunE: runUse,
}

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show resolved credentials for the active profile",
	Example: `  namecom config show
  namecom config show --profile sandbox`,
	Args: cobra.NoArgs,
	RunE: runShow,
}

func init() {
	Cmd.AddCommand(listProfilesCmd, useCmd, showCmd)
}

func runListProfiles(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if len(cfgFile.Profiles) == 0 {
		out.Warn("no profiles configured — run 'namecom auth login' to set one up")
		return nil
	}

	names := make([]string, 0, len(cfgFile.Profiles))
	for name := range cfgFile.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(cfgFile.Profiles)
	case output.FormatYAML:
		return out.YAML(cfgFile.Profiles)
	default:
		rows := make([][]string, 0, len(names))
		for _, name := range names {
			p := cfgFile.Profiles[name]
			endpoint := "api.name.com"
			if p.Sandbox {
				endpoint = "api.dev.name.com"
			}
			def := ""
			if name == cfgFile.Default {
				def = out.BoolBadge(true)
			}
			rows = append(rows, []string{name, p.Username, endpoint, def})
		}
		out.Table([]string{"PROFILE", "USERNAME", "ENDPOINT", "DEFAULT"}, rows)
		out.Count(len(names), "profile")
	}
	return nil
}

func runUse(cmd *cobra.Command, args []string) error {
	out := cmdutil.Out(cmd)
	profile := args[0]

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if _, ok := cfgFile.Profiles[profile]; !ok {
		return fmt.Errorf("profile %q not found — run 'namecom config list-profiles' to see available profiles", profile)
	}
	cfgFile.Default = profile
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	out.Success(fmt.Sprintf("Default profile set to %q", profile))
	return nil
}

func runShow(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	profileName := cfgFile.Default
	if profileName == "" {
		profileName = "default"
	}
	p, ok := cfgFile.Profiles[profileName]
	if !ok {
		return fmt.Errorf("no profile %q configured — run 'namecom auth login' to set up credentials", profileName)
	}

	endpoint := "api.name.com"
	if p.Sandbox {
		endpoint = "api.dev.name.com"
	}
	tokenDisplay := "••••••••"
	if p.TokenCmd != "" {
		tokenDisplay = out.Dim(fmt.Sprintf("(from token_cmd: %s)", p.TokenCmd))
	}

	path, _ := config.Path()

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(map[string]string{
			"profile":  profileName,
			"username": p.Username,
			"endpoint": endpoint,
			"config":   path,
		})
	case output.FormatYAML:
		return out.YAML(map[string]string{
			"profile":  profileName,
			"username": p.Username,
			"endpoint": endpoint,
			"config":   path,
		})
	default:
		out.KVTable([][]string{
			{"Profile", profileName},
			{"Username", p.Username},
			{"Token", tokenDisplay},
			{"Endpoint", endpoint},
			{"Config file", out.Dim(path)},
		})
	}
	return nil
}
