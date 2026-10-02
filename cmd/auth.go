package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/patramsey/namecom-cli/cmd/cmdutil"
	configcmd "github.com/patramsey/namecom-cli/cmd/config"
	"github.com/patramsey/namecom-cli/internal/api"
	"github.com/patramsey/namecom-cli/internal/config"
	"github.com/patramsey/namecom-cli/internal/output"
	"github.com/spf13/cobra"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage name.com API credentials",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Configure credentials interactively",
	Example: `  namecom auth login
  namecom auth login --profile staging
  namecom auth login --profile sandbox --sandbox`,
	Args: cobra.NoArgs,
	RunE: runAuthLogin,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Verify credentials by calling the API hello endpoint",
	Example: `  namecom auth status
  namecom auth status --profile staging`,
	Args: cobra.NoArgs,
	RunE: runAuthStatus,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove credentials for the active profile",
	Example: `  namecom auth logout
  namecom auth logout --profile staging`,
	Args: cobra.NoArgs,
	RunE: runAuthLogout,
}

var loginProfile string
var logoutProfile string

func init() {
	authLoginCmd.Flags().StringVar(&loginProfile, "profile", "default", "profile name to save credentials under")
	authLogoutCmd.Flags().StringVar(&logoutProfile, "profile", "", "profile to remove (defaults to the active profile)")
	// logout's local --profile shadows the global one, completion included.
	// login's names a profile that may not exist yet, so it offers none.
	_ = authLogoutCmd.RegisterFlagCompletionFunc("profile", cmdutil.CompleteProfiles)
	cmdutil.GroupCmd(authCmd)
	authCmd.AddCommand(authLoginCmd, authStatusCmd, authLogoutCmd)
	rootCmd.AddCommand(authCmd)
}

func runAuthLogin(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	if !output.IsInteractive() {
		return fmt.Errorf("auth login requires an interactive terminal; " +
			"set credentials via NAMECOM_USERNAME and NAMECOM_TOKEN environment variables instead")
	}

	// --sandbox answers the sandbox question. The form's answer used to start
	// false and the flag was never read, so `auth login --sandbox` saved a
	// production profile unless the user also said Yes at the prompt.
	sandbox := cmdutil.IsSandbox(cmd)
	a := loginAnswers{Sandbox: sandbox}
	if err := askLogin(&a, !sandbox); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			out.Warn("aborted")
			return nil
		}
		return fmt.Errorf("form: %w", err)
	}

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if cfgFile.Profiles == nil {
		cfgFile.Profiles = make(map[string]config.Profile)
	}
	cfgFile.Profiles[loginProfile] = config.Profile{
		Username: a.Username,
		Token:    a.Token,
		Sandbox:  a.Sandbox,
	}
	if cfgFile.Default == "" {
		cfgFile.Default = loginProfile
	}
	if cmdutil.IsDryRun(cmd) {
		// The form still runs, so the preview can say what would be saved —
		// everything but the token.
		configcmd.PreviewChange(out, configcmd.Change{
			Action:   "save_profile",
			Profile:  loginProfile,
			Username: a.Username,
			Sandbox:  &a.Sandbox,
			Default:  cfgFile.Default,
			Summary:  fmt.Sprintf("save profile %q (username %s, %s)", loginProfile, a.Username, api.DefaultBaseURL(a.Sandbox)),
		})
		return nil
	}
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	path, _ := config.ActivePath()
	out.Success(fmt.Sprintf("Credentials saved to %s (profile: %s)", path, loginProfile))
	out.Hint("Run 'namecom status' to see your account overview")
	out.Hint("Enable tab completion: run 'namecom completion --help' for shell setup instructions")
	return nil
}

// loginAnswers holds what the login form collects.
type loginAnswers struct {
	Username string
	Token    string
	Sandbox  bool
}

// askLogin runs the login form, filling a. The sandbox question is asked only
// when askSandbox is set; otherwise a.Sandbox is kept as given. It is
// replaceable in tests: the token field is a password input, which huh reads
// from a terminal even in accessible mode.
var askLogin = func(a *loginAnswers, askSandbox bool) error {
	fields := []huh.Field{
		huh.NewInput().
			Title("Username").
			Description("Your name.com API username (shown in the API settings page)").
			Placeholder("yourname").
			Value(&a.Username).
			Validate(func(s string) error {
				if s == "" {
					return errors.New("username is required")
				}
				return nil
			}),

		huh.NewInput().
			Title("API Token").
			Description("Your name.com API token — kept secret in the config file (chmod 600)").
			Placeholder("••••••••••••••••").
			EchoMode(huh.EchoModePassword).
			Value(&a.Token).
			Validate(func(s string) error {
				if s == "" {
					return errors.New("token is required")
				}
				return nil
			}),
	}
	if askSandbox {
		fields = append(fields, huh.NewConfirm().
			Title("Use sandbox API?").
			Description("Sends requests to api.dev.name.com instead of api.name.com").
			Value(&a.Sandbox))
	}
	return huh.NewForm(huh.NewGroup(fields...)).Run()
}

func runAuthStatus(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	// auth status needs the client; init it explicitly since PersistentPreRunE
	// is skipped for the auth group.
	if err := initContext(cmd); err != nil {
		return err
	}
	client := cmdutil.APIClient(cmd)

	stop := out.Spin("Checking credentials…")
	// Hello lives on the SDK's root client rather than a sub-client — it is the
	// API's credential check, not part of a resource group.
	_, err := client.SDK().Hello(cmd.Context())
	stop()

	// Report the identity the Hello call just used, resolved the same way.
	id := config.Identity(cmdutil.CfgFile(cmd), cmdutil.Overrides(cmd))
	if err != nil {
		// The error hints send users here to see which credentials are in
		// use, so a rejection says which ones were rejected rather than only
		// "Unauthorized" (#187). Wrapped, so the exit code is unchanged.
		cfgPath, _ := config.ActivePath()
		return fmt.Errorf("%w (profile %q, username %q, endpoint %s, config %s)",
			api.FromSDKError(err), id.Profile, id.Username, client.BaseURL(), cfgPath)
	}

	env := "production"
	if client.BaseURL() == "https://api.dev.name.com" {
		env = "sandbox"
	}

	cfgPath, _ := config.ActivePath()

	renderAuthStatus(out, [][]string{
		{"Profile", id.Profile},
		{"Username", id.Username},
		{"Environment", env},
		{"Endpoint", client.BaseURL()},
		{"Config", cfgPath},
	})
	return nil
}

// renderAuthStatus emits the credential summary in whichever format was asked
// for.
//
// This is the command CI runs to verify credentials before doing real work, so
// its output has to be parseable. It previously called out.Success followed by
// out.KVTable — and KVTable has no format guard, so `-o json` produced a JSON
// envelope followed by an ASCII table, which fails on the second document.
//
// Keys are lowercased for the structured views so they read as field names
// rather than display labels.
//
// Quiet prints the username the credentials verified as: the identity, which
// is what a script checking "am I logged in as the right account" compares.
func renderAuthStatus(out *output.Config, rows [][]string) {
	if out.QuietMode {
		for _, r := range rows {
			if r[0] == "Username" {
				out.Quiet(r[1])
			}
		}
		return
	}
	switch out.Format {
	case output.FormatJSON, output.FormatYAML:
		fields := make(map[string]any, len(rows)+1)
		for _, r := range rows {
			fields[strings.ToLower(strings.ReplaceAll(r[0], " ", "_"))] = r[1]
		}
		// A boolean, not the string "true" it used to be (#187).
		fields["verified"] = true
		if out.Format == output.FormatJSON {
			_ = out.JSON(fields)
			return
		}
		_ = out.YAML(fields)
	default:
		out.Success("Credentials verified")
		out.KVTable(rows)
	}
}

func runAuthLogout(cmd *cobra.Command, _ []string) error {
	out := cmdutil.Out(cmd)

	cfgFile, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// The profile API commands would use, so logout removes the credential in
	// use and not, say, prod when NAMECOM_PROFILE names staging.
	profile := config.ActiveProfile(cfgFile, logoutProfile)
	if profile == "" {
		if len(cfgFile.Profiles) == 0 {
			return errors.New("no profiles configured")
		}
		return fmt.Errorf("%d profiles exist but none is the default — pass --profile to choose one", len(cfgFile.Profiles))
	}

	if _, ok := cfgFile.Profiles[profile]; !ok {
		return fmt.Errorf("profile %q not found in config", profile)
	}
	delete(cfgFile.Profiles, profile)
	if cfgFile.Default == profile {
		cfgFile.Default = ""
	}
	if cmdutil.IsDryRun(cmd) {
		// --dry-run describes the removal and keeps the file; it used to
		// delete the profile.
		configcmd.PreviewChange(out, configcmd.Change{
			Action:  "remove_profile",
			Profile: profile,
			Default: cfgFile.Default,
			Summary: fmt.Sprintf("remove profile %q", profile),
		})
		return nil
	}
	if err := config.Save(cfgFile); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	out.Success(fmt.Sprintf("Removed profile %q", profile))
	return nil
}
