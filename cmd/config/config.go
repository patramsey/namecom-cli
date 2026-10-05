// Package config implements the `namecom config` command group.
package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	"github.com/patramsey/namecom-cli/internal/api"
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
	Args:              cmdutil.ExactArgs(1),
	ValidArgsFunction: cmdutil.CompleteProfiles,
	RunE:              runUse,
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
	cmdutil.GroupCmd(Cmd)
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
//
// Default marks the active profile, the one API commands would use, not merely
// the file's `default:` key; the name is kept for existing scripts.
type profileView struct {
	Name         string `json:"name" yaml:"name"`
	Username     string `json:"username" yaml:"username"`
	Endpoint     string `json:"endpoint" yaml:"endpoint"`
	Default      bool   `json:"default" yaml:"default"`
	HasToken     bool   `json:"hasToken" yaml:"hasToken"`
	UsesTokenCmd bool   `json:"usesTokenCmd" yaml:"usesTokenCmd"`
}

func redactProfiles(cfgFile *config.File, names []string, active string) []profileView {
	views := make([]profileView, 0, len(names))
	for _, name := range names {
		p := cfgFile.Profiles[name]
		views = append(views, profileView{
			Name:         name,
			Username:     p.Username,
			Endpoint:     api.DefaultBaseURL(p.Sandbox),
			Default:      name == active,
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

	// Mark the profile API commands would use, resolved as config show and
	// auth status resolve it: --profile, NAMECOM_PROFILE, the `default:` key,
	// then the implied default. Comparing against the `default:` key alone
	// marked a different profile than the one in use when NAMECOM_PROFILE was
	// set, and none at all for a lone profile with no key.
	active := config.ActiveProfile(cfgFile, cmdutil.Overrides(cmd).Profile)

	if out.Quiet(names...) {
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(redactProfiles(cfgFile, names, active))
	case output.FormatYAML:
		return out.YAML(redactProfiles(cfgFile, names, active))
	default:
		rows := make([][]string, 0, len(names))
		for _, name := range names {
			p := cfgFile.Profiles[name]
			// The URL, as config show and auth status print it (#135, #187).
			endpoint := api.DefaultBaseURL(p.Sandbox)
			def := ""
			if name == active {
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
	if cmdutil.IsDryRun(cmd) {
		PreviewChange(out, Change{
			Action:  "set_default",
			Profile: profile,
			Default: profile,
			Summary: fmt.Sprintf("set the default profile to %q", profile),
		})
		return nil
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

	// Describe exactly what API commands would use: config.Identity applies
	// Resolve's precedence for the profile (--profile, NAMECOM_PROFILE, the
	// file's default, the implied default), the username and the endpoint. A
	// chain of its own here reported the wrong profile or endpoint, and told a
	// user with one working profile to run `auth login`, which overwrites.
	id, err := config.Identity(cfgFile, cmdutil.Overrides(cmd))
	if err != nil {
		return cmdutil.NewUsageError(err)
	}
	profileName := id.Profile
	p, ok := cfgFile.Profiles[profileName]
	if !ok {
		switch {
		case len(cfgFile.Profiles) == 0:
			return fmt.Errorf("no profiles configured — run 'namecom auth login' to set up credentials")
		case profileName == "":
			return fmt.Errorf("%d profiles exist but none is the default — pass --profile or run 'namecom config use <profile>'", len(cfgFile.Profiles))
		}
		return fmt.Errorf("no profile %q configured — run 'namecom auth login' to set up credentials", profileName)
	}

	// The base URL, scheme included, as auth status prints it. A bare host here
	// put the same value in two forms across the two commands.
	endpoint := api.DefaultBaseURL(id.Sandbox)
	tokenDisplay := "••••••••" //nolint:gosec // G101 false positive: a mask shown in place of the token, not a credential
	if p.TokenCmd != "" {
		tokenDisplay = out.Dim(fmt.Sprintf("(from token_cmd: %s)", tokenCmdSummary(p.TokenCmd)))
	}

	path, _ := config.ActivePath()

	// Quiet prints the profile API commands would use.
	if out.Quiet(profileName) {
		return nil
	}

	switch out.Format {
	case output.FormatJSON:
		return out.JSON(map[string]string{
			"profile":  profileName,
			"username": id.Username,
			"endpoint": endpoint,
			"config":   path,
		})
	case output.FormatYAML:
		return out.YAML(map[string]string{
			"profile":  profileName,
			"username": id.Username,
			"endpoint": endpoint,
			"config":   path,
		})
	default:
		out.KVTable([][]string{
			{"Profile", profileName},
			{"Username", id.Username},
			{"Token", tokenDisplay},
			{"Endpoint", endpoint},
			{"Config file", out.Dim(path)},
		})
	}
	return nil
}
