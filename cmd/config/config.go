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
	Use: "list-profiles",
	// Aliases rather than a rename: every other group lists with `list`, but
	// `config list` reads as listing settings, and renaming would break the
	// scripts and docs that already call list-profiles (#237).
	Aliases: []string{"profiles", "ls"},
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
	cmdutil.MarkWrite(useCmd)
	cmdutil.MarkList(listProfilesCmd)
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
		// An empty list is still a list: a script reading .data gets [],
		// not an empty stdout it cannot parse (#240).
		switch out.Format {
		case output.FormatJSON:
			return out.JSONList([]profileView{}, nil, 0)
		case output.FormatYAML:
			return out.YAMLList([]profileView{}, nil, 0)
		}
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

	// In the {"data": [...]} envelope every list uses; it was a bare array
	// (#240).
	switch out.Format {
	case output.FormatJSON:
		return out.JSONList(redactProfiles(cfgFile, names, active), nil, 0)
	case output.FormatYAML:
		return out.YAMLList(redactProfiles(cfgFile, names, active), nil, 0)
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

	// Describe exactly what API commands would use, and fail exactly when they
	// would: the same profile check, then config.Identity, which applies
	// Resolve's precedence without running token_cmd, then the same test for a
	// usable pair. A chain of its own here reported the wrong profile or
	// endpoint, told a user with one working profile to run `auth login`,
	// which overwrites, and reported a missing profile when the credentials
	// came from the environment and no config file existed (#246).
	ov := cmdutil.Overrides(cmd)
	if err := cmdutil.RequireProfile(cfgFile, ov); err != nil {
		return err
	}
	id, err := config.Identity(cfgFile, ov)
	if err == nil {
		err = config.CheckComplete(cfgFile, id)
	}
	if err != nil {
		if classified := cmdutil.CredentialsError(err, ov); classified != nil {
			return classified
		}
		return cmdutil.NewAuthError(err)
	}
	profileName := id.Profile
	p := cfgFile.Profiles[profileName]

	// The base URL, scheme included, as auth status prints it. A bare host here
	// put the same value in two forms across the two commands.
	endpoint := id.BaseURL
	if endpoint == "" {
		endpoint = api.DefaultBaseURL(id.Sandbox)
	}
	tokenDisplay := "••••••••" //nolint:gosec // G101 false positive: a mask shown in place of the token, not a credential
	if id.Sources.Token == config.SourceTokenCmd {
		tokenDisplay = out.Dim(fmt.Sprintf("(from token_cmd: %s)", tokenCmdSummary(p.TokenCmd)))
	}

	path, _ := config.ActivePath()

	// Quiet prints the profile API commands would use.
	if out.Quiet(profileName) {
		return nil
	}

	// Each value's source is a sibling key rather than an object in place of
	// the value, so scripts reading "username" as a string keep working.
	fields := map[string]string{
		"profile":        profileName,
		"profileSource":  id.Sources.Profile,
		"username":       id.Username,
		"usernameSource": id.Sources.Username,
		"tokenSource":    id.Sources.Token,
		"endpoint":       endpoint,
		"endpointSource": id.Sources.Endpoint(),
		"config":         path,
	}
	switch out.Format {
	case output.FormatJSON:
		return out.JSON(fields)
	case output.FormatYAML:
		return out.YAML(fields)
	case output.FormatTSV:
		// The JSON document's keys and bare values. TSV printed the table's
		// labels, each value with its source appended (#289).
		return out.TSVObject(fields)
	default:
		profileDisplay := profileName
		if profileDisplay == "" {
			profileDisplay = out.Dim("(none)")
		}
		tokenSource := id.Sources.Token
		if tokenSource == config.SourceTokenCmd {
			tokenSource = "" // the display already says so
		}
		out.KVTable([][]string{
			{"Profile", WithSource(out, profileDisplay, id.Sources.Profile)},
			{"Username", WithSource(out, id.Username, id.Sources.Username)},
			{"Token", WithSource(out, tokenDisplay, tokenSource)},
			{"Endpoint", WithSource(out, endpoint, id.Sources.Endpoint())},
			{"Config file", out.Dim(path)},
		})
	}
	return nil
}

// WithSource appends where a value came from, dimmed, for the table views of
// config show and auth status.
func WithSource(out *output.Config, value, source string) string {
	if source == "" {
		return value
	}
	return value + "  " + out.Dim("("+source+")")
}
