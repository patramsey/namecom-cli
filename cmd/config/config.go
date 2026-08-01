// Package config implements the `namecom config` command group.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

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

// profileView is the serialization-safe projection of a config.Profile.
//
// config.Profile must never be marshalled directly: it carries the live API
// token and token_cmd, and neither has a `json:"-"` tag. Because
// output.DefaultConfig() selects JSON whenever stdout is not a TTY, marshalling
// it wrote every profile's credentials straight into any pipe, redirect, or CI
// log. This type exposes only what the table view already showed, plus booleans
// saying how the credential is supplied.
type profileView struct {
	Name         string `json:"name" yaml:"name"`
	Username     string `json:"username" yaml:"username"`
	Endpoint     string `json:"endpoint" yaml:"endpoint"`
	Default      bool   `json:"default" yaml:"default"`
	HasToken     bool   `json:"hasToken" yaml:"hasToken"`
	UsesTokenCmd bool   `json:"usesTokenCmd" yaml:"usesTokenCmd"`
}

func redactProfiles(cfgFile *config.File, names []string) []profileView {
	views := make([]profileView, 0, len(names))
	for _, name := range names {
		p := cfgFile.Profiles[name]
		views = append(views, profileView{
			Name:         name,
			Username:     p.Username,
			Endpoint:     endpointFor(p.Sandbox),
			Default:      name == cfgFile.Default,
			HasToken:     p.Token != "",
			UsesTokenCmd: p.TokenCmd != "",
		})
	}
	return views
}

// tokenCmdSummary reduces a token_cmd to the helper program it invokes.
// The arguments can themselves carry a secret — an inline bearer token, a
// vault path — so echoing the full command puts a credential on screen. The
// program name alone answers "where does my token come from?"; the full
// command is in the config file for anyone who needs it.
func tokenCmdSummary(cmd string) string {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) == 1 {
		return fields[0]
	}
	return fields[0] + " …"
}

func endpointFor(sandbox bool) string {
	if sandbox {
		return "api.dev.name.com"
	}
	return "api.name.com"
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
		return out.JSON(redactProfiles(cfgFile, names))
	case output.FormatYAML:
		return out.YAML(redactProfiles(cfgFile, names))
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

	// Honor the same profile selection every other command uses: --profile,
	// then NAMECOM_PROFILE, then the file's default. Reading only the default
	// meant `config show --profile sandbox` — the command's own documented
	// example — reported the production profile's endpoint instead.
	profileName := cmdutil.Overrides(cmd).Profile
	if profileName == "" {
		profileName = os.Getenv("NAMECOM_PROFILE")
	}
	if profileName == "" {
		profileName = cfgFile.Default
	}
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
		tokenDisplay = out.Dim(fmt.Sprintf("(from token_cmd: %s)", tokenCmdSummary(p.TokenCmd)))
	}

	path, _ := config.ActivePath()

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
